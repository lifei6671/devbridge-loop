package domain

const (
	// IngressErrorRouteNotFound 表示入口请求未命中任何可用路由。
	IngressErrorRouteNotFound = "ROUTE_NOT_FOUND"
	// IngressErrorRouteExtractFailed 表示入口请求无法提取 service/env。
	IngressErrorRouteExtractFailed = "ROUTE_EXTRACT_FAILED"
	// IngressErrorTunnelOffline 表示目标 tunnel 当前不可用。
	IngressErrorTunnelOffline = "TUNNEL_OFFLINE"
	// IngressErrorLocalEndpointDown 表示 agent 无法访问本地服务端点。
	IngressErrorLocalEndpointDown = "LOCAL_ENDPOINT_UNREACHABLE"
	// IngressErrorUpstreamTimeout 表示回流转发超时。
	IngressErrorUpstreamTimeout = "UPSTREAM_TIMEOUT"
)

// BackflowHTTPRequest 描述 bridge 发给 agent 的回流请求。
type BackflowHTTPRequest struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	RawQuery   string              `json:"rawQuery,omitempty"`
	Host       string              `json:"host,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
	TargetHost string              `json:"targetHost"`
	TargetPort int                 `json:"targetPort"`
	Protocol   string              `json:"protocol"`
}

// BackflowHTTPResponse 描述 agent 回传给 bridge 的转发结果。
type BackflowHTTPResponse struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
	ErrorCode  string              `json:"errorCode,omitempty"`
	Message    string              `json:"message,omitempty"`
}
