package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAgentBaseURL = "http://127.0.0.1:39090"
)

// AgentOptions 定义 agent registry 适配器初始化参数。
type AgentOptions struct {
	AgentAddr  string
	RuntimeEnv string
	HTTPClient *http.Client
}

// AgentAdapter 通过 dev-agent HTTP API 提供 register/discover 能力。
type AgentAdapter struct {
	baseURL    string
	runtimeEnv string
	httpClient *http.Client
}

type localRegistrationPayload struct {
	ServiceName string            `json:"serviceName"`
	Env         string            `json:"env"`
	InstanceID  string            `json:"instanceId,omitempty"`
	TTLSeconds  int               `json:"ttlSeconds,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Endpoints   []endpointPayload `json:"endpoints"`
}

type endpointPayload struct {
	Protocol   string `json:"protocol"`
	ListenHost string `json:"listenHost,omitempty"`
	ListenPort int    `json:"listenPort,omitempty"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort"`
	Status     string `json:"status,omitempty"`
}

type discoverPayload struct {
	ServiceName string `json:"serviceName"`
	Env         string `json:"env,omitempty"`
	Protocol    string `json:"protocol"`
}

type discoverResultPayload struct {
	Matched      bool   `json:"matched"`
	ResolvedEnv  string `json:"resolvedEnv"`
	Resolution   string `json:"resolution"`
	ResourceHint string `json:"resourceHint"`
	RouteTarget  struct {
		TargetHost string `json:"targetHost"`
		TargetPort int    `json:"targetPort"`
	} `json:"routeTarget"`
}

// NewAgentAdapter 创建 `registry=agent` 的适配器实现。
func NewAgentAdapter(options AgentOptions) (*AgentAdapter, error) {
	baseURL, err := normalizeAgentBaseURL(options.AgentAddr)
	if err != nil {
		return nil, err
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	return &AgentAdapter{
		baseURL:    baseURL,
		runtimeEnv: strings.TrimSpace(options.RuntimeEnv),
		httpClient: httpClient,
	}, nil
}

// Register 将业务服务注册到本地 dev-agent。
func (a *AgentAdapter) Register(ctx context.Context, registration Registration) (Registration, error) {
	if err := validateRegistration(registration); err != nil {
		return Registration{}, err
	}

	payload := localRegistrationPayload{
		ServiceName: strings.TrimSpace(registration.ServiceName),
		Env:         firstNonBlank(strings.TrimSpace(registration.Env), a.runtimeEnv),
		InstanceID:  strings.TrimSpace(registration.InstanceID),
		TTLSeconds:  registration.TTLSeconds,
		Metadata:    copyStringMap(registration.Metadata),
		Endpoints:   make([]endpointPayload, 0, len(registration.Endpoints)),
	}
	for _, endpoint := range registration.Endpoints {
		payload.Endpoints = append(payload.Endpoints, endpointPayload{
			Protocol:   strings.TrimSpace(endpoint.Protocol),
			ListenHost: strings.TrimSpace(endpoint.ListenHost),
			ListenPort: endpoint.ListenPort,
			TargetHost: firstNonBlank(strings.TrimSpace(endpoint.TargetHost), "127.0.0.1"),
			TargetPort: endpoint.TargetPort,
			Status:     firstNonBlank(strings.TrimSpace(endpoint.Status), "active"),
		})
	}

	var response localRegistrationPayload
	if err := a.doJSON(ctx, http.MethodPost, "/api/v1/registrations", payload, &response); err != nil {
		return Registration{}, err
	}
	return mapRegistrationFromPayload(response), nil
}

// Heartbeat 刷新指定实例在 dev-agent 的存活时间。
func (a *AgentAdapter) Heartbeat(ctx context.Context, instanceID string) error {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return fmt.Errorf("instanceID is required")
	}
	path := fmt.Sprintf("/api/v1/registrations/%s/heartbeat", url.PathEscape(id))
	return a.doJSON(ctx, http.MethodPost, path, map[string]any{}, &map[string]any{})
}

// Unregister 将实例从 dev-agent 注销。
func (a *AgentAdapter) Unregister(ctx context.Context, instanceID string) error {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return fmt.Errorf("instanceID is required")
	}
	path := fmt.Sprintf("/api/v1/registrations/%s", url.PathEscape(id))
	return a.doJSON(ctx, http.MethodDelete, path, nil, &map[string]any{})
}

// Discover 通过 dev-agent 执行 dev 优先、base fallback 的发现逻辑。
func (a *AgentAdapter) Discover(ctx context.Context, request DiscoverRequest) (DiscoverResponse, error) {
	serviceName := strings.TrimSpace(request.ServiceName)
	protocol := strings.TrimSpace(request.Protocol)
	if serviceName == "" || protocol == "" {
		return DiscoverResponse{}, fmt.Errorf("serviceName and protocol are required")
	}

	payload := discoverPayload{
		ServiceName: serviceName,
		Env:         firstNonBlank(strings.TrimSpace(request.Env), a.runtimeEnv),
		Protocol:    protocol,
	}

	var response discoverResultPayload
	if err := a.doJSON(ctx, http.MethodPost, "/api/v1/discover", payload, &response); err != nil {
		return DiscoverResponse{}, err
	}

	return DiscoverResponse{
		Matched:      response.Matched,
		ResolvedEnv:  strings.TrimSpace(response.ResolvedEnv),
		Resolution:   strings.TrimSpace(response.Resolution),
		TargetHost:   strings.TrimSpace(response.RouteTarget.TargetHost),
		TargetPort:   response.RouteTarget.TargetPort,
		ResourceHint: strings.TrimSpace(response.ResourceHint),
	}, nil
}

func (a *AgentAdapter) doJSON(ctx context.Context, method string, path string, requestBody any, responseBody any) error {
	rawURL := strings.TrimRight(a.baseURL, "/") + "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	bodyBytes := []byte{}
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request failed: %w", err)
		}
		bodyBytes = encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("build request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent request failed: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		// agent 错误返回统一提取 message/error 字段，便于业务侧日志统一观测。
		message := strings.TrimSpace(string(payload))
		if len(payload) > 0 {
			var body map[string]any
			if json.Unmarshal(payload, &body) == nil {
				message = firstNonBlank(stringValue(body["message"]), stringValue(body["error"]), message)
			}
		}
		return fmt.Errorf("agent request failed with status %d: %s", resp.StatusCode, message)
	}

	if responseBody == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, responseBody); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}
	return nil
}

func validateRegistration(registration Registration) error {
	if strings.TrimSpace(registration.ServiceName) == "" {
		return fmt.Errorf("serviceName is required")
	}
	if len(registration.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint is required")
	}
	for i, endpoint := range registration.Endpoints {
		if strings.TrimSpace(endpoint.Protocol) == "" {
			return fmt.Errorf("endpoints[%d].protocol is required", i)
		}
		if endpoint.TargetPort <= 0 {
			return fmt.Errorf("endpoints[%d].targetPort must be positive", i)
		}
	}
	return nil
}

func mapRegistrationFromPayload(payload localRegistrationPayload) Registration {
	result := Registration{
		ServiceName: strings.TrimSpace(payload.ServiceName),
		Env:         strings.TrimSpace(payload.Env),
		InstanceID:  strings.TrimSpace(payload.InstanceID),
		TTLSeconds:  payload.TTLSeconds,
		Metadata:    copyStringMap(payload.Metadata),
		Endpoints:   make([]Endpoint, 0, len(payload.Endpoints)),
	}
	for _, endpoint := range payload.Endpoints {
		result.Endpoints = append(result.Endpoints, Endpoint{
			Protocol:   strings.TrimSpace(endpoint.Protocol),
			ListenHost: strings.TrimSpace(endpoint.ListenHost),
			ListenPort: endpoint.ListenPort,
			TargetHost: strings.TrimSpace(endpoint.TargetHost),
			TargetPort: endpoint.TargetPort,
			Status:     strings.TrimSpace(endpoint.Status),
		})
	}
	return result
}

func normalizeAgentBaseURL(value string) (string, error) {
	baseURL := strings.TrimSpace(value)
	if baseURL == "" {
		baseURL = defaultAgentBaseURL
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse agentAddr failed: %w", err)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("agentAddr host is empty")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
