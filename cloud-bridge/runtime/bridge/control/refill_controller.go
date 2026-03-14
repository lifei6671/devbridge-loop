package control

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// defaultRefillTriggerThreshold 定义默认低水位阈值（idle<=2 时触发补池请求）。
	defaultRefillTriggerThreshold = 2
	// defaultRefillRequestCooldown 定义同会话补池请求的最小间隔。
	defaultRefillRequestCooldown = 600 * time.Millisecond
	// defaultMinRequestedIdleDelta 定义最小补池增量。
	defaultMinRequestedIdleDelta = 1
	// defaultMaxRequestedIdleDelta 定义单次补池最大增量，避免突发放量过大。
	defaultMaxRequestedIdleDelta = 32
)

// RefillControllerConfig 定义 Bridge 补池决策参数。
type RefillControllerConfig struct {
	TriggerThreshold int
	RequestCooldown  time.Duration
	MinRequestDelta  int
	MaxRequestDelta  int
}

// NormalizeAndValidate 回填补池决策配置并校验边界。
func (config RefillControllerConfig) NormalizeAndValidate() RefillControllerConfig {
	normalizedConfig := config
	if normalizedConfig.TriggerThreshold < 0 {
		normalizedConfig.TriggerThreshold = 0
	}
	if normalizedConfig.TriggerThreshold == 0 {
		normalizedConfig.TriggerThreshold = defaultRefillTriggerThreshold
	}
	if normalizedConfig.RequestCooldown <= 0 {
		normalizedConfig.RequestCooldown = defaultRefillRequestCooldown
	}
	if normalizedConfig.MinRequestDelta <= 0 {
		normalizedConfig.MinRequestDelta = defaultMinRequestedIdleDelta
	}
	if normalizedConfig.MaxRequestDelta <= 0 {
		normalizedConfig.MaxRequestDelta = defaultMaxRequestedIdleDelta
	}
	if normalizedConfig.MaxRequestDelta < normalizedConfig.MinRequestDelta {
		normalizedConfig.MaxRequestDelta = normalizedConfig.MinRequestDelta
	}
	return normalizedConfig
}

// RefillControllerOptions 定义补池控制器构造参数。
type RefillControllerOptions struct {
	Config RefillControllerConfig
	Now    func() time.Time
}

// refillRequestState 记录同会话最近一次补池请求，用于去重和节流。
type refillRequestState struct {
	RequestedAt    time.Time
	RequestedDelta int
}

// RefillController 负责根据 tunnel 池上报判定是否向 Agent 发起补池请求。
type RefillController struct {
	config RefillControllerConfig
	now    func() time.Time

	mutex               sync.Mutex
	lastRequestByStream map[string]refillRequestState
}

// NewRefillController 创建 Bridge 补池控制器。
func NewRefillController(options RefillControllerOptions) *RefillController {
	normalizedConfig := options.Config.NormalizeAndValidate()
	nowFunction := options.Now
	if nowFunction == nil {
		nowFunction = func() time.Time { return time.Now().UTC() }
	}
	return &RefillController{
		config:              normalizedConfig,
		now:                 nowFunction,
		lastRequestByStream: make(map[string]refillRequestState),
	}
}

// BuildRefillRequest 根据池上报判定是否需要发起 TunnelRefillRequest。
func (controller *RefillController) BuildRefillRequest(
	sessionID string,
	sessionEpoch uint64,
	report pb.TunnelPoolReport,
) (pb.TunnelRefillRequest, bool) {
	if controller == nil {
		return pb.TunnelRefillRequest{}, false
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return pb.TunnelRefillRequest{}, false
	}
	targetIdleCount := report.TargetIdleCount
	if targetIdleCount <= 0 {
		// target_idle<=0 表示无需 Bridge 触发补池。
		return pb.TunnelRefillRequest{}, false
	}
	idleCount := report.IdleCount
	if idleCount < 0 {
		idleCount = 0
	}
	// 仅在低水位区间触发补池，避免常态抖动时频繁下发请求。
	if idleCount > controller.config.TriggerThreshold {
		return pb.TunnelRefillRequest{}, false
	}

	requestedIdleDelta := targetIdleCount - idleCount
	if requestedIdleDelta <= 0 {
		return pb.TunnelRefillRequest{}, false
	}
	if requestedIdleDelta < controller.config.MinRequestDelta {
		requestedIdleDelta = controller.config.MinRequestDelta
	}
	if requestedIdleDelta > controller.config.MaxRequestDelta {
		requestedIdleDelta = controller.config.MaxRequestDelta
	}

	streamKey := fmt.Sprintf("%s:%d", normalizedSessionID, sessionEpoch)
	requestedAt := controller.now().UTC()
	if !controller.allowRequest(streamKey, requestedIdleDelta, requestedAt) {
		return pb.TunnelRefillRequest{}, false
	}
	reason := resolveRefillReason(report.Trigger)
	requestID := fmt.Sprintf(
		"refill-%s-%d",
		strings.ReplaceAll(normalizedSessionID, " ", "_"),
		requestedAt.UnixNano(),
	)
	return pb.TunnelRefillRequest{
		SessionID:          normalizedSessionID,
		SessionEpoch:       sessionEpoch,
		RequestID:          requestID,
		RequestedIdleDelta: requestedIdleDelta,
		Reason:             reason,
		TimestampUnix:      requestedAt.Unix(),
		Metadata: map[string]string{
			"idle_count":        strconv.Itoa(idleCount),
			"target_idle_count": strconv.Itoa(targetIdleCount),
			"trigger":           strings.TrimSpace(report.Trigger),
		},
	}, true
}

// allowRequest 校验同会话请求的时间窗口与增量去重规则。
func (controller *RefillController) allowRequest(
	streamKey string,
	requestedIdleDelta int,
	requestedAt time.Time,
) bool {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	lastRequest, exists := controller.lastRequestByStream[streamKey]
	if exists {
		withinCooldown := requestedAt.Sub(lastRequest.RequestedAt) < controller.config.RequestCooldown
		if withinCooldown && requestedIdleDelta <= lastRequest.RequestedDelta {
			// 冷却窗口内且增量未放大时直接抑制重复请求。
			return false
		}
	}
	controller.lastRequestByStream[streamKey] = refillRequestState{
		RequestedAt:    requestedAt,
		RequestedDelta: requestedIdleDelta,
	}
	return true
}

// resolveRefillReason 将上报 trigger 映射为协议内补池原因。
func resolveRefillReason(trigger string) string {
	normalizedTrigger := strings.TrimSpace(strings.ToLower(trigger))
	if strings.Contains(normalizedTrigger, "acquire_timeout") {
		return "acquire_timeout"
	}
	if strings.Contains(normalizedTrigger, "startup") {
		return "startup"
	}
	if strings.Contains(normalizedTrigger, "manual") {
		return "manual"
	}
	return "low_watermark"
}
