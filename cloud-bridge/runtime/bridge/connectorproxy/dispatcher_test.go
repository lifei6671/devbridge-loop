package connectorproxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type connectorProxyTestRefillRequester struct {
	mutex sync.Mutex
	calls []connectorProxyRefillCall
}

type connectorProxyRefillCall struct {
	connectorID string
	reason      string
}

func (requester *connectorProxyTestRefillRequester) RequestRefill(connectorID string, reason string) {
	requester.mutex.Lock()
	defer requester.mutex.Unlock()
	requester.calls = append(requester.calls, connectorProxyRefillCall{
		connectorID: connectorID,
		reason:      reason,
	})
}

func (requester *connectorProxyTestRefillRequester) Calls() []connectorProxyRefillCall {
	requester.mutex.Lock()
	defer requester.mutex.Unlock()
	copied := make([]connectorProxyRefillCall, 0, len(requester.calls))
	copied = append(copied, requester.calls...)
	return copied
}

type connectorProxyReadResult struct {
	payload pb.StreamPayload
	err     error
}

type connectorProxyTestTunnel struct {
	tunnelID string

	readQueue  chan connectorProxyReadResult
	writeMutex sync.Mutex
	writes     []pb.StreamPayload
	failReset  bool
	closeCount atomic.Int32
}

func newConnectorProxyTestTunnel(tunnelID string) *connectorProxyTestTunnel {
	return &connectorProxyTestTunnel{
		tunnelID:  tunnelID,
		readQueue: make(chan connectorProxyReadResult, 16),
	}
}

func (tunnel *connectorProxyTestTunnel) ID() string {
	return tunnel.tunnelID
}

func (tunnel *connectorProxyTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	select {
	case <-ctx.Done():
		return pb.StreamPayload{}, ctx.Err()
	case result := <-tunnel.readQueue:
		return result.payload, result.err
	}
}

func (tunnel *connectorProxyTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	if payload.Reset != nil && tunnel.failReset {
		return errors.New("write reset failed")
	}
	tunnel.writes = append(tunnel.writes, payload)
	return nil
}

func (tunnel *connectorProxyTestTunnel) Close() error {
	tunnel.closeCount.Add(1)
	return nil
}

func (tunnel *connectorProxyTestTunnel) EnqueueReadPayload(payload pb.StreamPayload) {
	tunnel.readQueue <- connectorProxyReadResult{payload: payload}
}

func (tunnel *connectorProxyTestTunnel) Writes() []pb.StreamPayload {
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	copied := make([]pb.StreamPayload, 0, len(tunnel.writes))
	copied = append(copied, tunnel.writes...)
	return copied
}

func (tunnel *connectorProxyTestTunnel) CloseCount() int32 {
	return tunnel.closeCount.Load()
}

func (tunnel *connectorProxyTestTunnel) SetFailResetWrite(enabled bool) {
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	tunnel.failReset = enabled
}

// TestTunnelAcquirerNoIdleTimeoutAndRefill 验证 no-idle 会触发补池并在等待窗口超时失败。
func TestTunnelAcquirerNoIdleTimeoutAndRefill(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	refillRequester := &connectorProxyTestRefillRequester{}
	metrics := obs.NewMetrics()
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		Refill:         refillRequester,
		WaitHint:       25 * time.Millisecond,
		PollInterval:   5 * time.Millisecond,
		Metrics:        metrics,
		EnableNoIdleWT: true,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}

	start := time.Now()
	_, err = acquirer.AcquireIdleTunnel(context.Background(), "connector-1")
	if !errors.Is(err, ErrNoIdleTunnel) {
		testingObject.Fatalf("expected no idle tunnel error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		testingObject.Fatalf("expected short wait before timeout, elapsed=%s", elapsed)
	}
	calls := refillRequester.Calls()
	if len(calls) != 1 {
		testingObject.Fatalf("expected one refill request, got=%d", len(calls))
	}
	if calls[0].connectorID != "connector-1" {
		testingObject.Fatalf("unexpected refill connector id: %s", calls[0].connectorID)
	}
	if metrics.BridgeTunnelAcquireWaitCount() != 1 {
		testingObject.Fatalf("expected acquire wait metric sample once, got=%d", metrics.BridgeTunnelAcquireWaitCount())
	}
	if metrics.BridgeTunnelAcquireWaitTotalMs() <= 0 {
		testingObject.Fatalf("expected acquire wait metric > 0, got=%d", metrics.BridgeTunnelAcquireWaitTotalMs())
	}
}

// TestTunnelAcquirerBurstNoIdleFastFail 验证突发 no-idle 请求会快速失败且每次都触发补池信号。
func TestTunnelAcquirerBurstNoIdleFastFail(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	refillRequester := &connectorProxyTestRefillRequester{}
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		Refill:         refillRequester,
		EnableNoIdleWT: false,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}

	const burstRequests = 64
	startedAt := time.Now()
	var waitGroup sync.WaitGroup
	errChannel := make(chan error, burstRequests)
	for index := 0; index < burstRequests; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			// 并发压入 no-idle 场景，验证快速失败路径稳定性。
			_, acquireErr := acquirer.AcquireIdleTunnel(context.Background(), "connector-burst")
			errChannel <- acquireErr
		}()
	}
	waitGroup.Wait()
	close(errChannel)

	noIdleCount := 0
	for acquireErr := range errChannel {
		// 每个请求都应返回 no-idle 语义，不能出现挂起或未知错误。
		if !errors.Is(acquireErr, ErrNoIdleTunnel) {
			testingObject.Fatalf("expected no idle tunnel error, got=%v", acquireErr)
		}
		noIdleCount++
	}
	if noIdleCount != burstRequests {
		testingObject.Fatalf("unexpected no-idle count: got=%d want=%d", noIdleCount, burstRequests)
	}
	if len(refillRequester.Calls()) != burstRequests {
		testingObject.Fatalf("expected refill called per request, got=%d", len(refillRequester.Calls()))
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		testingObject.Fatalf("expected burst fast-fail within 500ms, elapsed=%s", elapsed)
	}
}

// TestTunnelAcquirerWaitHintUsesInjectedClock 验证等待剩余时间使用注入时钟计算，不会被系统时钟偏移立即超时。
func TestTunnelAcquirerWaitHintUsesInjectedClock(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	var tick atomic.Int64
	baseTime := time.Unix(0, 0).UTC()
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		WaitHint:       12 * time.Millisecond,
		PollInterval:   2 * time.Millisecond,
		EnableNoIdleWT: true,
		Now: func() time.Time {
			nowTick := tick.Add(1) - 1
			return baseTime.Add(time.Duration(nowTick) * time.Millisecond)
		},
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}

	start := time.Now()
	_, err = acquirer.AcquireIdleTunnel(context.Background(), "connector-1")
	if !errors.Is(err, ErrNoIdleTunnel) {
		testingObject.Fatalf("expected no idle tunnel error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 6*time.Millisecond {
		testingObject.Fatalf("expected non-immediate timeout with injected clock, elapsed=%s", elapsed)
	}
}

// TestTunnelAcquirerWaitsForIncomingIdle 验证等待窗口内有新 idle tunnel 到达时可成功分配。
func TestTunnelAcquirerWaitsForIncomingIdle(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	refillRequester := &connectorProxyTestRefillRequester{}
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		Refill:         refillRequester,
		WaitHint:       120 * time.Millisecond,
		PollInterval:   5 * time.Millisecond,
		EnableNoIdleWT: true,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = tunnelRegistry.UpsertIdle(time.Now().UTC(), "connector-1", "session-1", newConnectorProxyTestTunnel("tunnel-1"))
	}()

	acquired, err := acquirer.AcquireIdleTunnel(context.Background(), "connector-1")
	if err != nil {
		testingObject.Fatalf("expected acquire succeeds after wait, got: %v", err)
	}
	if acquired.TunnelID != "tunnel-1" {
		testingObject.Fatalf("unexpected tunnel id: %s", acquired.TunnelID)
	}
	if acquired.State != registry.TunnelStateReserved {
		testingObject.Fatalf("unexpected tunnel state: %s", acquired.State)
	}
}

// TestOpenHandshakeRejectError 验证 open_ack reject 会返回可识别错误类型。
func TestOpenHandshakeRejectError(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newConnectorProxyTestTunnel("tunnel-1")
	tunnel.EnqueueReadPayload(pb.StreamPayload{
		OpenAck: &pb.TrafficOpenAck{
			TrafficID:    "traffic-1",
			Success:      false,
			ErrorCode:    "TRAFFIC_OPEN_REJECTED",
			ErrorMessage: "service unavailable",
		},
	})
	handshake := NewOpenHandshake(OpenHandshakeOptions{
		OpenTimeout: time.Second,
	})
	_, err := handshake.Execute(context.Background(), tunnel, pb.TrafficOpen{
		TrafficID: "traffic-1",
		ServiceID: "svc-1",
	})
	var openRejectedError *OpenRejectedError
	if !errors.As(err, &openRejectedError) {
		testingObject.Fatalf("expected OpenRejectedError, got: %v", err)
	}
	if !errors.Is(err, ErrTrafficOpenRejected) {
		testingObject.Fatalf("expected traffic open rejected error")
	}
}

// TestOpenHandshakeTimeoutFallbackClose 验证 open_ack 超时且 reset 不可写时会直接关闭 tunnel。
func TestOpenHandshakeTimeoutFallbackClose(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newConnectorProxyTestTunnel("tunnel-timeout-close")
	tunnel.SetFailResetWrite(true)
	handshake := NewOpenHandshake(OpenHandshakeOptions{
		OpenTimeout:         20 * time.Millisecond,
		LateAckDrainTimeout: 5 * time.Millisecond,
	})

	_, err := handshake.Execute(context.Background(), tunnel, pb.TrafficOpen{
		TrafficID: "traffic-timeout-close",
		ServiceID: "svc-1",
	})
	if !errors.Is(err, ErrOpenAckTimeout) {
		testingObject.Fatalf("expected open ack timeout error, got: %v", err)
	}
	if tunnel.CloseCount() != 1 {
		testingObject.Fatalf("expected tunnel close fallback once, got=%d", tunnel.CloseCount())
	}
}

// TestOpenHandshakeTimeoutReturnsWithoutDrainDelay 验证 open timeout 返回路径不会被 late-ack drain 阻塞。
func TestOpenHandshakeTimeoutReturnsWithoutDrainDelay(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newConnectorProxyTestTunnel("tunnel-timeout-fast-return")
	handshake := NewOpenHandshake(OpenHandshakeOptions{
		OpenTimeout:         20 * time.Millisecond,
		LateAckDrainTimeout: 300 * time.Millisecond,
		CancelHandler: NewCancelHandler(CancelHandlerOptions{
			WriteTimeout: 20 * time.Millisecond,
		}),
	})

	start := time.Now()
	_, err := handshake.Execute(context.Background(), tunnel, pb.TrafficOpen{
		TrafficID: "traffic-timeout-fast-return",
		ServiceID: "svc-1",
	})
	if !errors.Is(err, ErrOpenAckTimeout) {
		testingObject.Fatalf("expected open ack timeout error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 150*time.Millisecond {
		testingObject.Fatalf("expected timeout return without waiting drain timeout, elapsed=%s", elapsed)
	}
}

// TestDispatcherOpenRejectedMetrics 验证 open_ack reject 会记录 reject 指标并返回 503 语义。
func TestDispatcherOpenRejectedMetrics(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnel := newConnectorProxyTestTunnel("tunnel-open-reject")
	tunnel.EnqueueReadPayload(pb.StreamPayload{
		OpenAck: &pb.TrafficOpenAck{
			TrafficID:    "traffic-open-reject",
			Success:      false,
			ErrorCode:    "TRAFFIC_OPEN_REJECTED",
			ErrorMessage: "upstream unavailable",
		},
	})
	_, err := tunnelRegistry.UpsertIdle(time.Now().UTC(), "connector-1", "session-1", tunnel)
	if err != nil {
		testingObject.Fatalf("upsert idle tunnel failed: %v", err)
	}
	metrics := obs.NewMetrics()
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry: tunnelRegistry,
		Metrics:  metrics,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		TunnelAcquirer: acquirer,
		OpenHandshake:  NewOpenHandshake(OpenHandshakeOptions{OpenTimeout: 100 * time.Millisecond}),
		TunnelRegistry: tunnelRegistry,
		Metrics:        metrics,
	})
	if err != nil {
		testingObject.Fatalf("new dispatcher failed: %v", err)
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{
		ConnectorID: "connector-1",
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-open-reject",
			ServiceID: "svc-1",
		},
	})
	if !errors.Is(err, ErrTrafficOpenRejected) {
		testingObject.Fatalf("expected open rejected error, got=%v", err)
	}
	if result.HTTPStatus != 503 {
		testingObject.Fatalf("unexpected status code: %d", result.HTTPStatus)
	}
	if result.ErrorCode != FailureCodeOpenRejected {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
	if metrics.BridgeTrafficOpenRejectTotal() != 1 {
		testingObject.Fatalf("expected open reject metric increment once, got=%d", metrics.BridgeTrafficOpenRejectTotal())
	}
}

// TestDispatcherConnectorDialFailureDoesNotCountAsOpenReject 验证 connector dial 失败不会计入 open reject 指标。
func TestDispatcherConnectorDialFailureDoesNotCountAsOpenReject(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnel := newConnectorProxyTestTunnel("tunnel-dial-failed")
	tunnel.EnqueueReadPayload(pb.StreamPayload{
		OpenAck: &pb.TrafficOpenAck{
			TrafficID:    "traffic-dial-failed",
			Success:      false,
			ErrorCode:    ltfperrors.CodeConnectorDialFailed,
			ErrorMessage: "dial upstream failed",
		},
	})
	_, err := tunnelRegistry.UpsertIdle(time.Now().UTC(), "connector-1", "session-1", tunnel)
	if err != nil {
		testingObject.Fatalf("upsert idle tunnel failed: %v", err)
	}
	metrics := obs.NewMetrics()
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry: tunnelRegistry,
		Metrics:  metrics,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		TunnelAcquirer: acquirer,
		OpenHandshake:  NewOpenHandshake(OpenHandshakeOptions{OpenTimeout: 100 * time.Millisecond}),
		TunnelRegistry: tunnelRegistry,
		Metrics:        metrics,
	})
	if err != nil {
		testingObject.Fatalf("new dispatcher failed: %v", err)
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{
		ConnectorID: "connector-1",
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-dial-failed",
			ServiceID: "svc-1",
		},
	})
	if !errors.Is(err, ErrTrafficOpenRejected) {
		testingObject.Fatalf("expected open rejected error, got=%v", err)
	}
	if result.HTTPStatus != 502 {
		testingObject.Fatalf("unexpected status code: %d", result.HTTPStatus)
	}
	if result.ErrorCode != FailureCodeDialFailed {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
	if metrics.BridgeTrafficOpenRejectTotal() != 0 {
		testingObject.Fatalf("expected open reject metric unchanged, got=%d", metrics.BridgeTrafficOpenRejectTotal())
	}
}

// TestDispatcherDispatchSuccessLifecycle 验证 connector path 成功链路与回收行为。
func TestDispatcherDispatchSuccessLifecycle(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnel := newConnectorProxyTestTunnel("tunnel-1")
	tunnel.EnqueueReadPayload(pb.StreamPayload{
		OpenAck: &pb.TrafficOpenAck{
			TrafficID: "traffic-1",
			Success:   true,
			Metadata: map[string]string{
				"actual_endpoint_id":   "endpoint-2",
				"actual_endpoint_addr": "10.0.0.2:443",
			},
		},
	})
	_, err := tunnelRegistry.UpsertIdle(time.Now().UTC(), "connector-1", "session-1", tunnel)
	if err != nil {
		testingObject.Fatalf("upsert idle tunnel failed: %v", err)
	}
	metrics := obs.NewMetrics()

	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry: tunnelRegistry,
		Metrics:  metrics,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}
	relayStarted := make(chan struct{})
	relayReleased := make(chan struct{})
	dispatcher, err := NewDispatcher(DispatcherOptions{
		TunnelAcquirer: acquirer,
		OpenHandshake:  NewOpenHandshake(OpenHandshakeOptions{OpenTimeout: time.Second}),
		Relay: RelayFunc(func(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) error {
			_ = ctx
			_ = tunnel
			_ = trafficID
			close(relayStarted)
			<-relayReleased
			return nil
		}),
		TunnelRegistry: tunnelRegistry,
		Metrics:        metrics,
	})
	if err != nil {
		testingObject.Fatalf("new dispatcher failed: %v", err)
	}

	resultChannel := make(chan DispatchResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, dispatchErr := dispatcher.Dispatch(context.Background(), DispatchRequest{
			ConnectorID: "connector-1",
			TrafficOpen: pb.TrafficOpen{
				TrafficID: "traffic-1",
				ServiceID: "svc-1",
				EndpointSelectionHint: map[string]string{
					"endpoint_id": "endpoint-1",
				},
			},
		})
		resultChannel <- result
		errorChannel <- dispatchErr
	}()

	<-relayStarted
	runtime, exists := tunnelRegistry.Get("tunnel-1")
	if !exists {
		testingObject.Fatalf("expected tunnel tracked during relay")
	}
	if runtime.State != registry.TunnelStateActive {
		testingObject.Fatalf("expected active state during relay, got=%s", runtime.State)
	}
	close(relayReleased)

	result := <-resultChannel
	if dispatchErr := <-errorChannel; dispatchErr != nil {
		testingObject.Fatalf("dispatch failed: %v", dispatchErr)
	}
	if result.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected status code: %d", result.HTTPStatus)
	}
	if result.OpenAck == nil || !result.OpenAck.Success {
		testingObject.Fatalf("expected successful open ack in result")
	}
	if _, exists := tunnelRegistry.Get("tunnel-1"); exists {
		testingObject.Fatalf("expected tunnel removed after successful close")
	}
	if tunnel.CloseCount() != 1 {
		testingObject.Fatalf("expected tunnel closed once, got=%d", tunnel.CloseCount())
	}
	if metrics.BridgeActualEndpointOverrideTotal() != 1 {
		testingObject.Fatalf("expected actual endpoint override metric increment once, got=%d", metrics.BridgeActualEndpointOverrideTotal())
	}
	writes := tunnel.Writes()
	if len(writes) == 0 || writes[0].OpenReq == nil {
		testingObject.Fatalf("expected first write is traffic open request")
	}
}

// TestDispatcherNoIdleReturns503 验证 no-idle 最终返回 503 语义。
func TestDispatcherNoIdleReturns503(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	refillRequester := &connectorProxyTestRefillRequester{}
	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		Refill:         refillRequester,
		WaitHint:       20 * time.Millisecond,
		PollInterval:   5 * time.Millisecond,
		EnableNoIdleWT: true,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		TunnelAcquirer: acquirer,
		OpenHandshake:  NewOpenHandshake(OpenHandshakeOptions{OpenTimeout: 50 * time.Millisecond}),
		TunnelRegistry: tunnelRegistry,
	})
	if err != nil {
		testingObject.Fatalf("new dispatcher failed: %v", err)
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{
		ConnectorID: "connector-1",
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-no-idle",
			ServiceID: "svc-1",
		},
	})
	if !errors.Is(err, ErrNoIdleTunnel) {
		testingObject.Fatalf("expected no idle tunnel error, got: %v", err)
	}
	if result.HTTPStatus != 503 {
		testingObject.Fatalf("unexpected status code: %d", result.HTTPStatus)
	}
	if result.ErrorCode != FailureCodeNoIdleTunnel {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
	if len(refillRequester.Calls()) != 1 {
		testingObject.Fatalf("expected refill requested once")
	}
}

// TestDispatcherOpenAckTimeoutDropsLateAck 验证超时取消、late-ack 丢弃计数、以及终态回收。
func TestDispatcherOpenAckTimeoutDropsLateAck(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnel := newConnectorProxyTestTunnel("tunnel-timeout-late-ack")
	metrics := obs.NewMetrics()
	_, err := tunnelRegistry.UpsertIdle(time.Now().UTC(), "connector-1", "session-1", tunnel)
	if err != nil {
		testingObject.Fatalf("upsert idle tunnel failed: %v", err)
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		tunnel.EnqueueReadPayload(pb.StreamPayload{
			OpenAck: &pb.TrafficOpenAck{
				TrafficID: "traffic-timeout-late-ack",
				Success:   true,
			},
		})
	}()

	acquirer, err := NewTunnelAcquirer(TunnelAcquirerOptions{
		Registry: tunnelRegistry,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel acquirer failed: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		TunnelAcquirer: acquirer,
		OpenHandshake: NewOpenHandshake(OpenHandshakeOptions{
			OpenTimeout:         20 * time.Millisecond,
			LateAckDrainTimeout: 80 * time.Millisecond,
			Metrics:             metrics,
		}),
		TunnelRegistry: tunnelRegistry,
		Metrics:        metrics,
	})
	if err != nil {
		testingObject.Fatalf("new dispatcher failed: %v", err)
	}

	result, err := dispatcher.Dispatch(context.Background(), DispatchRequest{
		ConnectorID: "connector-1",
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-timeout-late-ack",
			ServiceID: "svc-1",
		},
	})
	if !errors.Is(err, ErrOpenAckTimeout) {
		testingObject.Fatalf("expected open ack timeout, got: %v", err)
	}
	if result.HTTPStatus != 504 {
		testingObject.Fatalf("unexpected status code: %d", result.HTTPStatus)
	}
	if result.ErrorCode != FailureCodeOpenAckTimeout {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
	if _, exists := tunnelRegistry.Get("tunnel-timeout-late-ack"); exists {
		testingObject.Fatalf("expected timeout tunnel removed from registry")
	}
	writes := tunnel.Writes()
	if len(writes) < 2 {
		testingObject.Fatalf("expected open+reset writes, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil {
		testingObject.Fatalf("expected first write to be open request")
	}
	if writes[1].Reset == nil {
		testingObject.Fatalf("expected second write to be timeout reset")
	}
	if writes[1].Reset.TrafficID != "traffic-timeout-late-ack" {
		testingObject.Fatalf("unexpected reset traffic_id: %s", writes[1].Reset.TrafficID)
	}
	if writes[1].Reset.ErrorCode != OpenTimeoutResetCode {
		testingObject.Fatalf("unexpected reset error code: %s", writes[1].Reset.ErrorCode)
	}
	if metrics.BridgeTrafficOpenTimeoutTotal() != 1 {
		testingObject.Fatalf("expected open timeout metric increment once, got=%d", metrics.BridgeTrafficOpenTimeoutTotal())
	}
	waitUntil(testingObject, 300*time.Millisecond, func() bool {
		return metrics.BridgeTrafficOpenAckLateTotal() == 1
	})
	if metrics.BridgeTrafficOpenAckLateTotal() != 1 {
		testingObject.Fatalf("expected late ack metric increment once, got=%d", metrics.BridgeTrafficOpenAckLateTotal())
	}
}

func waitUntil(testingObject *testing.T, timeout time.Duration, condition func() bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	testingObject.Fatalf("condition not satisfied within %s", timeout)
}
