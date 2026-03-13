package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
)

var (
	// ErrRefillHandlerDependencyMissing 表示 RefillHandler 关键依赖缺失。
	ErrRefillHandlerDependencyMissing = errors.New("refill handler dependency missing")
	// ErrInvalidRefillRequest 表示补池请求字段不合法。
	ErrInvalidRefillRequest = errors.New("invalid refill request")
)

// TunnelRefillReason 表示补池触发原因。
type TunnelRefillReason string

const (
	// TunnelRefillReasonLowWatermark 表示 idle 低于水位。
	TunnelRefillReasonLowWatermark TunnelRefillReason = "low_watermark"
	// TunnelRefillReasonAcquireTimeout 表示 acquire 超时触发。
	TunnelRefillReasonAcquireTimeout TunnelRefillReason = "acquire_timeout"
	// TunnelRefillReasonStartup 表示启动预热。
	TunnelRefillReasonStartup TunnelRefillReason = "startup"
	// TunnelRefillReasonManual 表示手工触发。
	TunnelRefillReasonManual TunnelRefillReason = "manual"
)

// TunnelRefillRequest 描述 Bridge 侧发起的补池请求。
type TunnelRefillRequest struct {
	SessionID          string
	SessionEpoch       uint64
	RequestID          string
	RequestedIdleDelta int
	Reason             TunnelRefillReason
	Timestamp          time.Time
}

// Normalize 归一化请求字段。
func (request TunnelRefillRequest) Normalize() TunnelRefillRequest {
	normalizedRequest := request
	normalizedRequest.SessionID = strings.TrimSpace(normalizedRequest.SessionID)
	normalizedRequest.RequestID = strings.TrimSpace(normalizedRequest.RequestID)
	normalizedRequest.Reason = TunnelRefillReason(strings.TrimSpace(string(normalizedRequest.Reason)))
	if normalizedRequest.Timestamp.IsZero() {
		// 缺失时间戳时补 UTC now，便于链路追踪。
		normalizedRequest.Timestamp = time.Now().UTC()
	}
	return normalizedRequest
}

// Validate 校验请求语义。
func (request TunnelRefillRequest) Validate() error {
	normalizedRequest := request.Normalize()
	if normalizedRequest.SessionID == "" {
		// session_id 为空无法归属请求。
		return fmt.Errorf("validate refill request: %w: empty session_id", ErrInvalidRefillRequest)
	}
	if normalizedRequest.SessionEpoch == 0 {
		// session_epoch 必须有效。
		return fmt.Errorf("validate refill request: %w: invalid session_epoch", ErrInvalidRefillRequest)
	}
	if normalizedRequest.RequestID == "" {
		// request_id 是幂等键，不能为空。
		return fmt.Errorf("validate refill request: %w: empty request_id", ErrInvalidRefillRequest)
	}
	if normalizedRequest.RequestedIdleDelta <= 0 {
		// requested_idle_delta 需为正数。
		return fmt.Errorf("validate refill request: %w: requested_idle_delta=%d", ErrInvalidRefillRequest, normalizedRequest.RequestedIdleDelta)
	}
	switch normalizedRequest.Reason {
	case "", TunnelRefillReasonLowWatermark, TunnelRefillReasonAcquireTimeout, TunnelRefillReasonStartup, TunnelRefillReasonManual:
		// 允许空 reason，由 Agent 侧补默认值。
	default:
		return fmt.Errorf("validate refill request: %w: reason=%s", ErrInvalidRefillRequest, normalizedRequest.Reason)
	}
	return nil
}

// RefillScheduler 定义补池调度能力。
type RefillScheduler interface {
	// Snapshot 返回当前池统计快照。
	Snapshot() tunnel.Snapshot
	// RequestRefill 请求把 idle 平滑补充到目标值。
	RequestRefill(targetIdle int, reason string) bool
}

// RefillHandlerConfig 定义补池请求处理参数。
type RefillHandlerConfig struct {
	RequestDeduplicateTTL time.Duration
	MaxIdle               int
}

// Normalize 回填 RefillHandler 配置。
func (config RefillHandlerConfig) Normalize() RefillHandlerConfig {
	normalizedConfig := config
	if normalizedConfig.RequestDeduplicateTTL <= 0 {
		// 未配置去重窗口时使用 10 分钟默认值。
		normalizedConfig.RequestDeduplicateTTL = 10 * time.Minute
	}
	if normalizedConfig.MaxIdle < 0 {
		// max_idle 不允许负值。
		normalizedConfig.MaxIdle = 0
	}
	return normalizedConfig
}

// RefillHandleResult 描述单次请求处理结果。
type RefillHandleResult struct {
	RequestID           string
	Deduplicated        bool
	Accepted            bool
	BeforeIdleCount     int
	RequestedTargetIdle int
	EffectiveTargetIdle int
	Reason              TunnelRefillReason
}

// RefillHandler 负责处理 TunnelRefillRequest 的幂等、合并与调度。
type RefillHandler struct {
	scheduler RefillScheduler
	config    RefillHandlerConfig

	mutex               sync.Mutex
	sessionID           string
	sessionEpoch        uint64
	requestSeenAt       map[string]time.Time
	pendingTargetIdle   int
	pendingMergedReason TunnelRefillReason
}

// NewRefillHandler 创建补池请求处理器。
func NewRefillHandler(scheduler RefillScheduler, config RefillHandlerConfig) (*RefillHandler, error) {
	if scheduler == nil {
		// 没有调度器时无法触发补池。
		return nil, fmt.Errorf("new refill handler: %w", ErrRefillHandlerDependencyMissing)
	}
	normalizedConfig := config.Normalize()
	return &RefillHandler{
		scheduler:     scheduler,
		config:        normalizedConfig,
		requestSeenAt: make(map[string]time.Time),
	}, nil
}

// SetSession 更新当前可接受的 session 上下文。
func (handler *RefillHandler) SetSession(sessionID string, sessionEpoch uint64) {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	// session 切换后刷新上下文并清空会话内缓存状态。
	handler.sessionID = strings.TrimSpace(sessionID)
	handler.sessionEpoch = sessionEpoch
	// 去重键仅在单个 session 内有效，切换后必须重置。
	handler.requestSeenAt = make(map[string]time.Time)
	handler.pendingTargetIdle = 0
	handler.pendingMergedReason = ""
}

// Handle 处理单条补池请求并触发平滑补池调度。
func (handler *RefillHandler) Handle(ctx context.Context, request TunnelRefillRequest) (RefillHandleResult, error) {
	normalizedContext := ctx
	if normalizedContext == nil {
		// nil context 时兜底 Background。
		normalizedContext = context.Background()
	}
	select {
	case <-normalizedContext.Done():
		// 外部已取消时直接返回。
		return RefillHandleResult{}, normalizedContext.Err()
	default:
	}

	normalizedRequest := request.Normalize()
	if err := normalizedRequest.Validate(); err != nil {
		return RefillHandleResult{}, err
	}

	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	now := normalizedRequest.Timestamp
	handler.cleanupSeenLocked(now)
	if !handler.matchSessionLocked(normalizedRequest) {
		// 非当前会话请求拒绝处理，避免旧会话污染。
		return RefillHandleResult{}, fmt.Errorf(
			"handle refill request: %w: stale session request session_id=%s epoch=%d",
			ErrInvalidRefillRequest,
			normalizedRequest.SessionID,
			normalizedRequest.SessionEpoch,
		)
	}

	result := RefillHandleResult{
		RequestID:       normalizedRequest.RequestID,
		Reason:          normalizedRequest.Reason,
		BeforeIdleCount: handler.scheduler.Snapshot().IdleCount,
	}
	if _, exists := handler.requestSeenAt[normalizedRequest.RequestID]; exists {
		// request_id 重复时按幂等命中返回。
		result.Deduplicated = true
		result.RequestedTargetIdle = result.BeforeIdleCount + normalizedRequest.RequestedIdleDelta
		result.EffectiveTargetIdle = handler.clampTargetIdle(result.RequestedTargetIdle)
		return result, nil
	}
	handler.requestSeenAt[normalizedRequest.RequestID] = now

	result.RequestedTargetIdle = result.BeforeIdleCount + normalizedRequest.RequestedIdleDelta
	result.EffectiveTargetIdle = handler.clampTargetIdle(result.RequestedTargetIdle)
	mergedTargetIdle := result.EffectiveTargetIdle
	if handler.pendingTargetIdle > mergedTargetIdle {
		// 多请求合并时按更高目标处理，避免重复建连。
		mergedTargetIdle = handler.pendingTargetIdle
	}
	handler.pendingTargetIdle = mergedTargetIdle
	if normalizedRequest.Reason != "" {
		// 有明确 reason 时覆盖为最新触发原因。
		handler.pendingMergedReason = normalizedRequest.Reason
	}
	reason := handler.pendingMergedReason
	if reason == "" {
		// 无 reason 时回落到低水位触发。
		reason = TunnelRefillReasonLowWatermark
	}

	accepted := handler.scheduler.RequestRefill(mergedTargetIdle, string(reason))
	result.Accepted = accepted
	if accepted {
		// 请求已进入调度后清空本地 pending，后续继续合并新请求。
		handler.pendingTargetIdle = 0
		handler.pendingMergedReason = ""
	}
	return result, nil
}

// cleanupSeenLocked 清理过期 request_id 去重记录。
func (handler *RefillHandler) cleanupSeenLocked(now time.Time) {
	expiredBefore := now.Add(-handler.config.RequestDeduplicateTTL)
	for requestID, seenAt := range handler.requestSeenAt {
		if seenAt.Before(expiredBefore) {
			// 过期记录删除，避免去重缓存无界增长。
			delete(handler.requestSeenAt, requestID)
		}
	}
}

// matchSessionLocked 校验请求是否属于当前 session。
func (handler *RefillHandler) matchSessionLocked(request TunnelRefillRequest) bool {
	if handler.sessionID == "" || handler.sessionEpoch == 0 {
		// 尚未设置 session 上下文时放行首批请求。
		return true
	}
	if request.SessionID != handler.sessionID {
		return false
	}
	if request.SessionEpoch != handler.sessionEpoch {
		return false
	}
	return true
}

// clampTargetIdle 将目标值限制到合法区间。
func (handler *RefillHandler) clampTargetIdle(targetIdle int) int {
	effectiveTargetIdle := targetIdle
	if effectiveTargetIdle < 0 {
		// 理论上不会出现，兜底归零。
		effectiveTargetIdle = 0
	}
	if handler.config.MaxIdle > 0 && effectiveTargetIdle > handler.config.MaxIdle {
		// 配置了 max_idle 时按上限截断。
		effectiveTargetIdle = handler.config.MaxIdle
	}
	return effectiveTargetIdle
}
