package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type runtimeDataPlaneReadResult struct {
	payload pb.StreamPayload
	err     error
}

// runtimeDataPlaneTestTunnel 是 app 层数据面测试使用的 tunnel 假实现。
type runtimeDataPlaneTestTunnel struct {
	id string

	bindingType transport.BindingType

	readQueue  chan runtimeDataPlaneReadResult
	writeMutex sync.Mutex
	writes     []pb.StreamPayload
	closeCount int
	flushError error
	recyclable bool

	closeAckStateMutex sync.Mutex
	closeAckSentByID   map[string]struct{}

	unsafeRecycleMutex     sync.Mutex
	unsafeRecycleErrorCode string
	unsafeRecycleReason    string
}

// newRuntimeDataPlaneTestTunnel 创建带读写缓冲的测试 tunnel。
func newRuntimeDataPlaneTestTunnel(id string) *runtimeDataPlaneTestTunnel {
	return &runtimeDataPlaneTestTunnel{
		id:               id,
		readQueue:        make(chan runtimeDataPlaneReadResult, 8),
		recyclable:       true,
		closeAckSentByID: make(map[string]struct{}),
	}
}

// ID 返回测试 tunnel 的稳定标识。
func (tunnel *runtimeDataPlaneTestTunnel) ID() string {
	return tunnel.id
}

// BindingType 返回测试 tunnel 的承载类型，默认按 tcp_framed 处理。
func (tunnel *runtimeDataPlaneTestTunnel) BindingType() transport.BindingType {
	if tunnel == nil {
		return ""
	}
	if tunnel.bindingType == "" {
		return transport.BindingTypeTCPFramed
	}
	return tunnel.bindingType
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

// Flush 模拟 recycle 前的本地缓冲刷空检查。
func (tunnel *runtimeDataPlaneTestTunnel) Flush() error {
	return tunnel.flushError
}

// Recyclable 返回测试 tunnel 是否允许继续回收入池。
func (tunnel *runtimeDataPlaneTestTunnel) Recyclable() bool {
	return tunnel.recyclable
}

// SetFlushError 配置 flush 阶段返回的错误。
func (tunnel *runtimeDataPlaneTestTunnel) SetFlushError(err error) {
	tunnel.flushError = err
}

// SetRecyclable 配置当前 tunnel 是否允许 recycle。
func (tunnel *runtimeDataPlaneTestTunnel) SetRecyclable(recyclable bool) {
	tunnel.recyclable = recyclable
}

// TryMarkCloseAckSent 记录某条 traffic 的 close_ack 是否已发送。
func (tunnel *runtimeDataPlaneTestTunnel) TryMarkCloseAckSent(trafficID string) bool {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return false
	}
	tunnel.closeAckStateMutex.Lock()
	defer tunnel.closeAckStateMutex.Unlock()
	if tunnel.closeAckSentByID == nil {
		tunnel.closeAckSentByID = make(map[string]struct{})
	}
	if _, exists := tunnel.closeAckSentByID[normalizedTrafficID]; exists {
		return false
	}
	tunnel.closeAckSentByID[normalizedTrafficID] = struct{}{}
	return true
}

// ClearCloseAckSent 清理某条 traffic 的 close_ack 发送状态。
func (tunnel *runtimeDataPlaneTestTunnel) ClearCloseAckSent(trafficID string) {
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return
	}
	tunnel.closeAckStateMutex.Lock()
	defer tunnel.closeAckStateMutex.Unlock()
	delete(tunnel.closeAckSentByID, normalizedTrafficID)
}

// MarkUnsafeToRecycle 标记 tunnel 本轮 traffic 后不得再进入 recycle，并记录统一错误码。
func (tunnel *runtimeDataPlaneTestTunnel) MarkUnsafeToRecycle(errorCode string, reason string) {
	tunnel.unsafeRecycleMutex.Lock()
	defer tunnel.unsafeRecycleMutex.Unlock()
	tunnel.unsafeRecycleErrorCode = strings.TrimSpace(errorCode)
	tunnel.unsafeRecycleReason = strings.TrimSpace(reason)
}

// ConsumeUnsafeToRecycle 读取并清空 unsafe recycle 标记。
func (tunnel *runtimeDataPlaneTestTunnel) ConsumeUnsafeToRecycle() (string, string, bool) {
	tunnel.unsafeRecycleMutex.Lock()
	defer tunnel.unsafeRecycleMutex.Unlock()
	normalizedErrorCode := strings.TrimSpace(tunnel.unsafeRecycleErrorCode)
	normalizedReason := strings.TrimSpace(tunnel.unsafeRecycleReason)
	if normalizedErrorCode == "" && normalizedReason == "" {
		return "", "", false
	}
	tunnel.unsafeRecycleErrorCode = ""
	tunnel.unsafeRecycleReason = ""
	return normalizedErrorCode, normalizedReason, true
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
	return newRuntimeWithConfigAndDataPlaneDependenciesForTest(testingObject, cfg, dependencies)
}

// newRuntimeWithConfigAndDataPlaneDependenciesForTest 构造带指定配置的数据面测试 runtime。
func newRuntimeWithConfigAndDataPlaneDependenciesForTest(
	testingObject *testing.T,
	cfg Config,
	dependencies runtimeDataPlaneDependencies,
) *Runtime {
	testingObject.Helper()
	dataPlane, err := newRuntimeDataPlaneWithDependencies(cfg, dependencies)
	if err != nil {
		testingObject.Fatalf("new runtime data plane failed: %v", err)
	}
	controlServer, err := newControlPlaneServer(cfg.ControlPlane, controlPlaneDependencies{
		sessionRegistry:      dataPlane.sessionRegistry,
		serviceRegistry:      dataPlane.serviceRegistry,
		routeRegistry:        dataPlane.routeRegistry,
		tunnelRegistry:       dataPlane.tunnelRegistry,
		hostDerivationDomain: cfg.Ingress.BaseDomain,
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

// enableExternalFallbackPolicyForTest 为测试 runtime 开启指定 namespace 的 external fallback。
func enableExternalFallbackPolicyForTest(cfg *Config, namespace string) {
	if cfg == nil {
		return
	}
	cfg.FallbackPolicies = []pb.ScopeFallbackPolicy{{
		PolicyID:  "fallback-" + strings.TrimSpace(namespace),
		Namespace: strings.TrimSpace(namespace),
		Enabled:   true,
		External:  pb.ExternalFallbackConfig{Enabled: true},
	}}
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
	runtime.dataPlane.serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-1",
		ServiceName:      "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Status:               pb.ServiceStatusActive,
		ActiveInstanceCount:  1,
		HealthyInstanceCount: 1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-1",
		LogicalServiceID: "ls-1",
		ConnectorID:      "connector-1",
		SessionID:        "session-1",
		SessionEpoch:     1,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
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
		RouteID: "route-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.local",
			PathPrefix: "/v1",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "order-service",
				},
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
	// 第二帧回 close，驱动 relay 正常结束并返回 close_ack。
	tunnel.EnqueueReadPayload(pb.StreamPayload{Close: &pb.TrafficClose{
		TrafficID: "traffic-1",
	}})
	// 第三帧回 recycle_ack，允许 tunnel 回收入池。
	tunnel.EnqueueReadPayload(pb.StreamPayload{RecycleAck: &pb.TunnelRecycleAck{
		TunnelID:   "tunnel-1",
		RecycleSeq: 1,
		Accepted:   true,
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
		TrafficID:        "traffic-1",
		RouteID:          "route-1",
		LogicalServiceID: "ls-1",
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
	if len(writes) != 3 {
		testingObject.Fatalf("expected open+close_ack+recycle writes, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil || writes[0].OpenReq.TrafficID != "traffic-1" {
		testingObject.Fatalf("expected first payload to be open req with traffic-1")
	}
	if writes[0].OpenReq.Metadata[trafficMetadataLogicalServiceIDKey] != "ls-1" {
		testingObject.Fatalf(
			"unexpected open req logical_service_id metadata: got=%s want=%s",
			writes[0].OpenReq.Metadata[trafficMetadataLogicalServiceIDKey],
			"ls-1",
		)
	}
	if writes[0].OpenReq.Metadata[trafficMetadataServiceNameKey] != "order-service" {
		testingObject.Fatalf(
			"unexpected open req service_name metadata: got=%s want=%s",
			writes[0].OpenReq.Metadata[trafficMetadataServiceNameKey],
			"order-service",
		)
	}
	if writes[0].OpenReq.Metadata[trafficMetadataInstanceIDKey] == "" {
		testingObject.Fatalf("expected open req instance_id metadata not empty")
	}
	if result.Resolution.Connector == nil {
		testingObject.Fatalf("expected connector resolution for service identity assert")
	}
	if writes[0].OpenReq.Metadata[trafficMetadataInstanceIDKey] != result.Resolution.Connector.Instance.InstanceID {
		testingObject.Fatalf(
			"unexpected open req instance_id metadata: got=%s want=%s",
			writes[0].OpenReq.Metadata[trafficMetadataInstanceIDKey],
			result.Resolution.Connector.Instance.InstanceID,
		)
	}
	if writes[1].CloseAck == nil || writes[1].CloseAck.TrafficID != "traffic-1" || !writes[1].CloseAck.Accepted {
		testingObject.Fatalf("expected second payload to be close_ack accepted for traffic-1")
	}
	if writes[2].Recycle == nil || writes[2].Recycle.TunnelID != "tunnel-1" || writes[2].Recycle.RecycleSeq != 1 || writes[2].Recycle.IsFinal {
		testingObject.Fatalf("expected third payload to be non-final recycle seq=1 for tunnel-1")
	}
	if tunnel.closeCount != 0 {
		testingObject.Fatalf("expected tunnel kept alive for reuse, close_count=%d", tunnel.closeCount)
	}

	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 1 || snapshot.IdleCount != 1 {
		testingObject.Fatalf("expected tunnel registry keeps one idle tunnel after recycle, total=%d idle=%d", snapshot.TotalCount, snapshot.IdleCount)
	}
	tunnelRuntime, exists := runtime.dataPlane.tunnelRegistry.Get("tunnel-1")
	if !exists {
		testingObject.Fatalf("expected recycled tunnel still present in registry")
	}
	if tunnelRuntime.State != registry.TunnelStateIdle || tunnelRuntime.ReuseCount != 1 || tunnelRuntime.RecycleSeq != 1 {
		testingObject.Fatalf(
			"expected recycled tunnel state idle/reuse=1/recycle_seq=1, got state=%s reuse=%d recycle_seq=%d",
			tunnelRuntime.State,
			tunnelRuntime.ReuseCount,
			tunnelRuntime.RecycleSeq,
		)
	}
	ownership, ownershipExists := runtime.dataPlane.resolveTrafficOwnership("traffic-1")
	if !ownershipExists {
		testingObject.Fatalf("expected traffic ownership exists for traffic-1")
	}
	if ownership.LogicalServiceID != "ls-1" {
		testingObject.Fatalf("unexpected ownership logical_service_id: got=%s want=ls-1", ownership.LogicalServiceID)
	}
	if result.Resolution.Connector == nil {
		testingObject.Fatalf("expected connector resolution exists for ownership assert")
	}
	if ownership.InstanceID != result.Resolution.Connector.Instance.InstanceID {
		testingObject.Fatalf(
			"unexpected ownership instance_id: got=%s want=%s",
			ownership.InstanceID,
			result.Resolution.Connector.Instance.InstanceID,
		)
	}
	if ownership.Scope.Namespace != "dev" || ownership.Scope.Environment != "demo" {
		testingObject.Fatalf("unexpected ownership scope: %+v", ownership.Scope)
	}
	if ownership.RequestScope.Namespace != "dev" || ownership.RequestScope.Environment != "demo" {
		testingObject.Fatalf("unexpected ownership request_scope: %+v", ownership.RequestScope)
	}
	if ownership.MatchedScope.Namespace != "dev" || ownership.MatchedScope.Environment != "demo" {
		testingObject.Fatalf("unexpected ownership matched_scope: %+v", ownership.MatchedScope)
	}
	if ownership.IsExternalFallback {
		testingObject.Fatalf("expected connector ownership not marked as external fallback")
	}
	if ownership.ConnectorID != "connector-1" || ownership.SessionID != "session-1" {
		testingObject.Fatalf(
			"unexpected ownership runtime fields: connector_id=%s session_id=%s",
			ownership.ConnectorID,
			ownership.SessionID,
		)
	}
	if ownership.RouteID != "route-1" {
		testingObject.Fatalf("unexpected ownership route_id: got=%s want=route-1", ownership.RouteID)
	}
}

// TestDispatchHTTPIngressQUICConnectorPath 验证 app 层 connector 主路径可保留 quic_native binding 语义。
func TestDispatchHTTPIngressQUICConnectorPath(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, err := Bootstrap(context.Background(), DefaultConfig())
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-quic-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.quic.local",
			PathPrefix: "/v1",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "order-service",
				},
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	tunnel := newRuntimeDataPlaneTestTunnel("tunnel-quic-1")
	tunnel.bindingType = transport.BindingTypeQUICNative
	tunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
		TrafficID: "traffic-quic-1",
		Success:   true,
	}})
	tunnel.EnqueueReadPayload(pb.StreamPayload{Close: &pb.TrafficClose{
		TrafficID: "traffic-quic-1",
	}})
	tunnel.EnqueueReadPayload(pb.StreamPayload{RecycleAck: &pb.TunnelRecycleAck{
		TunnelID:   "tunnel-quic-1",
		RecycleSeq: 1,
		Accepted:   true,
	}})
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", tunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.quic.local",
		Path:        "/v1/orders",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID:        "traffic-quic-1",
		RouteID:          "route-quic-1",
		LogicalServiceID: "ls-1",
	})
	if err != nil {
		testingObject.Fatalf("dispatch quic http ingress failed: %v", err)
	}
	if result.Execute.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected execute http status: %d", result.Execute.HTTPStatus)
	}
	if result.Execute.ConnectorResult == nil || result.Execute.ConnectorResult.TunnelID != "tunnel-quic-1" {
		testingObject.Fatalf("expected connector execute result tunnel=tunnel-quic-1")
	}

	writes := tunnel.Writes()
	if len(writes) != 3 {
		testingObject.Fatalf("expected open+close_ack+recycle writes, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil || writes[0].OpenReq.TrafficID != "traffic-quic-1" {
		testingObject.Fatalf("expected first payload to be open req with traffic-quic-1")
	}
	if writes[1].CloseAck == nil || writes[1].CloseAck.TrafficID != "traffic-quic-1" || !writes[1].CloseAck.Accepted {
		testingObject.Fatalf("expected second payload to be close_ack accepted for traffic-quic-1")
	}
	if writes[2].Recycle == nil || writes[2].Recycle.TunnelID != "tunnel-quic-1" || writes[2].Recycle.RecycleSeq != 1 {
		testingObject.Fatalf("expected third payload to be recycle seq=1 for tunnel-quic-1")
	}
	if tunnel.closeCount != 0 {
		testingObject.Fatalf("expected quic tunnel kept alive for reuse, close_count=%d", tunnel.closeCount)
	}

	tunnelRuntime, exists := runtime.dataPlane.tunnelRegistry.Get("tunnel-quic-1")
	if !exists {
		testingObject.Fatalf("expected quic tunnel still present in registry")
	}
	if tunnelRuntime.Binding != transport.BindingTypeQUICNative.String() {
		testingObject.Fatalf(
			"unexpected tunnel binding after quic recycle: got=%s want=%s",
			tunnelRuntime.Binding,
			transport.BindingTypeQUICNative.String(),
		)
	}
	if tunnelRuntime.State != registry.TunnelStateIdle || tunnelRuntime.ReuseCount != 1 || tunnelRuntime.RecycleSeq != 1 {
		testingObject.Fatalf(
			"expected recycled quic tunnel state idle/reuse=1/recycle_seq=1, got state=%s reuse=%d recycle_seq=%d",
			tunnelRuntime.State,
			tunnelRuntime.ReuseCount,
			tunnelRuntime.RecycleSeq,
		)
	}
}

// TestDispatchHTTPIngressExternalServicePath 验证 external_service 走 direct path。
func TestDispatchHTTPIngressExternalServicePath(testingObject *testing.T) {
	testingObject.Parallel()

	directDialer := &runtimeDataPlaneTestDirectDialer{}
	cfg := DefaultConfig()
	enableExternalFallbackPolicyForTest(&cfg, "dev")
	runtime := newRuntimeWithConfigAndDataPlaneDependenciesForTest(testingObject, cfg, runtimeDataPlaneDependencies{
		directUpstreamDialer: directDialer,
	})
	now := time.Now().UTC()

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-external-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
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
	if !result.Resolution.IsExternalFallback {
		testingObject.Fatalf("expected external resolution marked as fallback")
	}
	ownership, exists := runtime.dataPlane.resolveTrafficOwnership("traffic-external-1")
	if !exists {
		testingObject.Fatalf("expected traffic ownership exists for external traffic")
	}
	if !ownership.IsExternalFallback {
		testingObject.Fatalf("expected external ownership marked as fallback")
	}
	if ownership.RequestScope.Namespace != "dev" || ownership.MatchedScope.Environment != "demo" {
		testingObject.Fatalf("unexpected external ownership scopes: request=%+v matched=%+v", ownership.RequestScope, ownership.MatchedScope)
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
		RouteID: "route-timeout-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.timeout.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "order-service",
				},
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
		TrafficID:        "traffic-timeout-1",
		RouteID:          "route-timeout-1",
		LogicalServiceID: "ls-1",
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

// TestDispatchHTTPIngressQUICConnectorOpenAckTimeoutLifecycle 验证 QUIC connector path 复用 timeout/cancel/late-ack 语义。
func TestDispatchHTTPIngressQUICConnectorOpenAckTimeoutLifecycle(testingObject *testing.T) {
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
		RouteID: "route-quic-timeout-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.quic-timeout.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "order-service",
				},
			},
		},
		Metadata: map[string]string{
			"ingress_mode": string(pb.IngressModeL7Shared),
		},
	})

	tunnel := newRuntimeDataPlaneTestTunnel("tunnel-quic-timeout-1")
	tunnel.bindingType = transport.BindingTypeQUICNative
	go func() {
		time.Sleep(30 * time.Millisecond)
		tunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
			TrafficID: "traffic-quic-timeout-1",
			Success:   true,
		}})
	}()
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", tunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	result, err := runtime.DispatchHTTPIngress(context.Background(), ingress.HTTPGatewayRequest{
		Host:        "api.quic-timeout.local",
		Path:        "/v1/orders",
		Namespace:   "dev",
		Environment: "demo",
	}, pb.TrafficOpen{
		TrafficID:        "traffic-quic-timeout-1",
		RouteID:          "route-quic-timeout-1",
		LogicalServiceID: "ls-1",
	})
	if !errors.Is(err, connectorproxy.ErrOpenAckTimeout) {
		testingObject.Fatalf("expected quic open_ack timeout error, got=%v", err)
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
	if writes[0].OpenReq == nil || writes[0].OpenReq.TrafficID != "traffic-quic-timeout-1" {
		testingObject.Fatalf("expected first write open_req with traffic-quic-timeout-1")
	}
	if writes[1].Reset == nil {
		testingObject.Fatalf("expected second write timeout reset payload")
	}
	if writes[1].Reset.ErrorCode != connectorproxy.OpenTimeoutResetCode {
		testingObject.Fatalf("unexpected timeout reset code: %s", writes[1].Reset.ErrorCode)
	}
	if tunnel.closeCount != 1 {
		testingObject.Fatalf("expected quic timeout tunnel closed once, got=%d", tunnel.closeCount)
	}

	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 0 {
		testingObject.Fatalf("expected quic timeout tunnel removed from registry, total=%d", snapshot.TotalCount)
	}
	if _, exists := runtime.dataPlane.tunnelRegistry.Get("tunnel-quic-timeout-1"); exists {
		testingObject.Fatalf("expected quic timeout tunnel purged from registry")
	}
	waitUntilMetric(testingObject, 300*time.Millisecond, func() bool {
		return metrics.BridgeTrafficOpenAckLateTotal() == 1
	})
	if metrics.BridgeTrafficOpenTimeoutTotal() != 1 {
		testingObject.Fatalf("expected quic open timeout metric increment once, got=%d", metrics.BridgeTrafficOpenTimeoutTotal())
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
