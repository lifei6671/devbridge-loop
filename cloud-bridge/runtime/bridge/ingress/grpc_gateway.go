package ingress

// GRPCGatewayRequest 描述 gRPC 入口请求上下文。
type GRPCGatewayRequest struct {
	Authority   string
	Path        string
	Namespace   string
	Environment string
	Metadata    map[string]string
}

// GRPCGateway 负责把 gRPC 请求转换为统一路由请求。
type GRPCGateway struct {
	ListenPort uint32
}

// NewGRPCGateway 创建 gRPC 入口适配器。
func NewGRPCGateway(listenPort uint32) *GRPCGateway {
	return &GRPCGateway{ListenPort: listenPort}
}

// BuildRouteLookupRequest 将 gRPC 请求转换为统一路由请求。
func (gateway *GRPCGateway) BuildRouteLookupRequest(request GRPCGatewayRequest) (RouteLookupRequest, error) {
	return buildL7RouteLookupRequest(gateway.ListenPort, "grpc", l7GatewayRequest{
		Protocol:    "grpc",
		Authority:   request.Authority,
		Path:        request.Path,
		Namespace:   request.Namespace,
		Environment: request.Environment,
		Metadata:    request.Metadata,
	})
}
