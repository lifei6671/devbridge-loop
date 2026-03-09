package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 封装 demo 服务与 agent-core 的 HTTP 交互。
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Registration 描述 demo 服务向 agent 注册所需信息。
type Registration struct {
	ServiceName string
	Env         string
	InstanceID  string
	TTLSeconds  int
	HTTPHost    string
	HTTPPort    int
	GRPCHost    string
	GRPCPort    int
}

// EgressHTTPRequest 是调用 agent HTTP 出口代理的请求体。
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

// EgressHTTPResponse 是 agent HTTP 出口代理响应。
type EgressHTTPResponse struct {
	StatusCode  int                 `json:"statusCode"`
	Headers     map[string][]string `json:"headers,omitempty"`
	Body        []byte              `json:"body,omitempty"`
	ResolvedEnv string              `json:"resolvedEnv"`
	Resolution  string              `json:"resolution"`
	Upstream    string              `json:"upstream"`
	ErrorCode   string              `json:"errorCode,omitempty"`
	Message     string              `json:"message,omitempty"`
}

// EgressGRPCRequest 是调用 agent gRPC 出口代理的请求体。
type EgressGRPCRequest struct {
	ServiceName   string `json:"serviceName"`
	Env           string `json:"env,omitempty"`
	BaseAddress   string `json:"baseAddress,omitempty"`
	HealthService string `json:"healthService,omitempty"`
	TimeoutMs     int    `json:"timeoutMs,omitempty"`
}

// EgressGRPCResponse 是 agent gRPC 出口代理响应。
type EgressGRPCResponse struct {
	Status       string `json:"status"`
	ResolvedEnv  string `json:"resolvedEnv"`
	Resolution   string `json:"resolution"`
	Upstream     string `json:"upstream"`
	Target       string `json:"target"`
	LatencyMs    int64  `json:"latencyMs"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Message      string `json:"message,omitempty"`
	ObservedTime string `json:"observedTime"`
}

// New 创建 agent 客户端；baseURL 支持有无协议前缀两种写法。
func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		baseURL: normalizeBaseURL(baseURL),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Register 向 agent 注册当前 demo 服务的 HTTP/gRPC endpoint。
func (c *Client) Register(ctx context.Context, registration Registration) error {
	payload := map[string]any{
		"serviceName": registration.ServiceName,
		"env":         registration.Env,
		"instanceId":  registration.InstanceID,
		"ttlSeconds":  registration.TTLSeconds,
		"endpoints": []map[string]any{
			{
				"protocol":   "http",
				"listenHost": registration.HTTPHost,
				"listenPort": registration.HTTPPort,
				"targetHost": registration.HTTPHost,
				"targetPort": registration.HTTPPort,
				"status":     "active",
			},
			{
				"protocol":   "grpc",
				"listenHost": registration.GRPCHost,
				"listenPort": registration.GRPCPort,
				"targetHost": registration.GRPCHost,
				"targetPort": registration.GRPCPort,
				"status":     "active",
			},
		},
	}

	return c.doJSONRequest(ctx, http.MethodPost, "/api/v1/registrations", payload, nil)
}

// Heartbeat 向 agent 发送心跳，续期当前注册项。
func (c *Client) Heartbeat(ctx context.Context, instanceID string) error {
	path := fmt.Sprintf("/api/v1/registrations/%s/heartbeat", strings.TrimSpace(instanceID))
	return c.doJSONRequest(ctx, http.MethodPost, path, map[string]any{}, nil)
}

// Unregister 从 agent 注销当前实例。
func (c *Client) Unregister(ctx context.Context, instanceID string) error {
	path := fmt.Sprintf("/api/v1/registrations/%s", strings.TrimSpace(instanceID))
	return c.doJSONRequest(ctx, http.MethodDelete, path, nil, nil)
}

// EgressHTTP 调用 agent HTTP 出口代理。
func (c *Client) EgressHTTP(ctx context.Context, request EgressHTTPRequest) (EgressHTTPResponse, error) {
	response := EgressHTTPResponse{}
	if err := c.doJSONRequest(ctx, http.MethodPost, "/api/v1/egress/http", request, &response); err != nil {
		return EgressHTTPResponse{}, err
	}
	return response, nil
}

// EgressGRPC 调用 agent gRPC 出口代理。
func (c *Client) EgressGRPC(ctx context.Context, request EgressGRPCRequest) (EgressGRPCResponse, error) {
	response := EgressGRPCResponse{}
	if err := c.doJSONRequest(ctx, http.MethodPost, "/api/v1/egress/grpc", request, &response); err != nil {
		return EgressGRPCResponse{}, err
	}
	return response, nil
}

// doJSONRequest 统一处理序列化、请求发送、错误透传与响应解码。
func (c *Client) doJSONRequest(ctx context.Context, method string, path string, payload any, output any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request payload failed: %w", err)
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("agent api status=%d body=%s", response.StatusCode, strings.TrimSpace(string(message)))
	}

	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}
	return nil
}

func normalizeBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "http://127.0.0.1:19090"
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return strings.TrimRight(trimmed, "/")
	}
	return "http://" + strings.TrimRight(trimmed, "/")
}
