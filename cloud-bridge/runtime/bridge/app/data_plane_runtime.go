package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/routing"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrRuntimeDataPlaneDependencyMissing 表示 runtime 数据面依赖未就绪。
	ErrRuntimeDataPlaneDependencyMissing = errors.New("runtime data plane dependency missing")
)

const (
	// selectorEndpointKey 定义单 endpoint 的 selector 键。
	selectorEndpointKey = "endpoint"
	// selectorAddressKey 定义 endpoint 的别名 selector 键。
	selectorAddressKey = "address"
	// selectorEndpointsKey 定义多 endpoint 列表 selector 键（逗号分隔）。
	selectorEndpointsKey = "endpoints"

	// defaultBridgeAcquireWaitHint 定义 no-idle 时的短等待窗口。
	defaultBridgeAcquireWaitHint = 150 * time.Millisecond
	// defaultBridgeAcquirePollInterval 定义 acquire 轮询间隔。
	defaultBridgeAcquirePollInterval = 10 * time.Millisecond
	// defaultBridgeTrafficOpenTimeout 定义 pre-open 握手超时。
	defaultBridgeTrafficOpenTimeout = 3 * time.Second
	// defaultBridgeLateAckDrainTimeout 定义迟到 ack 丢弃窗口。
	defaultBridgeLateAckDrainTimeout = 250 * time.Millisecond
	// defaultBridgeDirectDialTimeout 定义 direct path 拨号超时。
	defaultBridgeDirectDialTimeout = 3 * time.Second
	// trafficMetadataServiceIDKey 定义 traffic 元数据中的 service_id 键。
	trafficMetadataServiceIDKey = "service_id"
	// trafficMetadataServiceKeyKey 定义 traffic 元数据中的 service_key 键。
	trafficMetadataServiceKeyKey = "service_key"
	// trafficMetadataServiceInstanceIDKey 定义 traffic 元数据中的 service_instance_id 键。
	trafficMetadataServiceInstanceIDKey = "service_instance_id"
)

// runtimeDataPlaneDependencies 描述 app 层数据面可注入依赖。
type runtimeDataPlaneDependencies struct {
	directDiscoveryProvider directproxy.DiscoveryProvider
	directUpstreamDialer    directproxy.UpstreamDialer
	directRelay             directproxy.RelayPump

	// connectorOpenTimeout 覆盖 connector pre-open 等待 open_ack 的超时阈值。
	connectorOpenTimeout time.Duration
	// connectorLateAckDrainTimeout 覆盖迟到 open_ack 丢弃窗口。
	connectorLateAckDrainTimeout time.Duration
	// connectorCancelHandler 允许注入超时取消处理策略（含 reset 写入行为）。
	connectorCancelHandler *connectorproxy.CancelHandler
	// connectorMetrics 允许 app 层测试读取 timeout/late-ack 指标。
	connectorMetrics *obs.Metrics
}

// DispatchRouteLookupRequest 描述 runtime 统一路由分发输入。
type DispatchRouteLookupRequest struct {
	LookupRequest ingress.RouteLookupRequest
	TrafficOpen   pb.TrafficOpen
}

// DispatchRouteLookupResult 描述 runtime 统一路由分发输出。
type DispatchRouteLookupResult struct {
	Resolution routing.ResolveResult
	Execute    routing.PathExecuteResult
}

// runtimeDataPlane 聚合 Bridge 数据面主路径依赖。
type runtimeDataPlane struct {
	sessionRegistry  *registry.SessionRegistry
	serviceRegistry  *registry.ServiceRegistry
	routeRegistry    *registry.RouteRegistry
	tunnelRegistry   *registry.TunnelRegistry
	connectorMetrics *obs.Metrics
	trafficOwnership *trafficOwnershipStore

	resolver     *routing.Resolver
	pathExecutor *routing.PathExecutor

	httpGateway   *ingress.HTTPGateway
	grpcGateway   *ingress.GRPCGateway
	tlsSNIGateway *ingress.TLSSNIGateway
	tcpGateway    *ingress.TCPPortGateway
}

// newRuntimeDataPlane 构建 runtime 所需的 routing + connector/direct/hybrid 主路径组件。
func newRuntimeDataPlane(cfg Config) (*runtimeDataPlane, error) {
	return newRuntimeDataPlaneWithDependencies(cfg, runtimeDataPlaneDependencies{})
}

// newRuntimeDataPlaneWithDependencies 构建支持依赖注入的数据面组件图。
func newRuntimeDataPlaneWithDependencies(
	cfg Config,
	dependencies runtimeDataPlaneDependencies,
) (*runtimeDataPlane, error) {
	httpListenPort, err := resolveListenPort(cfg.Ingress.HTTPAddr)
	if err != nil {
		return nil, fmt.Errorf("new runtime data plane: invalid http addr: %w", err)
	}
	grpcListenPort, err := resolveListenPort(cfg.Ingress.GRPCAddr)
	if err != nil {
		return nil, fmt.Errorf("new runtime data plane: invalid grpc addr: %w", err)
	}
	tlsSNIPort, err := resolveListenPort(cfg.Ingress.TLSSNIAddr)
	if err != nil {
		return nil, fmt.Errorf("new runtime data plane: invalid tls sni addr: %w", err)
	}

	// 四类注册表在 runtime 内共享，确保控制面更新与数据面解析看到同一真相源。
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	routeRegistry := registry.NewRouteRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	connectorMetrics := dependencies.connectorMetrics
	if connectorMetrics == nil {
		// 未显式注入指标容器时使用默认容器，确保路由与执行链路指标统一落桶。
		connectorMetrics = obs.DefaultMetrics
	}
	dependencies.connectorMetrics = connectorMetrics

	resolver := routing.NewResolver(routing.ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		Metrics:         connectorMetrics,
	})
	pathExecutor, err := newRuntimePathExecutor(cfg, tunnelRegistry, dependencies)
	if err != nil {
		return nil, fmt.Errorf("new runtime data plane: build path executor: %w", err)
	}

	return &runtimeDataPlane{
		sessionRegistry:  sessionRegistry,
		serviceRegistry:  serviceRegistry,
		routeRegistry:    routeRegistry,
		tunnelRegistry:   tunnelRegistry,
		connectorMetrics: connectorMetrics,
		trafficOwnership: newTrafficOwnershipStore(
			defaultTrafficOwnershipTTL,
			defaultTrafficOwnershipCapacity,
			nil,
		),
		resolver:      resolver,
		pathExecutor:  pathExecutor,
		httpGateway:   ingress.NewHTTPGateway(httpListenPort),
		grpcGateway:   ingress.NewGRPCGateway(grpcListenPort),
		tlsSNIGateway: ingress.NewTLSSNIGateway(tlsSNIPort),
		tcpGateway:    ingress.NewTCPPortGateway(),
	}, nil
}

// newRuntimePathExecutor 组装 connector/direct/hybrid 三路径执行器。
func newRuntimePathExecutor(
	cfg Config,
	tunnelRegistry *registry.TunnelRegistry,
	dependencies runtimeDataPlaneDependencies,
) (*routing.PathExecutor, error) {
	tunnelAcquirer, err := connectorproxy.NewTunnelAcquirer(connectorproxy.TunnelAcquirerOptions{
		Registry:       tunnelRegistry,
		WaitHint:       defaultBridgeAcquireWaitHint,
		PollInterval:   defaultBridgeAcquirePollInterval,
		EnableNoIdleWT: true,
	})
	if err != nil {
		return nil, fmt.Errorf("new runtime path executor: new tunnel acquirer: %w", err)
	}
	openTimeout := defaultBridgeTrafficOpenTimeout
	if dependencies.connectorOpenTimeout > 0 {
		openTimeout = dependencies.connectorOpenTimeout
	}
	lateAckDrainTimeout := defaultBridgeLateAckDrainTimeout
	if dependencies.connectorLateAckDrainTimeout > 0 {
		lateAckDrainTimeout = dependencies.connectorLateAckDrainTimeout
	}
	openHandshake := connectorproxy.NewOpenHandshake(connectorproxy.OpenHandshakeOptions{
		OpenTimeout:         openTimeout,
		LateAckDrainTimeout: lateAckDrainTimeout,
		CancelHandler:       dependencies.connectorCancelHandler,
		Metrics:             dependencies.connectorMetrics,
	})
	connectorDispatcher, err := connectorproxy.NewDispatcher(connectorproxy.DispatcherOptions{
		TunnelAcquirer: tunnelAcquirer,
		OpenHandshake:  openHandshake,
		TunnelRegistry: tunnelRegistry,
		Metrics:        dependencies.connectorMetrics,
		MaxReuseCount:  cfg.TunnelReuse.MaxReuseCount,
		RecycleTimeout: cfg.TunnelReuse.RecycleTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("new runtime path executor: new connector dispatcher: %w", err)
	}
	directExecutor, err := newRuntimeDirectExecutor(dependencies)
	if err != nil {
		return nil, fmt.Errorf("new runtime path executor: new direct executor: %w", err)
	}
	pathExecutor, err := routing.NewPathExecutor(routing.PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		return nil, fmt.Errorf("new runtime path executor: new path executor: %w", err)
	}
	return pathExecutor, nil
}

// newRuntimeDirectExecutor 构建 direct path 运行时执行器。
func newRuntimeDirectExecutor(dependencies runtimeDataPlaneDependencies) (*directproxy.Executor, error) {
	discoveryProvider := dependencies.directDiscoveryProvider
	if discoveryProvider == nil {
		// 默认使用 selector 静态 endpoint 作为最小 discovery provider。
		discoveryProvider = &runtimeSelectorDiscoveryProvider{}
	}
	discoveryAdapter, err := directproxy.NewDiscoveryAdapter(directproxy.DiscoveryAdapterOptions{
		Provider: discoveryProvider,
	})
	if err != nil {
		return nil, fmt.Errorf("new runtime direct executor: new discovery adapter: %w", err)
	}
	upstreamDialer := dependencies.directUpstreamDialer
	if upstreamDialer == nil {
		// 默认使用 TCP 拨号器，保证 external_service 主路径可直接执行。
		upstreamDialer = &runtimeNetUpstreamDialer{dialTimeout: defaultBridgeDirectDialTimeout}
	}
	dialer, err := directproxy.NewDialer(directproxy.DialerOptions{Upstream: upstreamDialer})
	if err != nil {
		return nil, fmt.Errorf("new runtime direct executor: new dialer: %w", err)
	}
	relay := dependencies.directRelay
	if relay == nil {
		relay = directproxy.NewRelay()
	}
	executor, err := directproxy.NewExecutor(directproxy.ExecutorOptions{
		Discovery: discoveryAdapter,
		Dialer:    dialer,
		Relay:     relay,
	})
	if err != nil {
		return nil, fmt.Errorf("new runtime direct executor: new executor: %w", err)
	}
	return executor, nil
}

// runtimeSelectorDiscoveryProvider 从 target.selector 解析静态 endpoint 列表。
type runtimeSelectorDiscoveryProvider struct{}

// Discover 返回 external_service 的静态 endpoint 发现结果。
func (provider *runtimeSelectorDiscoveryProvider) Discover(
	ctx context.Context,
	request directproxy.DiscoveryRequest,
) ([]directproxy.ExternalEndpoint, error) {
	_ = provider
	_ = ctx
	endpoints := make([]directproxy.ExternalEndpoint, 0)
	for index, endpointAddress := range parseSelectorEndpointAddresses(request.Selector) {
		if strings.TrimSpace(endpointAddress) == "" {
			continue
		}
		endpoints = append(endpoints, directproxy.ExternalEndpoint{
			EndpointID: fmt.Sprintf("selector-%d", index+1),
			Address:    strings.TrimSpace(endpointAddress),
		})
	}
	if len(endpoints) == 0 {
		return nil, ltfperrors.New(
			ltfperrors.CodeDiscoveryProviderUnavailable,
			"external_service.selector.endpoint(s) is required for runtime static discovery",
		)
	}
	return endpoints, nil
}

// parseSelectorEndpointAddresses 解析 selector 中的 endpoint 列表配置。
func parseSelectorEndpointAddresses(selector map[string]string) []string {
	if len(selector) == 0 {
		return nil
	}
	resolved := make([]string, 0, 4)
	appendIfPresent := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			resolved = append(resolved, trimmed)
		}
	}
	appendIfPresent(selector[selectorEndpointKey])
	appendIfPresent(selector[selectorAddressKey])
	for _, raw := range strings.Split(selector[selectorEndpointsKey], ",") {
		appendIfPresent(raw)
	}
	return resolved
}

// runtimeNetUpstreamDialer 是 runtime 默认 direct path TCP 拨号器。
type runtimeNetUpstreamDialer struct {
	dialTimeout time.Duration
}

// Dial 建立到 external endpoint 的 TCP 连接。
func (dialer *runtimeNetUpstreamDialer) Dial(
	ctx context.Context,
	endpoint directproxy.ExternalEndpoint,
) (directproxy.UpstreamConn, error) {
	if dialer == nil {
		return nil, directproxy.ErrDialerDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	if dialer.dialTimeout > 0 {
		if _, hasDeadline := normalizedContext.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			normalizedContext, cancel = context.WithTimeout(normalizedContext, dialer.dialTimeout)
			defer cancel()
		}
	}
	normalizedAddress := strings.TrimSpace(endpoint.Address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("runtime direct dial: empty endpoint address")
	}
	connection, err := (&net.Dialer{}).DialContext(normalizedContext, "tcp", normalizedAddress)
	if err != nil {
		return nil, fmt.Errorf("runtime direct dial: endpoint=%s: %w", normalizedAddress, err)
	}
	return &runtimeNetUpstreamConnection{connection: connection}, nil
}

// runtimeNetUpstreamConnection 适配 net.Conn 到 directproxy.UpstreamConn。
type runtimeNetUpstreamConnection struct {
	connection net.Conn
}

// Close 关闭底层 TCP 连接。
func (connection *runtimeNetUpstreamConnection) Close() error {
	if connection == nil || connection.connection == nil {
		return nil
	}
	return connection.connection.Close()
}

// resolveListenPort 解析监听地址中的端口号。
func resolveListenPort(listenAddr string) (uint32, error) {
	normalizedAddr := strings.TrimSpace(listenAddr)
	if normalizedAddr == "" {
		// 允许未配置地址，返回 0 让后续匹配走“无端口约束”语义。
		return 0, nil
	}
	_, portText, err := net.SplitHostPort(normalizedAddr)
	if err != nil {
		return 0, fmt.Errorf("parse listen addr %q failed: %w", normalizedAddr, err)
	}
	parsedPort, err := strconv.ParseUint(strings.TrimSpace(portText), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("parse listen port %q failed: %w", portText, err)
	}
	return uint32(parsedPort), nil
}

// DispatchRouteLookup 执行 ingress lookup -> route resolve -> path execute 主链路。
func (runtime *Runtime) DispatchRouteLookup(
	ctx context.Context,
	request DispatchRouteLookupRequest,
) (DispatchRouteLookupResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.resolver == nil || runtime.dataPlane.pathExecutor == nil {
		return DispatchRouteLookupResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// 兜底背景上下文，避免调用方必须显式构造 context。
		normalizedContext = context.Background()
	}
	lookupRequest := request.LookupRequest
	lookupRequest.Metadata = copyStringMap(lookupRequest.Metadata)
	normalizedTrafficID := strings.TrimSpace(request.TrafficOpen.TrafficID)
	if normalizedTrafficID != "" {
		if lookupRequest.Metadata == nil {
			lookupRequest.Metadata = make(map[string]string, 1)
		}
		// 透传 traffic_id 到 resolver，支撑单条 traffic 实例粘性。
		lookupRequest.Metadata[routing.RouteLookupMetadataTrafficIDKey] = normalizedTrafficID
	}

	// 第一步：按 ingress 请求解析 route 与 target。
	resolution, err := runtime.dataPlane.resolver.Resolve(lookupRequest)
	if err != nil {
		return DispatchRouteLookupResult{}, fmt.Errorf("dispatch route lookup: resolve route: %w", err)
	}
	// 统一从解析结果提取服务身份，避免后续日志/审计字段口径不一致。
	resolvedServiceID, resolvedServiceKey, resolvedServiceInstanceID := resolveTrafficServiceIdentityFromResolution(resolution)

	trafficOpen := pb.TrafficOpen{
		TrafficID:             normalizedTrafficID,
		RouteID:               strings.TrimSpace(request.TrafficOpen.RouteID),
		ServiceID:             strings.TrimSpace(request.TrafficOpen.ServiceID),
		SourceAddr:            strings.TrimSpace(request.TrafficOpen.SourceAddr),
		ProtocolHint:          strings.TrimSpace(request.TrafficOpen.ProtocolHint),
		TraceID:               strings.TrimSpace(request.TrafficOpen.TraceID),
		EndpointSelectionHint: copyStringMap(request.TrafficOpen.EndpointSelectionHint),
		Metadata:              copyStringMap(request.TrafficOpen.Metadata),
	}
	if trafficOpen.RouteID == "" {
		// 调用方未显式透传 route_id 时，使用 resolver 命中的 route 作为权威值。
		trafficOpen.RouteID = strings.TrimSpace(resolution.Route.RouteID)
	}
	if trafficOpen.ServiceID == "" {
		// 调用方未显式透传 service_id 时，按解析结果补齐 connector 服务标识。
		trafficOpen.ServiceID = resolvedServiceID
	}
	if trafficOpen.ProtocolHint == "" {
		// protocolHint 缺失时回填 ingress 归一化后的协议，便于 Agent 侧选择本地处理策略。
		trafficOpen.ProtocolHint = strings.TrimSpace(request.LookupRequest.Protocol)
	}
	// 默认写入统一服务身份元数据，便于运行时日志与审计链路直接复用。
	enrichTrafficOpenServiceIdentity(
		&trafficOpen,
		resolvedServiceID,
		resolvedServiceKey,
		resolvedServiceInstanceID,
	)
	// 路由命中后写入 traffic 归属索引，支撑管理面按 traffic_id 反查服务归属。
	runtime.observeTrafficOwnership(resolution, trafficOpen)

	// 第二步：执行 connector/direct/hybrid 路径。
	executeResult, executeErr := runtime.dataPlane.pathExecutor.Execute(normalizedContext, routing.PathExecuteRequest{
		Resolution:  resolution,
		TrafficOpen: trafficOpen,
	})
	dispatchResult := DispatchRouteLookupResult{
		Resolution: resolution,
		Execute:    executeResult,
	}
	if executeErr != nil {
		return dispatchResult, fmt.Errorf("dispatch route lookup: execute path: %w", executeErr)
	}
	return dispatchResult, nil
}

// DispatchHTTPIngress 执行 HTTP 入口到路径执行的完整分发。
func (runtime *Runtime) DispatchHTTPIngress(
	ctx context.Context,
	request ingress.HTTPGatewayRequest,
	trafficOpen pb.TrafficOpen,
) (DispatchRouteLookupResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.httpGateway == nil {
		return DispatchRouteLookupResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	lookupRequest, err := runtime.dataPlane.httpGateway.BuildRouteLookupRequest(request)
	if err != nil {
		return DispatchRouteLookupResult{}, fmt.Errorf("dispatch http ingress: %w", err)
	}
	return runtime.DispatchRouteLookup(ctx, DispatchRouteLookupRequest{
		LookupRequest: lookupRequest,
		TrafficOpen:   trafficOpen,
	})
}

// DispatchGRPCIngress 执行 gRPC 入口到路径执行的完整分发。
func (runtime *Runtime) DispatchGRPCIngress(
	ctx context.Context,
	request ingress.GRPCGatewayRequest,
	trafficOpen pb.TrafficOpen,
) (DispatchRouteLookupResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.grpcGateway == nil {
		return DispatchRouteLookupResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	lookupRequest, err := runtime.dataPlane.grpcGateway.BuildRouteLookupRequest(request)
	if err != nil {
		return DispatchRouteLookupResult{}, fmt.Errorf("dispatch grpc ingress: %w", err)
	}
	return runtime.DispatchRouteLookup(ctx, DispatchRouteLookupRequest{
		LookupRequest: lookupRequest,
		TrafficOpen:   trafficOpen,
	})
}

// DispatchTLSSNIIngress 执行 TLS-SNI 入口到路径执行的完整分发。
func (runtime *Runtime) DispatchTLSSNIIngress(
	ctx context.Context,
	request ingress.TLSSNIGatewayRequest,
	trafficOpen pb.TrafficOpen,
) (DispatchRouteLookupResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tlsSNIGateway == nil {
		return DispatchRouteLookupResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	lookupRequest, err := runtime.dataPlane.tlsSNIGateway.BuildRouteLookupRequest(request)
	if err != nil {
		return DispatchRouteLookupResult{}, fmt.Errorf("dispatch tls-sni ingress: %w", err)
	}
	return runtime.DispatchRouteLookup(ctx, DispatchRouteLookupRequest{
		LookupRequest: lookupRequest,
		TrafficOpen:   trafficOpen,
	})
}

// DispatchTCPPortIngress 执行 dedicated TCP 入口到路径执行的完整分发。
func (runtime *Runtime) DispatchTCPPortIngress(
	ctx context.Context,
	request ingress.TCPPortGatewayRequest,
	trafficOpen pb.TrafficOpen,
) (DispatchRouteLookupResult, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tcpGateway == nil {
		return DispatchRouteLookupResult{}, ErrRuntimeDataPlaneDependencyMissing
	}
	lookupRequest, err := runtime.dataPlane.tcpGateway.BuildRouteLookupRequest(request)
	if err != nil {
		return DispatchRouteLookupResult{}, fmt.Errorf("dispatch tcp ingress: %w", err)
	}
	return runtime.DispatchRouteLookup(ctx, DispatchRouteLookupRequest{
		LookupRequest: lookupRequest,
		TrafficOpen:   trafficOpen,
	})
}

// RegisterIdleTunnel 向 runtime 的共享 tunnel registry 注册一条 idle tunnel。
func (runtime *Runtime) RegisterIdleTunnel(
	connectorID string,
	sessionID string,
	tunnel registry.RuntimeTunnel,
) (registry.TunnelRuntime, error) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.tunnelRegistry == nil {
		return registry.TunnelRuntime{}, ErrRuntimeDataPlaneDependencyMissing
	}
	// 数据面路径要求 tunnel 注册表使用 UTC 时间，避免跨时区日志歧义。
	return runtime.dataPlane.tunnelRegistry.UpsertIdle(time.Now().UTC(), connectorID, sessionID, tunnel)
}

// copyStringMap 复制 map，避免调用方后续修改影响 runtime 内部执行。
func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// resolveTrafficServiceIDFromResolution 从解析结果补齐 connector 路径必需的 service_id。
func resolveTrafficServiceIDFromResolution(result routing.ResolveResult) string {
	serviceID, _, _ := resolveTrafficServiceIdentityFromResolution(result)
	return serviceID
}

// resolveTrafficServiceKeyFromResolution 从解析结果提取服务 key，供日志与审计复用。
func resolveTrafficServiceKeyFromResolution(result routing.ResolveResult) string {
	_, serviceKey, _ := resolveTrafficServiceIdentityFromResolution(result)
	return serviceKey
}

// resolveTrafficServiceInstanceIDFromResolution 从解析结果提取实例 ID，供实例级观测复用。
func resolveTrafficServiceInstanceIDFromResolution(result routing.ResolveResult) string {
	_, _, serviceInstanceID := resolveTrafficServiceIdentityFromResolution(result)
	return serviceInstanceID
}

// resolveTrafficServiceIdentityFromResolution 统一提取 service_id/service_key/service_instance_id。
func resolveTrafficServiceIdentityFromResolution(result routing.ResolveResult) (string, string, string) {
	switch result.TargetKind {
	case pb.RouteTargetTypeConnectorService:
		if result.Connector != nil {
			return strings.TrimSpace(result.Connector.Service.ServiceID),
				strings.TrimSpace(result.Connector.Service.ServiceKey),
				strings.TrimSpace(result.Connector.ServiceInstanceID)
		}
	case pb.RouteTargetTypeHybridGroup:
		if result.Hybrid != nil {
			return strings.TrimSpace(result.Hybrid.Primary.Service.ServiceID),
				strings.TrimSpace(result.Hybrid.Primary.Service.ServiceKey),
				strings.TrimSpace(result.Hybrid.Primary.ServiceInstanceID)
		}
	}
	return "", "", ""
}

// enrichTrafficOpenServiceIdentity 把服务身份补齐到 traffic open，确保下游日志字段默认可用。
func enrichTrafficOpenServiceIdentity(
	trafficOpen *pb.TrafficOpen,
	serviceID string,
	serviceKey string,
	serviceInstanceID string,
) {
	if trafficOpen == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if strings.TrimSpace(trafficOpen.ServiceID) == "" && normalizedServiceID != "" {
		trafficOpen.ServiceID = normalizedServiceID
	}
	if trafficOpen.Metadata == nil {
		trafficOpen.Metadata = make(map[string]string, 3)
	}
	// 仅在调用方未显式覆写时补默认值，兼容上层自定义元数据。
	if strings.TrimSpace(trafficOpen.Metadata[trafficMetadataServiceIDKey]) == "" && normalizedServiceID != "" {
		trafficOpen.Metadata[trafficMetadataServiceIDKey] = normalizedServiceID
	}
	if strings.TrimSpace(trafficOpen.Metadata[trafficMetadataServiceKeyKey]) == "" && normalizedServiceKey != "" {
		trafficOpen.Metadata[trafficMetadataServiceKeyKey] = normalizedServiceKey
	}
	if strings.TrimSpace(trafficOpen.Metadata[trafficMetadataServiceInstanceIDKey]) == "" &&
		normalizedServiceInstanceID != "" {
		trafficOpen.Metadata[trafficMetadataServiceInstanceIDKey] = normalizedServiceInstanceID
	}
}

// observeTrafficOwnership 把 traffic 与服务归属关系写入索引，供排障链路反查。
func (runtime *Runtime) observeTrafficOwnership(resolution routing.ResolveResult, trafficOpen pb.TrafficOpen) {
	if runtime == nil || runtime.dataPlane == nil || runtime.dataPlane.trafficOwnership == nil {
		return
	}
	record := buildTrafficOwnershipRecord(resolution, trafficOpen)
	runtime.dataPlane.trafficOwnership.Observe(record)
}

// resolveTrafficOwnership 按 traffic_id 查询服务归属快照。
func (dataPlane *runtimeDataPlane) resolveTrafficOwnership(trafficID string) (trafficOwnershipRecord, bool) {
	if dataPlane == nil || dataPlane.trafficOwnership == nil {
		return trafficOwnershipRecord{}, false
	}
	return dataPlane.trafficOwnership.Load(trafficID)
}

// buildTrafficOwnershipRecord 从路由解析与 traffic_open 构造服务归属记录。
func buildTrafficOwnershipRecord(resolution routing.ResolveResult, trafficOpen pb.TrafficOpen) trafficOwnershipRecord {
	record := trafficOwnershipRecord{
		TrafficID:         strings.TrimSpace(trafficOpen.TrafficID),
		RouteID:           strings.TrimSpace(trafficOpen.RouteID),
		TargetKind:        strings.TrimSpace(string(resolution.TargetKind)),
		IngressMode:       strings.TrimSpace(string(resolution.IngressMode)),
		ServiceID:         strings.TrimSpace(trafficOpen.ServiceID),
		ServiceKey:        strings.TrimSpace(trafficOpen.Metadata[trafficMetadataServiceKeyKey]),
		ServiceInstanceID: strings.TrimSpace(trafficOpen.Metadata[trafficMetadataServiceInstanceIDKey]),
	}
	switch resolution.TargetKind {
	case pb.RouteTargetTypeConnectorService:
		if resolution.Connector != nil {
			// connector 路径优先采用 resolver 结果，避免上游透传字段不一致。
			record.ServiceID = firstNonEmptyTrimmed(
				record.ServiceID,
				resolution.Connector.Service.ServiceID,
			)
			record.ServiceKey = firstNonEmptyTrimmed(
				record.ServiceKey,
				resolution.Connector.Service.ServiceKey,
			)
			record.ServiceInstanceID = firstNonEmptyTrimmed(
				record.ServiceInstanceID,
				resolution.Connector.ServiceInstanceID,
			)
			record.ConnectorID = strings.TrimSpace(resolution.Connector.Session.ConnectorID)
			record.SessionID = strings.TrimSpace(resolution.Connector.Session.SessionID)
		}
	case pb.RouteTargetTypeHybridGroup:
		if resolution.Hybrid != nil {
			// hybrid 路径归属到 primary connector 实例，便于统一排障口径。
			record.ServiceID = firstNonEmptyTrimmed(
				record.ServiceID,
				resolution.Hybrid.Primary.Service.ServiceID,
			)
			record.ServiceKey = firstNonEmptyTrimmed(
				record.ServiceKey,
				resolution.Hybrid.Primary.Service.ServiceKey,
			)
			record.ServiceInstanceID = firstNonEmptyTrimmed(
				record.ServiceInstanceID,
				resolution.Hybrid.Primary.ServiceInstanceID,
			)
			record.ConnectorID = strings.TrimSpace(resolution.Hybrid.Primary.Session.ConnectorID)
			record.SessionID = strings.TrimSpace(resolution.Hybrid.Primary.Session.SessionID)
		}
	}
	return record
}

// firstNonEmptyTrimmed 返回首个非空字符串（会先做 trim）。
func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		normalizedValue := strings.TrimSpace(value)
		if normalizedValue != "" {
			return normalizedValue
		}
	}
	return ""
}
