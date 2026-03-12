package transport

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// countingTunnelProducer 是统计并发行为的测试 producer。
type countingTunnelProducer struct {
	mutex sync.Mutex

	openDelay time.Duration
	nextID    int

	currentConcurrentOpens int
	maxConcurrentOpens     int
}

// OpenTunnel 打开一条测试 tunnel 并记录并发峰值。
func (producer *countingTunnelProducer) OpenTunnel(ctx context.Context) (Tunnel, error) {
	producer.mutex.Lock()
	producer.currentConcurrentOpens++
	if producer.currentConcurrentOpens > producer.maxConcurrentOpens {
		// 持续记录最大并发建连数，用于验证 inflight 上限。
		producer.maxConcurrentOpens = producer.currentConcurrentOpens
	}
	producer.nextID++
	tunnelID := producer.nextID
	producer.mutex.Unlock()

	if producer.openDelay > 0 {
		select {
		case <-ctx.Done():
			// 上下文取消时直接返回，避免测试 goroutine 泄漏。
			producer.mutex.Lock()
			producer.currentConcurrentOpens--
			producer.mutex.Unlock()
			return nil, ctx.Err()
		case <-time.After(producer.openDelay):
			// 模拟真实建连耗时。
		}
	}

	producer.mutex.Lock()
	producer.currentConcurrentOpens--
	producer.mutex.Unlock()

	// 每次建连成功返回一个 idle tunnel 测试桩。
	return newIdleMockTunnel("producer-tunnel-" + strconv.Itoa(tunnelID)), nil
}

// MaxConcurrentOpens 返回测试期间观测到的并发峰值。
func (producer *countingTunnelProducer) MaxConcurrentOpens() int {
	producer.mutex.Lock()
	defer producer.mutex.Unlock()
	// 返回峰值供断言使用。
	return producer.maxConcurrentOpens
}

// closeTrackingTunnel 用于验证回填失败时是否关闭 tunnel。
type closeTrackingTunnel struct {
	*mockTunnel

	mutex      sync.Mutex
	closeCount int
}

// newCloseTrackingTunnel 创建带关闭计数的测试 tunnel。
func newCloseTrackingTunnel(tunnelID string) *closeTrackingTunnel {
	return &closeTrackingTunnel{
		mockTunnel: newIdleMockTunnel(tunnelID),
	}
}

// Close 记录关闭次数。
func (tunnel *closeTrackingTunnel) Close() error {
	tunnel.mutex.Lock()
	defer tunnel.mutex.Unlock()
	tunnel.closeCount++
	tunnel.lastError = ErrClosed
	return nil
}

// CloseCount 返回关闭次数。
func (tunnel *closeTrackingTunnel) CloseCount() int {
	tunnel.mutex.Lock()
	defer tunnel.mutex.Unlock()
	return tunnel.closeCount
}

// singleTunnelProducer 每次返回同一条测试 tunnel。
type singleTunnelProducer struct {
	tunnel Tunnel
}

// OpenTunnel 返回预置 tunnel。
func (producer *singleTunnelProducer) OpenTunnel(ctx context.Context) (Tunnel, error) {
	return producer.tunnel, nil
}

// failingPutIdlePool 用于模拟入池失败。
type failingPutIdlePool struct {
	putErr error
}

// PutIdle 始终返回预置错误。
func (pool *failingPutIdlePool) PutIdle(tunnel Tunnel) error {
	return pool.putErr
}

// Acquire 仅为满足接口。
func (pool *failingPutIdlePool) Acquire(ctx context.Context) (Tunnel, error) {
	return nil, ErrUnsupported
}

// Remove 仅为满足接口。
func (pool *failingPutIdlePool) Remove(tunnelID string) error {
	return ErrUnsupported
}

// IdleCount 返回空池计数。
func (pool *failingPutIdlePool) IdleCount() int {
	return 0
}

// InUseCount 返回空池计数。
func (pool *failingPutIdlePool) InUseCount() int {
	return 0
}

// TestRefillControllerRefillToTarget 验证补池控制器能补到目标容量。
func TestRefillControllerRefillToTarget(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	tunnelProducer := &countingTunnelProducer{}

	refillController, err := NewRefillController(
		tunnelPool,
		tunnelProducer,
		RefillControllerConfig{
			MinIdleTunnels:         0,
			MaxIdleTunnels:         8,
			MaxInFlightTunnelOpens: 2,
			TunnelOpenRateLimit:    rate.Inf,
			TunnelOpenBurst:        8,
			RequestDeduplicateTTL:  time.Minute,
		},
	)
	if err != nil {
		testingObject.Fatalf("new refill controller failed: %v", err)
	}

	result, err := refillController.RefillToTarget(context.Background(), 5)
	if err != nil {
		testingObject.Fatalf("refill to target failed: %v", err)
	}
	if result.OpenedCount != 5 {
		testingObject.Fatalf("expected opened count 5, got %d", result.OpenedCount)
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 5 {
		testingObject.Fatalf("expected idle count 5, got %d", idleCount)
	}
}

// TestRefillControllerHandleRefillRequestDedup 验证 request_id 去重生效。
func TestRefillControllerHandleRefillRequestDedup(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	tunnelProducer := &countingTunnelProducer{}

	refillController, err := NewRefillController(
		tunnelPool,
		tunnelProducer,
		RefillControllerConfig{
			MinIdleTunnels:         0,
			MaxIdleTunnels:         8,
			MaxInFlightTunnelOpens: 2,
			TunnelOpenRateLimit:    rate.Inf,
			TunnelOpenBurst:        8,
			RequestDeduplicateTTL:  time.Minute,
		},
	)
	if err != nil {
		testingObject.Fatalf("new refill controller failed: %v", err)
	}

	request := TunnelRefillRequest{
		SessionID:          "session-1",
		SessionEpoch:       1,
		RequestID:          "request-1",
		RequestedIdleDelta: 3,
		Reason:             TunnelRefillReasonLowWatermark,
		Timestamp:          time.Now().UTC(),
	}

	firstResult, err := refillController.HandleRefillRequest(context.Background(), request)
	if err != nil {
		testingObject.Fatalf("first refill request failed: %v", err)
	}
	if firstResult.Deduplicated {
		testingObject.Fatalf("expected first request not deduplicated")
	}

	secondResult, err := refillController.HandleRefillRequest(context.Background(), request)
	if err != nil {
		testingObject.Fatalf("second refill request failed: %v", err)
	}
	if !secondResult.Deduplicated {
		testingObject.Fatalf("expected second request deduplicated")
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 3 {
		testingObject.Fatalf("expected idle count 3 after dedup, got %d", idleCount)
	}
}

// TestRefillControllerRespectMaxInflight 验证建连并发峰值受上限约束。
func TestRefillControllerRespectMaxInflight(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	tunnelProducer := &countingTunnelProducer{
		openDelay: 20 * time.Millisecond,
	}

	refillController, err := NewRefillController(
		tunnelPool,
		tunnelProducer,
		RefillControllerConfig{
			MinIdleTunnels:         0,
			MaxIdleTunnels:         10,
			MaxInFlightTunnelOpens: 2,
			TunnelOpenRateLimit:    rate.Inf,
			TunnelOpenBurst:        10,
			RequestDeduplicateTTL:  time.Minute,
		},
	)
	if err != nil {
		testingObject.Fatalf("new refill controller failed: %v", err)
	}

	if _, err := refillController.RefillToTarget(context.Background(), 6); err != nil {
		testingObject.Fatalf("refill to target failed: %v", err)
	}
	if maxConcurrent := tunnelProducer.MaxConcurrentOpens(); maxConcurrent > 2 {
		testingObject.Fatalf("expected max concurrent <= 2, got %d", maxConcurrent)
	}
}

// TestRefillControllerClosesTunnelOnPutIdleFailure 验证入池失败时会回收新开的 tunnel。
func TestRefillControllerClosesTunnelOnPutIdleFailure(testingObject *testing.T) {
	tunnel := newCloseTrackingTunnel("close-on-failure")
	refillController, err := NewRefillController(
		&failingPutIdlePool{putErr: ErrPoolExhausted},
		&singleTunnelProducer{tunnel: tunnel},
		RefillControllerConfig{
			MinIdleTunnels:         0,
			MaxIdleTunnels:         1,
			MaxInFlightTunnelOpens: 1,
			TunnelOpenRateLimit:    rate.Inf,
			TunnelOpenBurst:        1,
			RequestDeduplicateTTL:  time.Minute,
		},
	)
	if err != nil {
		testingObject.Fatalf("new refill controller failed: %v", err)
	}

	if _, err := refillController.RefillToTarget(context.Background(), 1); err == nil {
		testingObject.Fatalf("expected refill failure when put idle fails")
	}
	if closeCount := tunnel.CloseCount(); closeCount != 1 {
		testingObject.Fatalf("expected opened tunnel to be closed once, got %d", closeCount)
	}
}

// TestRefillControllerHandleRefillRequestDedupUsesLocalClock 验证 request 去重不受对端时间戳漂移影响。
func TestRefillControllerHandleRefillRequestDedupUsesLocalClock(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	tunnelProducer := &countingTunnelProducer{}

	refillController, err := NewRefillController(
		tunnelPool,
		tunnelProducer,
		RefillControllerConfig{
			MinIdleTunnels:         0,
			MaxIdleTunnels:         8,
			MaxInFlightTunnelOpens: 1,
			TunnelOpenRateLimit:    rate.Inf,
			TunnelOpenBurst:        1,
			RequestDeduplicateTTL:  time.Minute,
		},
	)
	if err != nil {
		testingObject.Fatalf("new refill controller failed: %v", err)
	}

	firstRequest := TunnelRefillRequest{
		SessionID:          "session-1",
		SessionEpoch:       1,
		RequestID:          "request-local-clock",
		RequestedIdleDelta: 1,
		Reason:             TunnelRefillReasonLowWatermark,
		Timestamp:          time.Now().UTC().Add(-2 * time.Minute),
	}
	firstResult, err := refillController.HandleRefillRequest(context.Background(), firstRequest)
	if err != nil {
		testingObject.Fatalf("first refill request failed: %v", err)
	}
	if firstResult.Deduplicated {
		testingObject.Fatalf("expected first request not deduplicated")
	}

	secondRequest := firstRequest
	secondRequest.Timestamp = time.Now().UTC()
	secondResult, err := refillController.HandleRefillRequest(context.Background(), secondRequest)
	if err != nil {
		testingObject.Fatalf("second refill request failed: %v", err)
	}
	if !secondResult.Deduplicated {
		testingObject.Fatalf("expected second request deduplicated even with skewed timestamps")
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 1 {
		testingObject.Fatalf("expected idle count 1 after dedup, got %d", idleCount)
	}
}
