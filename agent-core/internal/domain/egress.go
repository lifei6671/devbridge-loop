package domain

import "time"

// EgressHTTPRequest 定义本地应用通过 agent 发起的 HTTP 出口代理请求。
type EgressHTTPRequest struct {
	ServiceName string              `json:"serviceName"`
	Env         string              `json:"env,omitempty"`
	Method      string              `json:"method,omitempty"`
	Path        string              `json:"path,omitempty"`
	RawQuery    string              `json:"rawQuery,omitempty"`
	Headers     map[string][]string `json:"headers,omitempty"`
	Body        []byte              `json:"body,omitempty"`
	BaseURL     string              `json:"baseUrl,omitempty"`
}

// EgressHTTPResponse 定义 HTTP 出口代理结果。
type EgressHTTPResponse struct {
	StatusCode  int                 `json:"statusCode"`
	Headers     map[string][]string `json:"headers,omitempty"`
	Body        []byte              `json:"body,omitempty"`
	ResolvedEnv string              `json:"resolvedEnv"`
	Resolution  string              `json:"resolution"`
	Upstream    string              `json:"upstream"`
	ErrorCode   ErrorCode           `json:"errorCode,omitempty"`
	Message     string              `json:"message,omitempty"`
}

// EgressGRPCRequest 定义本地应用通过 agent 发起的 gRPC 出口代理请求。
// 一期 MVP 先支持 health check 调用，后续可扩展到任意方法代理。
type EgressGRPCRequest struct {
	ServiceName   string `json:"serviceName"`
	Env           string `json:"env,omitempty"`
	BaseAddress   string `json:"baseAddress,omitempty"`
	HealthService string `json:"healthService,omitempty"`
	TimeoutMs     int    `json:"timeoutMs,omitempty"`
}

// EgressGRPCResponse 定义 gRPC 出口代理结果。
type EgressGRPCResponse struct {
	Status       string    `json:"status"`
	ResolvedEnv  string    `json:"resolvedEnv"`
	Resolution   string    `json:"resolution"`
	Upstream     string    `json:"upstream"`
	Target       string    `json:"target"`
	LatencyMs    int64     `json:"latencyMs"`
	ErrorCode    ErrorCode `json:"errorCode,omitempty"`
	Message      string    `json:"message,omitempty"`
	ObservedTime time.Time `json:"observedTime"`
}

// RequestSummary 用于记录入口/出口请求摘要，供诊断接口与 UI 展示。
type RequestSummary struct {
	Direction    string    `json:"direction"`
	Protocol     string    `json:"protocol"`
	ServiceName  string    `json:"serviceName"`
	RequestedEnv string    `json:"requestedEnv"`
	ResolvedEnv  string    `json:"resolvedEnv"`
	Resolution   string    `json:"resolution"`
	Upstream     string    `json:"upstream"`
	StatusCode   int       `json:"statusCode"`
	Result       string    `json:"result"`
	ErrorCode    ErrorCode `json:"errorCode,omitempty"`
	Message      string    `json:"message,omitempty"`
	LatencyMs    int64     `json:"latencyMs"`
	OccurredAt   time.Time `json:"occurredAt"`
}
