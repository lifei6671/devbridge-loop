package transport

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// mockTunnel 是测试用的最小 tunnel 实现。
type mockTunnel struct {
	id          string
	state       TunnelState
	meta        TunnelMeta
	bindingInfo BindingInfo
	doneChannel chan struct{}
	lastError   error
	resetCause  error
	resetError  error
	closeCount  int
	resetCount  int
}

// Read 实现 io.Reader。
func (tunnel *mockTunnel) Read(payload []byte) (int, error) {
	// 测试桩不承载真实数据，直接返回 EOF。
	return 0, io.EOF
}

// Write 实现 io.Writer。
func (tunnel *mockTunnel) Write(payload []byte) (int, error) {
	// 测试桩按全量写入处理，便于通过接口检查。
	return len(payload), nil
}

// Close 实现 io.Closer。
func (tunnel *mockTunnel) Close() error {
	// 关闭时仅记录统一 closed 错误。
	tunnel.closeCount++
	tunnel.lastError = ErrClosed
	return nil
}

// ID 返回 tunnelId。
func (tunnel *mockTunnel) ID() string {
	// 直接返回初始化 ID。
	return tunnel.id
}

// Meta 返回 tunnel 元数据。
func (tunnel *mockTunnel) Meta() TunnelMeta {
	// 返回结构体副本，避免测试误改内部值。
	return tunnel.meta
}

// State 返回 tunnel 状态。
func (tunnel *mockTunnel) State() TunnelState {
	// 用于池校验入池状态是否合法。
	return tunnel.state
}

// BindingInfo 返回 binding 元信息。
func (tunnel *mockTunnel) BindingInfo() BindingInfo {
	// 返回测试预置的 binding 信息。
	return tunnel.bindingInfo
}

// CloseWrite 实现半关闭写侧。
func (tunnel *mockTunnel) CloseWrite() error {
	// 测试桩默认支持并直接返回成功。
	return nil
}

// Reset 实现异常终止。
func (tunnel *mockTunnel) Reset(cause error) error {
	// reset 时保存原因供断言使用。
	tunnel.resetCount++
	tunnel.resetCause = cause
	tunnel.lastError = cause
	if tunnel.resetError != nil {
		return tunnel.resetError
	}
	return nil
}

// SetDeadline 实现 deadline 设置。
func (tunnel *mockTunnel) SetDeadline(deadline time.Time) error {
	// 测试桩不做真实时间控制，直接返回成功。
	return nil
}

// SetReadDeadline 实现读 deadline 设置。
func (tunnel *mockTunnel) SetReadDeadline(deadline time.Time) error {
	// 测试桩不做真实时间控制，直接返回成功。
	return nil
}

// SetWriteDeadline 实现写 deadline 设置。
func (tunnel *mockTunnel) SetWriteDeadline(deadline time.Time) error {
	// 测试桩不做真实时间控制，直接返回成功。
	return nil
}

// Done 返回 tunnel 生命周期结束通知。
func (tunnel *mockTunnel) Done() <-chan struct{} {
	// 返回预置 done 通道用于接口兼容。
	return tunnel.doneChannel
}

// Err 返回最近错误。
func (tunnel *mockTunnel) Err() error {
	// 返回测试期间记录的最近错误。
	return tunnel.lastError
}

// newIdleMockTunnel 创建 idle 状态 tunnel 测试桩。
func newIdleMockTunnel(tunnelID string) *mockTunnel {
	return &mockTunnel{
		id:          tunnelID,
		state:       TunnelStateIdle,
		meta:        TunnelMeta{TunnelID: tunnelID},
		bindingInfo: BindingInfo{Type: BindingTypeGRPCH2},
		doneChannel: make(chan struct{}),
	}
}

type mockProbeTunnel struct {
	*mockTunnel
	probeError error
	probeCount int
}

func (tunnel *mockProbeTunnel) Probe(ctx context.Context) error {
	tunnel.probeCount++
	return tunnel.probeError
}

// TestInMemoryTunnelPoolAcquireAndCount 验证池的入队、获取与计数行为。
func TestInMemoryTunnelPoolAcquireAndCount(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	idleTunnel := newIdleMockTunnel("tunnel-1")

	if err := tunnelPool.PutIdle(idleTunnel); err != nil {
		testingObject.Fatalf("put idle tunnel failed: %v", err)
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 1 {
		testingObject.Fatalf("expected idle count 1, got %d", idleCount)
	}

	acquireContext, cancelAcquire := context.WithTimeout(context.Background(), time.Second)
	defer cancelAcquire()
	acquiredTunnel, err := tunnelPool.Acquire(acquireContext)
	if err != nil {
		testingObject.Fatalf("acquire tunnel failed: %v", err)
	}
	if acquiredTunnel.ID() != idleTunnel.ID() {
		testingObject.Fatalf("expected tunnel id %s, got %s", idleTunnel.ID(), acquiredTunnel.ID())
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 0 {
		testingObject.Fatalf("expected idle count 0, got %d", idleCount)
	}
	if inUseCount := tunnelPool.InUseCount(); inUseCount != 1 {
		testingObject.Fatalf("expected in-use count 1, got %d", inUseCount)
	}
}

// TestInMemoryTunnelPoolAcquireTimeout 验证空池场景下 Acquire 超时返回。
func TestInMemoryTunnelPoolAcquireTimeout(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	acquireContext, cancelAcquire := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelAcquire()
	_, err := tunnelPool.Acquire(acquireContext)
	if err == nil {
		testingObject.Fatalf("expected acquire timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected deadline exceeded, got %v", err)
	}
}

// TestInMemoryTunnelPoolRemoveNotFound 验证删除不存在 tunnel 的错误语义。
func TestInMemoryTunnelPoolRemoveNotFound(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	err := tunnelPool.Remove("unknown-tunnel")
	if err == nil {
		testingObject.Fatalf("expected remove not found error")
	}
	if !errors.Is(err, ErrTunnelNotFound) {
		testingObject.Fatalf("expected ErrTunnelNotFound, got %v", err)
	}
}

// TestTunnelPoolConfigNormalizeAndValidate 验证池配置归一化与参数校验。
func TestTunnelPoolConfigNormalizeAndValidate(testingObject *testing.T) {
	validConfig := TunnelPoolConfig{
		MinIdleTunnels: 1,
		MaxIdleTunnels: 4,
		IdleTunnelTTL:  time.Minute,
		AcquireTimeout: time.Second,
	}
	normalizedConfig, err := validConfig.NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("expected valid config, got err=%v", err)
	}
	if normalizedConfig.MinIdleTunnels != 1 || normalizedConfig.MaxIdleTunnels != 4 {
		testingObject.Fatalf("unexpected normalized config: %+v", normalizedConfig)
	}

	invalidConfig := TunnelPoolConfig{
		MinIdleTunnels: 5,
		MaxIdleTunnels: 1,
	}
	if _, err := invalidConfig.NormalizeAndValidate(); err == nil {
		testingObject.Fatalf("expected error when min_idle_tunnels > max_idle_tunnels")
	}
}

// TestInMemoryTunnelPoolAcquireDefaultTimeout 验证池级 acquire_timeout 默认值生效。
func TestInMemoryTunnelPoolAcquireDefaultTimeout(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 8,
		AcquireTimeout: 15 * time.Millisecond,
	})
	// 调用方未提供 deadline 时，应命中池级默认超时。
	_, err := tunnelPool.Acquire(context.Background())
	if err == nil {
		testingObject.Fatalf("expected acquire timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

// TestInMemoryTunnelPoolEvictExpiredIdle 验证 idle_tunnel_ttl 过期清理行为。
func TestInMemoryTunnelPoolEvictExpiredIdle(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 8,
		IdleTunnelTTL:  20 * time.Millisecond,
	})
	if err := tunnelPool.PutIdle(newIdleMockTunnel("ttl-tunnel")); err != nil {
		testingObject.Fatalf("put idle tunnel failed: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	evictedTunnelIDs := tunnelPool.EvictExpiredIdle(time.Now().UTC())
	if len(evictedTunnelIDs) != 1 {
		testingObject.Fatalf("expected one evicted tunnel, got %d", len(evictedTunnelIDs))
	}
	if evictedTunnelIDs[0] != "ttl-tunnel" {
		testingObject.Fatalf("expected evicted tunnel id ttl-tunnel, got %s", evictedTunnelIDs[0])
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 0 {
		testingObject.Fatalf("expected idle count 0 after eviction, got %d", idleCount)
	}
}

// TestInMemoryTunnelPoolPutIdleOverflowDoesNotPolluteOrder 验证满池拒绝时不会污染 idle 顺序队列。
func TestInMemoryTunnelPoolPutIdleOverflowDoesNotPolluteOrder(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 1,
	})
	if err := tunnelPool.PutIdle(newIdleMockTunnel("tunnel-a")); err != nil {
		testingObject.Fatalf("put first idle tunnel failed: %v", err)
	}

	err := tunnelPool.PutIdle(newIdleMockTunnel("tunnel-b"))
	if err == nil {
		testingObject.Fatalf("expected pool exhausted error")
	}
	if !errors.Is(err, ErrPoolExhausted) {
		testingObject.Fatalf("expected ErrPoolExhausted, got %v", err)
	}

	// 失败入池不应把 tunnel-b 写进顺序队列。
	tunnelPool.mutex.Lock()
	idleOrderLength := len(tunnelPool.idleTunnelOrder)
	firstTunnelID := ""
	if idleOrderLength > 0 {
		firstTunnelID = tunnelPool.idleTunnelOrder[0]
	}
	tunnelPool.mutex.Unlock()
	if idleOrderLength != 1 || firstTunnelID != "tunnel-a" {
		testingObject.Fatalf("unexpected idle order after overflow: len=%d first=%s", idleOrderLength, firstTunnelID)
	}
}

// TestInMemoryTunnelPoolPutIdleSameTunnelWhenFull 验证满池时同 ID 更新不会被误拒绝。
func TestInMemoryTunnelPoolPutIdleSameTunnelWhenFull(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 1,
	})
	if err := tunnelPool.PutIdle(newIdleMockTunnel("tunnel-a")); err != nil {
		testingObject.Fatalf("put first idle tunnel failed: %v", err)
	}

	// 同一 tunnel 重复入池应被视为覆盖更新，而不是新增容量。
	if err := tunnelPool.PutIdle(newIdleMockTunnel("tunnel-a")); err != nil {
		testingObject.Fatalf("expected same tunnel put idle success, got %v", err)
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 1 {
		testingObject.Fatalf("expected idle count 1, got %d", idleCount)
	}
	tunnelPool.mutex.Lock()
	idleOrderLength := len(tunnelPool.idleTunnelOrder)
	tunnelPool.mutex.Unlock()
	if idleOrderLength != 1 {
		testingObject.Fatalf("expected idle order length 1, got %d", idleOrderLength)
	}
}

// TestInMemoryTunnelPoolAcquireWakesMultipleWaiters 验证多等待方不会因丢通知而错过已入池 tunnel。
func TestInMemoryTunnelPoolAcquireWakesMultipleWaiters(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	resultChannel := make(chan error, 2)

	for waiterIndex := 0; waiterIndex < 2; waiterIndex++ {
		go func() {
			acquireContext, cancelAcquire := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancelAcquire()
			_, err := tunnelPool.Acquire(acquireContext)
			resultChannel <- err
		}()
	}

	// 给等待方一点时间进入阻塞态，再连续放入两条 tunnel。
	time.Sleep(20 * time.Millisecond)
	if err := tunnelPool.PutIdle(newIdleMockTunnel("wake-tunnel-1")); err != nil {
		testingObject.Fatalf("put first wake tunnel failed: %v", err)
	}
	if err := tunnelPool.PutIdle(newIdleMockTunnel("wake-tunnel-2")); err != nil {
		testingObject.Fatalf("put second wake tunnel failed: %v", err)
	}

	for waiterIndex := 0; waiterIndex < 2; waiterIndex++ {
		select {
		case err := <-resultChannel:
			if err != nil {
				testingObject.Fatalf("expected acquire success for waiter %d, got %v", waiterIndex, err)
			}
		case <-time.After(300 * time.Millisecond):
			testingObject.Fatalf("timed out waiting for waiter %d to acquire tunnel", waiterIndex)
		}
	}
}

// TestInMemoryTunnelPoolAcquireSkipsBrokenIdle 验证 Acquire 会跳过已 broken 的 idle tunnel。
func TestInMemoryTunnelPoolAcquireSkipsBrokenIdle(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	staleTunnel := newIdleMockTunnel("stale-tunnel")
	healthyTunnel := newIdleMockTunnel("healthy-tunnel")
	if err := tunnelPool.PutIdle(staleTunnel); err != nil {
		testingObject.Fatalf("put stale tunnel failed: %v", err)
	}
	if err := tunnelPool.PutIdle(healthyTunnel); err != nil {
		testingObject.Fatalf("put healthy tunnel failed: %v", err)
	}
	// 模拟入池后底层链路异常。
	staleTunnel.state = TunnelStateBroken

	acquireContext, cancelAcquire := context.WithTimeout(context.Background(), time.Second)
	defer cancelAcquire()
	acquiredTunnel, err := tunnelPool.Acquire(acquireContext)
	if err != nil {
		testingObject.Fatalf("acquire tunnel failed: %v", err)
	}
	if acquiredTunnel.ID() != healthyTunnel.ID() {
		testingObject.Fatalf("expected healthy tunnel %s, got %s", healthyTunnel.ID(), acquiredTunnel.ID())
	}
	if staleTunnel.resetCount != 1 {
		testingObject.Fatalf("expected stale tunnel reset once, got %d", staleTunnel.resetCount)
	}
	if !errors.Is(staleTunnel.resetCause, ErrTunnelBroken) {
		testingObject.Fatalf("expected stale reset cause ErrTunnelBroken, got %v", staleTunnel.resetCause)
	}
}

// TestInMemoryTunnelPoolEvictZombieIdleProbeFailure 验证 probe 失败会标记 broken 并剔除。
func TestInMemoryTunnelPoolEvictZombieIdleProbeFailure(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 8,
		IdleTunnelTTL:  time.Minute,
	})
	probeFailure := errors.New("probe timeout")
	unhealthyTunnel := &mockProbeTunnel{
		mockTunnel: newIdleMockTunnel("probe-bad"),
		probeError: probeFailure,
	}
	healthyTunnel := &mockProbeTunnel{
		mockTunnel: newIdleMockTunnel("probe-good"),
	}
	if err := tunnelPool.PutIdle(unhealthyTunnel); err != nil {
		testingObject.Fatalf("put unhealthy tunnel failed: %v", err)
	}
	if err := tunnelPool.PutIdle(healthyTunnel); err != nil {
		testingObject.Fatalf("put healthy tunnel failed: %v", err)
	}

	evictions := tunnelPool.EvictZombieIdle(context.Background(), time.Now().UTC())
	evictionByID := indexEvictionsByTunnelID(evictions)
	eviction, exists := evictionByID["probe-bad"]
	if !exists {
		testingObject.Fatalf("expected probe-bad to be evicted, got %+v", evictions)
	}
	if eviction.Reason != IdleTunnelEvictionReasonProbeFailed {
		testingObject.Fatalf("expected probe_failed reason, got %s", eviction.Reason)
	}
	if !errors.Is(eviction.Cause, probeFailure) {
		testingObject.Fatalf("expected probe failure cause, got %v", eviction.Cause)
	}
	if unhealthyTunnel.resetCount != 1 {
		testingObject.Fatalf("expected unhealthy tunnel reset once, got %d", unhealthyTunnel.resetCount)
	}
	if !errors.Is(unhealthyTunnel.resetCause, probeFailure) {
		testingObject.Fatalf("expected reset cause probe failure, got %v", unhealthyTunnel.resetCause)
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 1 {
		testingObject.Fatalf("expected idle count 1 after eviction, got %d", idleCount)
	}
}

// TestInMemoryTunnelPoolEvictZombieIdleTTLClose 验证 TTL 剔除走关闭语义而非 broken reset。
func TestInMemoryTunnelPoolEvictZombieIdleTTLClose(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 8,
		IdleTunnelTTL:  50 * time.Millisecond,
	})
	ttlTunnel := newIdleMockTunnel("ttl-zombie")
	if err := tunnelPool.PutIdle(ttlTunnel); err != nil {
		testingObject.Fatalf("put ttl tunnel failed: %v", err)
	}
	tunnelPool.mutex.Lock()
	tunnelPool.idleInsertedAt[ttlTunnel.ID()] = time.Now().UTC().Add(-time.Second)
	tunnelPool.mutex.Unlock()

	evictions := tunnelPool.EvictZombieIdle(context.Background(), time.Now().UTC())
	evictionByID := indexEvictionsByTunnelID(evictions)
	eviction, exists := evictionByID["ttl-zombie"]
	if !exists {
		testingObject.Fatalf("expected ttl-zombie to be evicted, got %+v", evictions)
	}
	if eviction.Reason != IdleTunnelEvictionReasonTTLExpired {
		testingObject.Fatalf("expected ttl_expired reason, got %s", eviction.Reason)
	}
	if ttlTunnel.closeCount != 1 {
		testingObject.Fatalf("expected ttl tunnel close once, got %d", ttlTunnel.closeCount)
	}
	if ttlTunnel.resetCount != 0 {
		testingObject.Fatalf("expected ttl tunnel no reset, got %d", ttlTunnel.resetCount)
	}
}

// TestInMemoryTunnelPoolAcquireFallbackCloseWhenResetUnsupported 验证 reset 不支持时会回退 close 回收资源。
func TestInMemoryTunnelPoolAcquireFallbackCloseWhenResetUnsupported(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPool()
	unsupportedResetTunnel := newIdleMockTunnel("unsupported-reset")
	unsupportedResetTunnel.resetError = ErrUnsupported
	healthyTunnel := newIdleMockTunnel("healthy-tunnel")
	if err := tunnelPool.PutIdle(unsupportedResetTunnel); err != nil {
		testingObject.Fatalf("put unsupported-reset tunnel failed: %v", err)
	}
	if err := tunnelPool.PutIdle(healthyTunnel); err != nil {
		testingObject.Fatalf("put healthy tunnel failed: %v", err)
	}
	unsupportedResetTunnel.state = TunnelStateBroken

	acquireContext, cancelAcquire := context.WithTimeout(context.Background(), time.Second)
	defer cancelAcquire()
	acquiredTunnel, err := tunnelPool.Acquire(acquireContext)
	if err != nil {
		testingObject.Fatalf("acquire tunnel failed: %v", err)
	}
	if acquiredTunnel.ID() != healthyTunnel.ID() {
		testingObject.Fatalf("expected healthy tunnel %s, got %s", healthyTunnel.ID(), acquiredTunnel.ID())
	}
	if unsupportedResetTunnel.resetCount != 1 {
		testingObject.Fatalf("expected unsupported-reset tunnel reset once, got %d", unsupportedResetTunnel.resetCount)
	}
	if unsupportedResetTunnel.closeCount != 1 {
		testingObject.Fatalf("expected unsupported-reset tunnel close once, got %d", unsupportedResetTunnel.closeCount)
	}
}

// TestInMemoryTunnelPoolEvictZombieIdleSkipsProbeContextCancellation 验证 probe 因上下文取消/超时不会触发驱逐。
func TestInMemoryTunnelPoolEvictZombieIdleSkipsProbeContextCancellation(testingObject *testing.T) {
	tunnelPool := NewInMemoryTunnelPoolWithConfig(TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 8,
		IdleTunnelTTL:  time.Minute,
	})
	cancelProbeTunnel := &mockProbeTunnel{
		mockTunnel: newIdleMockTunnel("probe-canceled"),
		probeError: context.Canceled,
	}
	timeoutProbeTunnel := &mockProbeTunnel{
		mockTunnel: newIdleMockTunnel("probe-timeout"),
		probeError: context.DeadlineExceeded,
	}
	if err := tunnelPool.PutIdle(cancelProbeTunnel); err != nil {
		testingObject.Fatalf("put canceled probe tunnel failed: %v", err)
	}
	if err := tunnelPool.PutIdle(timeoutProbeTunnel); err != nil {
		testingObject.Fatalf("put timeout probe tunnel failed: %v", err)
	}

	evictions := tunnelPool.EvictZombieIdle(context.Background(), time.Now().UTC())
	if len(evictions) != 0 {
		testingObject.Fatalf("expected no evictions for probe cancellation/timeout, got %+v", evictions)
	}
	if idleCount := tunnelPool.IdleCount(); idleCount != 2 {
		testingObject.Fatalf("expected idle count 2 after scan, got %d", idleCount)
	}
}

func indexEvictionsByTunnelID(evictions []IdleTunnelEviction) map[string]IdleTunnelEviction {
	index := make(map[string]IdleTunnelEviction, len(evictions))
	for _, eviction := range evictions {
		index[eviction.TunnelID] = eviction
	}
	return index
}
