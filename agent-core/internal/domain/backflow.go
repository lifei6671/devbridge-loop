package domain

// BackflowHTTPRequest 描述 bridge 下发给 agent 的 HTTP 回流请求。
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

// BackflowHTTPResponse 描述 agent 回传给 bridge 的 HTTP 回流结果。
type BackflowHTTPResponse struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
	ErrorCode  ErrorCode           `json:"errorCode,omitempty"`
	Message    string              `json:"message,omitempty"`
}

// BackflowGRPCRequest 描述 bridge 下发给 agent 的 gRPC 回流请求。
// 一期与 egress 一致，先支持 health check 语义。
type BackflowGRPCRequest struct {
	TargetHost    string `json:"targetHost"`
	TargetPort    int    `json:"targetPort"`
	Env           string `json:"env,omitempty"`
	HealthService string `json:"healthService,omitempty"`
	TimeoutMs     int    `json:"timeoutMs,omitempty"`
}

// BackflowGRPCResponse 描述 agent 回传给 bridge 的 gRPC 回流结果。
type BackflowGRPCResponse struct {
	Status    string    `json:"status"`
	Target    string    `json:"target"`
	LatencyMs int64     `json:"latencyMs"`
	ErrorCode ErrorCode `json:"errorCode,omitempty"`
	Message   string    `json:"message,omitempty"`
}
