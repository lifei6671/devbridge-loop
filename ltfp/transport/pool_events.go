package transport

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TunnelPoolReport 描述当前会话的池状态上报。
type TunnelPoolReport struct {
	SessionID       string
	SessionEpoch    uint64
	IdleCount       int
	InUseCount      int
	TargetIdleCount int
	Timestamp       time.Time
}

// Validate 校验 TunnelPoolReport 字段合法性。
func (report TunnelPoolReport) Validate() error {
	if strings.TrimSpace(report.SessionID) == "" {
		// session_id 为空会导致上报无法归属。
		return fmt.Errorf("validate tunnel pool report: %w: empty session_id", ErrInvalidArgument)
	}
	if report.SessionEpoch == 0 {
		// session_epoch 必须大于 0，避免混淆旧会话数据。
		return fmt.Errorf("validate tunnel pool report: %w: invalid session_epoch", ErrInvalidArgument)
	}
	if report.IdleCount < 0 || report.InUseCount < 0 || report.TargetIdleCount < 0 {
		// 容量计数都应为非负数。
		return fmt.Errorf("validate tunnel pool report: %w: negative counter", ErrInvalidArgument)
	}
	return nil
}

// TunnelRefillReason 表示补池触发原因。
type TunnelRefillReason string

const (
	// TunnelRefillReasonLowWatermark 表示 idle 低于水位线。
	TunnelRefillReasonLowWatermark TunnelRefillReason = "low_watermark"
	// TunnelRefillReasonAcquireTimeout 表示 acquire 超时触发补池。
	TunnelRefillReasonAcquireTimeout TunnelRefillReason = "acquire_timeout"
	// TunnelRefillReasonStartup 表示启动阶段预热补池。
	TunnelRefillReasonStartup TunnelRefillReason = "startup"
	// TunnelRefillReasonManual 表示人工触发补池。
	TunnelRefillReasonManual TunnelRefillReason = "manual"
)

// TunnelRefillRequest 描述控制面补池请求。
type TunnelRefillRequest struct {
	SessionID          string
	SessionEpoch       uint64
	RequestID          string
	RequestedIdleDelta int
	Reason             TunnelRefillReason
	Timestamp          time.Time
}

// Normalize 归一化补池请求字段。
func (request TunnelRefillRequest) Normalize() TunnelRefillRequest {
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.Reason = TunnelRefillReason(strings.TrimSpace(string(request.Reason)))
	if request.Timestamp.IsZero() {
		// 未提供时间戳时自动补当前时间，便于链路追踪。
		request.Timestamp = time.Now().UTC()
	}
	return request
}

// Validate 校验补池请求是否合法。
func (request TunnelRefillRequest) Validate() error {
	normalizedRequest := request.Normalize()
	if normalizedRequest.SessionID == "" {
		// session_id 为空时无法做归属校验。
		return fmt.Errorf("validate tunnel refill request: %w: empty session_id", ErrInvalidArgument)
	}
	if normalizedRequest.SessionEpoch == 0 {
		// session_epoch 必须有效。
		return fmt.Errorf("validate tunnel refill request: %w: invalid session_epoch", ErrInvalidArgument)
	}
	if normalizedRequest.RequestID == "" {
		// request_id 是幂等键，不能为空。
		return fmt.Errorf("validate tunnel refill request: %w: empty request_id", ErrInvalidArgument)
	}
	if normalizedRequest.RequestedIdleDelta <= 0 {
		// delta 必须为正数，避免无意义请求。
		return fmt.Errorf("validate tunnel refill request: %w: requested_idle_delta=%d", ErrInvalidArgument, normalizedRequest.RequestedIdleDelta)
	}
	switch normalizedRequest.Reason {
	case "", TunnelRefillReasonLowWatermark, TunnelRefillReasonAcquireTimeout, TunnelRefillReasonStartup, TunnelRefillReasonManual:
		// 允许空 reason，调用方可按需补充。
	default:
		// 未知 reason 直接拒绝，防止写入脏枚举。
		return fmt.Errorf("validate tunnel refill request: %w: reason=%s", ErrInvalidArgument, normalizedRequest.Reason)
	}
	return nil
}

// RefillRequestDeduplicator 维护补池请求去重窗口。
type RefillRequestDeduplicator struct {
	mutex sync.Mutex

	requestSeenAt map[string]time.Time
	ttl           time.Duration
}

// NewRefillRequestDeduplicator 创建请求去重器。
func NewRefillRequestDeduplicator(ttl time.Duration) *RefillRequestDeduplicator {
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		// 未配置 TTL 时采用默认值，避免 map 无界增长。
		normalizedTTL = 10 * time.Minute
	}
	return &RefillRequestDeduplicator{
		requestSeenAt: make(map[string]time.Time),
		ttl:           normalizedTTL,
	}
}

// MarkSeen 标记 requestId 已处理，返回是否首次处理。
func (deduplicator *RefillRequestDeduplicator) MarkSeen(requestID string, now time.Time) bool {
	normalizedRequestID := strings.TrimSpace(requestID)
	if normalizedRequestID == "" {
		// 空 request_id 无法去重，按首次处理。
		return true
	}
	if now.IsZero() {
		// 未提供当前时间时自动填充 UTC now。
		now = time.Now().UTC()
	}
	deduplicator.mutex.Lock()
	defer deduplicator.mutex.Unlock()
	deduplicator.cleanupLocked(now)
	_, exists := deduplicator.requestSeenAt[normalizedRequestID]
	if exists {
		// 已存在说明是重复请求。
		return false
	}
	deduplicator.requestSeenAt[normalizedRequestID] = now
	return true
}

// cleanupLocked 清理过期去重记录。
func (deduplicator *RefillRequestDeduplicator) cleanupLocked(now time.Time) {
	expiredBefore := now.Add(-deduplicator.ttl)
	for requestID, seenAt := range deduplicator.requestSeenAt {
		if seenAt.Before(expiredBefore) {
			// 过期记录删除，避免请求去重缓存无限增长。
			delete(deduplicator.requestSeenAt, requestID)
		}
	}
}
