package transport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RefillControllerConfig 描述补池控制循环配置。
type RefillControllerConfig struct {
	MinIdleTunnels         int
	MaxIdleTunnels         int
	MaxInFlightTunnelOpens int
	TunnelOpenRateLimit    rate.Limit
	TunnelOpenBurst        int
	RequestDeduplicateTTL  time.Duration
}

// NormalizeAndValidate 归一化并校验补池配置。
func (config RefillControllerConfig) NormalizeAndValidate() (RefillControllerConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MinIdleTunnels < 0 {
		// min_idle_tunnels 负值统一归零。
		normalizedConfig.MinIdleTunnels = 0
	}
	if normalizedConfig.MaxIdleTunnels <= 0 {
		// 未设置 max 时启用保守默认值。
		normalizedConfig.MaxIdleTunnels = 1024
	}
	if normalizedConfig.MinIdleTunnels > normalizedConfig.MaxIdleTunnels {
		// min 不能超过 max，避免矛盾配置。
		return RefillControllerConfig{}, fmt.Errorf(
			"normalize refill controller config: %w: min_idle=%d max_idle=%d",
			ErrInvalidArgument,
			normalizedConfig.MinIdleTunnels,
			normalizedConfig.MaxIdleTunnels,
		)
	}
	if normalizedConfig.MaxInFlightTunnelOpens <= 0 {
		// 未配置 inflight 时使用默认值，限制并发建连风暴。
		normalizedConfig.MaxInFlightTunnelOpens = 4
	}
	if normalizedConfig.TunnelOpenRateLimit <= 0 {
		// 速率限制必须大于 0。
		return RefillControllerConfig{}, fmt.Errorf(
			"normalize refill controller config: %w: tunnel_open_rate_limit=%v",
			ErrInvalidArgument,
			normalizedConfig.TunnelOpenRateLimit,
		)
	}
	if normalizedConfig.TunnelOpenBurst <= 0 {
		// burst 需为正数，避免 limiter 永远不可用。
		return RefillControllerConfig{}, fmt.Errorf(
			"normalize refill controller config: %w: tunnel_open_burst=%d",
			ErrInvalidArgument,
			normalizedConfig.TunnelOpenBurst,
		)
	}
	if normalizedConfig.RequestDeduplicateTTL <= 0 {
		// 去重窗口未设置时采用默认值。
		normalizedConfig.RequestDeduplicateTTL = 10 * time.Minute
	}
	return normalizedConfig, nil
}

// DefaultRefillControllerConfig 返回默认补池配置。
func DefaultRefillControllerConfig() RefillControllerConfig {
	return RefillControllerConfig{
		MinIdleTunnels:         0,
		MaxIdleTunnels:         1024,
		MaxInFlightTunnelOpens: 4,
		TunnelOpenRateLimit:    rate.Limit(20),
		TunnelOpenBurst:        10,
		RequestDeduplicateTTL:  10 * time.Minute,
	}
}

// RefillResult 描述一次补池动作结果。
type RefillResult struct {
	RequestID           string
	Deduplicated        bool
	BeforeIdleCount     int
	RequestedTargetIdle int
	EffectiveTargetIdle int
	OpenedCount         int
	FailedCount         int
	AfterIdleCount      int
	FirstFailure        error
}

// RefillController 管理补池、限流和去重逻辑。
type RefillController struct {
	config RefillControllerConfig

	pool     TunnelPool
	producer TunnelProducer

	openRateLimiter   *rate.Limiter
	requestDedupStore *RefillRequestDeduplicator
}

// NewRefillController 创建补池控制器。
func NewRefillController(pool TunnelPool, producer TunnelProducer, config RefillControllerConfig) (*RefillController, error) {
	if pool == nil || producer == nil {
		// pool/producer 是控制器核心依赖，不能为空。
		return nil, fmt.Errorf("new refill controller: %w: nil dependency", ErrInvalidArgument)
	}
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &RefillController{
		config:            normalizedConfig,
		pool:              pool,
		producer:          producer,
		openRateLimiter:   rate.NewLimiter(normalizedConfig.TunnelOpenRateLimit, normalizedConfig.TunnelOpenBurst),
		requestDedupStore: NewRefillRequestDeduplicator(normalizedConfig.RequestDeduplicateTTL),
	}, nil
}

// HandleRefillRequest 处理控制面补池请求（包含幂等去重）。
func (controller *RefillController) HandleRefillRequest(ctx context.Context, request TunnelRefillRequest) (RefillResult, error) {
	normalizedRequest := request.Normalize()
	if err := normalizedRequest.Validate(); err != nil {
		return RefillResult{}, err
	}
	result := RefillResult{
		RequestID: strings.TrimSpace(normalizedRequest.RequestID),
	}
	if !controller.requestDedupStore.MarkSeen(normalizedRequest.RequestID, time.Now().UTC()) {
		// 重复 request_id 直接视为幂等命中并返回。
		result.Deduplicated = true
		result.BeforeIdleCount = controller.pool.IdleCount()
		result.AfterIdleCount = result.BeforeIdleCount
		result.RequestedTargetIdle = result.BeforeIdleCount + normalizedRequest.RequestedIdleDelta
		result.EffectiveTargetIdle = controller.clampTargetIdle(result.RequestedTargetIdle)
		return result, nil
	}
	requestedTargetIdle := controller.pool.IdleCount() + normalizedRequest.RequestedIdleDelta
	refillResult, err := controller.RefillToTarget(ctx, requestedTargetIdle)
	refillResult.RequestID = result.RequestID
	return refillResult, err
}

// RefillToTarget 将空闲池平滑补充到目标容量。
func (controller *RefillController) RefillToTarget(ctx context.Context, targetIdle int) (RefillResult, error) {
	if ctx == nil {
		// 兜底 context，避免调用方传 nil 时 panic。
		ctx = context.Background()
	}
	beforeIdleCount := controller.pool.IdleCount()
	effectiveTargetIdle := controller.clampTargetIdle(targetIdle)
	result := RefillResult{
		BeforeIdleCount:     beforeIdleCount,
		RequestedTargetIdle: targetIdle,
		EffectiveTargetIdle: effectiveTargetIdle,
	}
	if beforeIdleCount >= effectiveTargetIdle {
		// 当前 idle 已满足目标时直接返回，无需补池。
		result.AfterIdleCount = beforeIdleCount
		return result, nil
	}

	tunnelCountToOpen := effectiveTargetIdle - beforeIdleCount
	workerCount := tunnelCountToOpen
	if workerCount > controller.config.MaxInFlightTunnelOpens {
		// worker 数受 max_inflight_tunnel_opens 约束。
		workerCount = controller.config.MaxInFlightTunnelOpens
	}

	openJobChannel := make(chan struct{}, tunnelCountToOpen)
	for index := 0; index < tunnelCountToOpen; index++ {
		// 预填充任务通道，让 worker 按固定任务量执行。
		openJobChannel <- struct{}{}
	}
	close(openJobChannel)

	var resultMutex sync.Mutex
	var workerGroup sync.WaitGroup
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for range openJobChannel {
				if err := controller.openRateLimiter.Wait(ctx); err != nil {
					// context 取消或 limiter 异常时记录失败并停止该 worker。
					resultMutex.Lock()
					result.FailedCount++
					if result.FirstFailure == nil {
						result.FirstFailure = fmt.Errorf("refill wait limiter: %w", err)
					}
					resultMutex.Unlock()
					return
				}
				tunnel, err := controller.producer.OpenTunnel(ctx)
				if err != nil {
					// 打开 tunnel 失败时记录失败计数，但继续处理后续任务。
					resultMutex.Lock()
					result.FailedCount++
					if result.FirstFailure == nil {
						result.FirstFailure = fmt.Errorf("refill open tunnel: %w", err)
					}
					resultMutex.Unlock()
					continue
				}
				if err := controller.pool.PutIdle(tunnel); err != nil {
					// 入池失败时主动关闭 tunnel，避免留下未追踪资源。
					cleanupErr := tunnel.Close()
					resultMutex.Lock()
					result.FailedCount++
					if result.FirstFailure == nil {
						if cleanupErr != nil {
							result.FirstFailure = fmt.Errorf("refill put idle: %w", errors.Join(err, cleanupErr))
						} else {
							result.FirstFailure = fmt.Errorf("refill put idle: %w", err)
						}
					}
					resultMutex.Unlock()
					continue
				}
				resultMutex.Lock()
				// 成功入池后累计 opened 数。
				result.OpenedCount++
				resultMutex.Unlock()
			}
		}()
	}
	workerGroup.Wait()

	result.AfterIdleCount = controller.pool.IdleCount()
	if result.FirstFailure != nil {
		// 存在失败时返回首个错误供上层告警。
		return result, result.FirstFailure
	}
	return result, nil
}

// clampTargetIdle 将目标容量限制在 [min_idle, max_idle] 区间。
func (controller *RefillController) clampTargetIdle(targetIdle int) int {
	if targetIdle < controller.config.MinIdleTunnels {
		// 小于下限时提升到最小容量。
		return controller.config.MinIdleTunnels
	}
	if targetIdle > controller.config.MaxIdleTunnels {
		// 大于上限时截断到最大容量。
		return controller.config.MaxIdleTunnels
	}
	return targetIdle
}
