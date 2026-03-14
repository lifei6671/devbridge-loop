package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/routing"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type runtimeDataPlaneReadResult struct {
	payload pb.StreamPayload
	err     error
}

// runtimeDataPlaneTestTunnel 是 app 层数据面测试使用的 tunnel 假实现。
type runtimeDataPlaneTestTunnel struct {
	id string

	readQueue  chan runtimeDataPlaneReadResult
	writeMutex sync.Mutex
	writes     []pb.StreamPayload
	closeCount int
}

// newRuntimeDataPlaneTestTunnel 创建带读写缓冲的测试 tunnel。
func newRuntimeDataPlaneTestTunnel(id string) *runtimeDataPlaneTestTunnel {
	return &runtimeDataPlaneTestTunnel{
		id:        id,
		readQueue: make(chan runtimeDataPlaneReadResult, 8),
	}
}

// ID 返回测试 tunnel 的稳定标识。
func (tunnel *runtimeDataPlaneTestTunnel) ID() string {
	return tunnel.id
}

// ReadPayload 从预置队列读取 payload，模拟数据面输入流。
func (tunnel *runtimeDataPlaneTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	select {
	case <-ctx.Done():
		return pb.StreamPayload{}, ctx.Err()
	case result := <-tunnel.readQueue:
		return result.payload, result.err
	}
}

// WritePayload 记录 Bridge 写入 payload，便于断言 open 请求。
func (tunnel *runtimeDataPlaneTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	tunnel.writes = append(tunnel.writes, payload)
	return nil
}

// Close 记录关闭次数，便于断言 tunnel 回收行为。
func (tunnel *runtimeDataPlaneTestTunnel) Close() error {
	tunnel.closeCount++
	return nil
}

// EnqueueReadPayload 预置下一次读取结果。
func (tunnel *runtimeDataPlaneTestTunnel) EnqueueReadPayload(payload pb.StreamPayload) {
	tunnel.readQueue <- runtimeDataPlaneReadResult{payload: payload}
}

// Writes 返回 Bridge 写入 payload 的副本。
func (tunnel *runtimeDataPlaneTestTunnel) Writes() []pb.StreamPayload {
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	copied := make([]pb.StreamPayload, 0, len(tunnel.writes))
	copied = append(copied, tunnel.writes...)
	return copied
}

// runtimeDataPlaneTestDirectConnection 是 direct path 测试连接句柄。
type runtimeDataPlaneTestDirectConnection struct{}

// Close 模拟上游连接关闭。
func (connection *runtimeDataPlaneTestDirectConnection) Close() error {
	_ = connection
	return nil
}

// runtimeDataPlaneTestDirectDialer 记录 direct path 拨号调用参数。
type runtimeDataPlaneTestDirectDialer struct {
	mutex sync.Mutex
	calls []directproxy.ExternalEndpoint
}

// Dial 记录请求并返回成功连接。
func (dialer *runtimeDataPlaneTestDirectDialer) Dial(
	ctx context.Context,
	endpoint directproxy.ExternalEndpoint,
) (directproxy.UpstreamConn, error) {
	_ = ctx
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	dialer.calls = append(dialer.calls, endpoint)
	return &runtimeDataPlaneTestDirectConnection{}, nil
}

// Calls 返回 direct 拨号调用历史副本。
func (dialer *runtimeDataPlaneTestDirectDialer) Calls() []directproxy.ExternalEndpoint {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	copied := make([]directproxy.ExternalEndpoint, 0, len(dialer.calls))
	copied = append(copied, dialer.calls...)
	return copied
}

// newRuntimeWithDataPlaneDependenciesForTest 构造注入数据面依赖的 runtime。
func newRuntimeWithDataPlaneDependenciesForTest(
	testingObject *testing.T,
	dependencies runtimeDataPlaneDependencies,
) *Runtime {
	testingObject.Helper()
	cfg := DefaultConfig()
	dataPlane, err := newRuntimeDataPlaneWithDependencies(cfg, dependencies)
	if err != nil {
		testingObject.Fatalf("new runtime data plane failed: %v", err)
	}
	controlServer, err := newControlPlaneServer(cfg.ControlPlane, controlPlaneDependencies{
		sessionRegistry: dataPlane.sessionRegistry,
		serviceRegistry: dataPlane.serviceRegistry,
		routeRegistry:   dataPlane.routeRegistry,
		tunnelRegistry:  dataPlane.tunnelRegistry,
	})
	if err != nil {
		testingObject.Fatalf("new control server failed: %v", err)
	}
	return &Runtime{
		cfg:           cfg,
		controlServer: controlServer,
		dataPlane:     dataPlane,
	}
}

// seedConnectorServiceAndSession 写入 connector 解析所需的服务与会话快照。
func seedConnectorServiceAndSession(runtime *Runtime, now time.Time) {
	runtime.dataPlane.sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-1",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now,
	})
	runtime.dataPlane.serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-1",
		ServiceKey:   "dev/demo/order-service",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
}

// TestBootstrapWireDataPlaneDependencies 验证 bootstrap 会把控制面与数据面绑定到同一注册表。
func TestBootstrapWireDataPlaneDependencies(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, err := Bootstrap(context.Background(), DefaultConfig())
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.dataPlane == nil {
		testingObject.Fatalf("expected data plane initialized")
	}
	if runtime.dataPlane.resolver == nil || runtime.dataPlane.pathExecutor == nil {
		testingObject.Fatalf("expected resolver and path executor initialized")
	}
	if runtime.controlServer == nil || runtime.controlServer.dispatcher == nil {
		testingObject.Fatalf("expected control plane dispatcher initialized")
	}
	if runtime.controlServer.dispatcher.sessionRegistry != runtime.dataPlane.sessionRegistry {
		testingObject.Fatalf("expected shared session registry between control plane and data plane")
	}
}

// TestDispatchHTTPIngressConnectorPath 验证 HTTP 入口可走 resolver + connector 主路径。
func TestDispatchHTTPIngressConnectorPath(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, err := Bootstrap(context.Background(), DefaultConfig())
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	// 写入 L7 路由，模拟 API 请求命中 connector_service。
	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.local",
			PathPrefix: "/v1",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	tunnel := newRuntimeDataPlaneTestTunnel("tunnel-1")
	// 第一帧回 open_ack success=true，允许进入 relay。
	tunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
		TrafficID: "traffic-1",
		Success:   true,
	}})
	// 第二帧回 close，驱动 relay 正常结束并回收 tunnel。
	tunnel.EnqueueReadPayload(pb.StreamPayload{Close: &pb.TrafficClose{
		TrafficID: "traffic-1",
	}})
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", tunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.dev.local",
		Path:        "/v1/orders",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID: "traffic-1",
		RouteID:   "route-1",
		ServiceID: "svc-1",
	})
	if err != nil {
		testingObject.Fatalf("dispatch http ingress failed: %v", err)
	}
	if result.Resolution.TargetKind != pb.RouteTargetTypeConnectorService {
		testingObject.Fatalf("unexpected resolved target kind: %s", result.Resolution.TargetKind)
	}
	if result.Execute.TargetKind != pb.RouteTargetTypeConnectorService {
		testingObject.Fatalf("unexpected execute target kind: %s", result.Execute.TargetKind)
	}
	if result.Execute.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected execute http status: %d", result.Execute.HTTPStatus)
	}
	if result.Execute.ConnectorResult == nil {
		testingObject.Fatalf("expected connector execute result")
	}
	if result.Execute.ConnectorResult.TunnelID != "tunnel-1" {
		testingObject.Fatalf("unexpected tunnel id: %s", result.Execute.ConnectorResult.TunnelID)
	}

	writes := tunnel.Writes()
	if len(writes) != 1 {
		testingObject.Fatalf("expected one write payload, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil || writes[0].OpenReq.TrafficID != "traffic-1" {
		testingObject.Fatalf("expected first payload to be open req with traffic-1")
	}
	if tunnel.closeCount != 1 {
		testingObject.Fatalf("expected tunnel close once, got=%d", tunnel.closeCount)
	}

	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 0 {
		testingObject.Fatalf("expected tunnel registry cleaned after close, total=%d", snapshot.TotalCount)
	}
}

// TestDispatchHTTPIngressExternalServicePath 验证 external_service 走 direct path。
func TestDispatchHTTPIngressExternalServicePath(testingObject *testing.T) {
	testingObject.Parallel()

	directDialer := &runtimeDataPlaneTestDirectDialer{}
	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{
		directUpstreamDialer: directDialer,
	})
	now := time.Now().UTC()

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-external-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.direct.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "pay-service",
				Selector: map[string]string{
					"endpoint": "127.0.0.1:19090",
				},
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.direct.local",
		Path:        "/order/create",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID: "traffic-external-1",
	})
	if err != nil {
		testingObject.Fatalf("dispatch external service ingress failed: %v", err)
	}
	if result.Resolution.TargetKind != pb.RouteTargetTypeExternalService {
		testingObject.Fatalf("unexpected resolved target kind: %s", result.Resolution.TargetKind)
	}
	if result.Execute.TargetKind != pb.RouteTargetTypeExternalService {
		testingObject.Fatalf("unexpected execute target kind: %s", result.Execute.TargetKind)
	}
	if result.Execute.DirectResult == nil {
		testingObject.Fatalf("expected direct execute result")
	}
	if result.Execute.DirectResult.Endpoint.Address != "127.0.0.1:19090" {
		testingObject.Fatalf("unexpected direct endpoint: %s", result.Execute.DirectResult.Endpoint.Address)
	}
	if result.Execute.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected execute http status: %d", result.Execute.HTTPStatus)
	}
	calls := directDialer.Calls()
	if len(calls) != 1 {
		testingObject.Fatalf("expected one direct dial call, got=%d", len(calls))
	}
	if calls[0].Address != "127.0.0.1:19090" {
		testingObject.Fatalf("unexpected dial target address: %s", calls[0].Address)
	}
}

// TestDispatchHTTPIngressHybridFallbackPreOpenNoTunnel 验证 hybrid pre-open no-idle 回退到 direct path。
func TestDispatchHTTPIngressHybridFallbackPreOpenNoTunnel(testingObject *testing.T) {
	testingObject.Parallel()

	directDialer := &runtimeDataPlaneTestDirectDialer{}
	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{
		directUpstreamDialer: directDialer,
	})
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-hybrid-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.hybrid.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeHybridGroup,
			HybridGroup: &pb.HybridGroupTarget{
				PrimaryConnectorService: pb.ConnectorServiceTarget{
					ServiceKey: "dev/demo/order-service",
				},
				FallbackExternalService: pb.ExternalServiceTarget{
					Namespace:   "dev",
					Environment: "demo",
					ServiceName: "pay-fallback",
					Selector: map[string]string{
						"endpoint": "127.0.0.1:29090",
					},
				},
				FallbackPolicy: pb.FallbackPolicyPreOpenOnly,
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.hybrid.local",
		Path:        "/fallback",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID: "traffic-hybrid-1",
	})
	if err != nil {
		testingObject.Fatalf("dispatch hybrid ingress failed: %v", err)
	}
	if result.Resolution.TargetKind != pb.RouteTargetTypeHybridGroup {
		testingObject.Fatalf("unexpected resolved target kind: %s", result.Resolution.TargetKind)
	}
	if !result.Execute.UsedHybridFallback {
		testingObject.Fatalf("expected hybrid fallback used")
	}
	if result.Execute.HybridFallbackStage != routing.HybridFallbackStagePreOpenNoTunnel {
		testingObject.Fatalf("unexpected hybrid fallback stage: %s", result.Execute.HybridFallbackStage)
	}
	if result.Execute.DirectResult == nil {
		testingObject.Fatalf("expected direct fallback result")
	}
	if result.Execute.DirectResult.Endpoint.Address != "127.0.0.1:29090" {
		testingObject.Fatalf("unexpected hybrid fallback endpoint: %s", result.Execute.DirectResult.Endpoint.Address)
	}
	if result.Execute.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected execute http status: %d", result.Execute.HTTPStatus)
	}
	calls := directDialer.Calls()
	if len(calls) != 1 {
		testingObject.Fatalf("expected one direct dial call from hybrid fallback, got=%d", len(calls))
	}
	if calls[0].Address != "127.0.0.1:29090" {
		testingObject.Fatalf("unexpected hybrid dial target: %s", calls[0].Address)
	}
}

// TestDispatchHTTPIngressConnectorOpenAckTimeoutLifecycle 验证 app 层链路可复现 timeout/cancel/late-ack 语义。
func TestDispatchHTTPIngressConnectorOpenAckTimeoutLifecycle(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{
		connectorOpenTimeout:         20 * time.Millisecond,
		connectorLateAckDrainTimeout: 80 * time.Millisecond,
		connectorMetrics:             metrics,
	})
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-timeout-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.timeout.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	tunnel := newRuntimeDataPlaneTestTunnel("tunnel-timeout-1")
	// 模拟迟到 open_ack：先触发 timeout，再在 drain 窗口内送达 ack。
	go func() {
		time.Sleep(30 * time.Millisecond)
		tunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
			TrafficID: "traffic-timeout-1",
			Success:   true,
		}})
	}()
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", tunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.timeout.local",
		Path:        "/v1/orders",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID: "traffic-timeout-1",
		RouteID:   "route-timeout-1",
		ServiceID: "svc-1",
	})
	if !errors.Is(err, connectorproxy.ErrOpenAckTimeout) {
		testingObject.Fatalf("expected open_ack timeout error, got=%v", err)
	}
	if result.Execute.HTTPStatus != 504 {
		testingObject.Fatalf("unexpected execute http status: %d", result.Execute.HTTPStatus)
	}
	if result.Execute.ErrorCode != connectorproxy.FailureCodeOpenAckTimeout {
		testingObject.Fatalf("unexpected execute error code: %s", result.Execute.ErrorCode)
	}

	writes := tunnel.Writes()
	if len(writes) < 2 {
		testingObject.Fatalf("expected open+reset writes, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil || writes[0].OpenReq.TrafficID != "traffic-timeout-1" {
		testingObject.Fatalf("expected first write open_req with traffic-timeout-1")
	}
	if writes[1].Reset == nil {
		testingObject.Fatalf("expected second write timeout reset payload")
	}
	if writes[1].Reset.ErrorCode != connectorproxy.OpenTimeoutResetCode {
		testingObject.Fatalf("unexpected timeout reset code: %s", writes[1].Reset.ErrorCode)
	}
	if tunnel.closeCount != 1 {
		testingObject.Fatalf("expected timeout tunnel closed once, got=%d", tunnel.closeCount)
	}

	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 0 {
		testingObject.Fatalf("expected timeout tunnel removed from registry, total=%d", snapshot.TotalCount)
	}
	waitUntilMetric(testingObject, 300*time.Millisecond, func() bool {
		return metrics.BridgeTrafficOpenAckLateTotal() == 1
	})
	if metrics.BridgeTrafficOpenTimeoutTotal() != 1 {
		testingObject.Fatalf("expected open timeout metric increment once, got=%d", metrics.BridgeTrafficOpenTimeoutTotal())
	}
}

func waitUntilMetric(testingObject *testing.T, timeout time.Duration, condition func() bool) {
	testingObject.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	testingObject.Fatalf("condition not satisfied within %s", timeout)
}
