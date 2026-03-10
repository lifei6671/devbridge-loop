package serviceregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAgentCoreBaseURL = "http://127.0.0.1:39090"
	defaultSearchWatchEvery = 2 * time.Second
	defaultHeartbeatTTL     = 30
)

// AgentCoreClientOptions 定义 AgentCore 客户端 SDK 初始化参数。
type AgentCoreClientOptions struct {
	// BaseURL 指定 agent-core 地址，支持 `127.0.0.1:39090` 或完整 URL。
	BaseURL string
	// RuntimeEnv 作为默认 env，Service 元数据未提供 env 时会回退到这里。
	RuntimeEnv string
	// HTTPClient 允许调用方注入自定义 HTTP 客户端。
	HTTPClient *http.Client
	// WatchEvery 指定 Watch 轮询周期。
	WatchEvery time.Duration
}

type agentCoreRegistrationPayload struct {
	ServiceName string            `json:"serviceName"`
	Env         string            `json:"env"`
	InstanceID  string            `json:"instanceId,omitempty"`
	TTLSeconds  int               `json:"ttlSeconds,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Endpoints   []agentEndpoint   `json:"endpoints"`
}

type agentEndpoint struct {
	Protocol   string `json:"protocol"`
	ListenHost string `json:"listenHost,omitempty"`
	ListenPort int    `json:"listenPort,omitempty"`
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort"`
	Status     string `json:"status,omitempty"`
}

type agentLeaseState struct {
	instanceID string
	cancel     context.CancelFunc
}

// AgentCoreClientSDK 封装面向 agent-core 的注册发现客户端能力。
type AgentCoreClientSDK struct {
	baseURL    string
	runtimeEnv string
	httpClient *http.Client
	watchEvery time.Duration

	mu     sync.Mutex
	leases map[string]agentLeaseState
}

// NewAgentCoreClientSDK 创建 agent-core 客户端 SDK。
func NewAgentCoreClientSDK(options AgentCoreClientOptions) (*AgentCoreClientSDK, error) {
	baseURL, err := normalizeAgentCoreBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}

	watchEvery := options.WatchEvery
	if watchEvery <= 0 {
		watchEvery = defaultSearchWatchEvery
	}

	return &AgentCoreClientSDK{
		baseURL:    baseURL,
		runtimeEnv: strings.TrimSpace(options.RuntimeEnv),
		httpClient: httpClient,
		watchEvery: watchEvery,
		leases:     make(map[string]agentLeaseState),
	}, nil
}

// Register 将 Service 注册到 agent-core，并自动启动后台心跳续租。
func (c *AgentCoreClientSDK) Register(ctx context.Context, service Service) (Service, error) {
	payload, leaseKey, leaseTTL, err := c.mapServiceToPayload(service)
	if err != nil {
		return nil, err
	}

	var response agentCoreRegistrationPayload
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/registrations", payload, &response); err != nil {
		return nil, err
	}

	registered := c.mapPayloadToService(response, service)
	c.replaceLease(leaseKey, strings.TrimSpace(response.InstanceID), leaseTTL)
	return registered, nil
}

// Deregister 将 Service 从 agent-core 注销，并停止对应心跳任务。
func (c *AgentCoreClientSDK) Deregister(ctx context.Context, service Service) error {
	if service == nil {
		return fmt.Errorf("service is nil")
	}

	leaseKey := buildSDKLeaseKey(service)
	instanceID := strings.TrimSpace(getStringMetadata(service.GetMetadata(), "instanceId"))
	if instanceID == "" {
		instanceID = c.lookupLeaseInstance(leaseKey)
	}
	if instanceID == "" {
		return fmt.Errorf("instanceId is required for deregister")
	}

	path := fmt.Sprintf("/api/v1/registrations/%s", url.PathEscape(instanceID))
	if err := c.doJSON(ctx, http.MethodDelete, path, nil, &map[string]any{}); err != nil {
		return err
	}
	c.stopLease(leaseKey)
	return nil
}

// Search 从 agent-core 拉取注册列表并按 SearchInput 条件过滤。
func (c *AgentCoreClientSDK) Search(ctx context.Context, input SearchInput) ([]Service, error) {
	var payloads []agentCoreRegistrationPayload
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/registrations", nil, &payloads); err != nil {
		return nil, err
	}

	result := make([]Service, 0, len(payloads))
	for _, payload := range payloads {
		service := c.mapPayloadToService(payload, nil)
		if !matchSDKSearchInput(service, input) {
			continue
		}
		result = append(result, service)
	}

	// 排序后返回，保证调用方获取稳定结果，便于缓存和对比。
	sort.Slice(result, func(i, j int) bool {
		return result[i].GetKey() < result[j].GetKey()
	})
	return result, nil
}

// Watch 使用轮询方式监听服务变化。
func (c *AgentCoreClientSDK) Watch(ctx context.Context, key string) (Watcher, error) {
	return NewPollingWatcher(ctx, key, c.watchEvery, func(searchCtx context.Context, input SearchInput) ([]Service, error) {
		return c.Search(searchCtx, input)
	})
}

// Close 停止当前 SDK 管理的全部心跳任务。
func (c *AgentCoreClientSDK) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	leases := make([]agentLeaseState, 0, len(c.leases))
	for key, lease := range c.leases {
		leases = append(leases, lease)
		delete(c.leases, key)
	}
	c.mu.Unlock()

	for _, lease := range leases {
		if lease.cancel != nil {
			lease.cancel()
		}
	}
	return nil
}

func (c *AgentCoreClientSDK) mapServiceToPayload(service Service) (agentCoreRegistrationPayload, string, int, error) {
	if service == nil {
		return agentCoreRegistrationPayload{}, "", 0, fmt.Errorf("service is nil")
	}

	serviceName := strings.TrimSpace(service.GetName())
	if serviceName == "" {
		return agentCoreRegistrationPayload{}, "", 0, fmt.Errorf("service name is required")
	}
	metadata := normalizeSDKMetadata(service.GetMetadata())

	env := pickFirstNonBlank(getStringMetadata(metadata, "env"), c.runtimeEnv, "base")
	version := pickFirstNonBlank(strings.TrimSpace(service.GetVersion()), getStringMetadata(metadata, "version"), "latest")
	instanceID := strings.TrimSpace(getStringMetadata(metadata, "instanceId"))
	ttlSeconds := getIntMetadata(metadata, "ttlSeconds")
	if ttlSeconds <= 0 {
		ttlSeconds = defaultHeartbeatTTL
	}

	// 注册中心抽象不关心协议，这里只在落地到 agent-core 的 HTTP API 时提供协议提示。
	protocolHint := pickFirstNonBlank(getStringMetadata(metadata, "protocol"), "tcp")
	endpoints := service.GetEndpoints()
	if len(endpoints) == 0 {
		return agentCoreRegistrationPayload{}, "", 0, fmt.Errorf("at least one endpoint is required")
	}

	convertedEndpoints := make([]agentEndpoint, 0, len(endpoints))
	for index, endpoint := range endpoints {
		if endpoint == nil {
			continue
		}
		host := pickFirstNonBlank(endpoint.Host(), "127.0.0.1")
		port := endpoint.Port()
		if port <= 0 {
			return agentCoreRegistrationPayload{}, "", 0, fmt.Errorf("endpoints[%d].port must be positive", index)
		}
		convertedEndpoints = append(convertedEndpoints, agentEndpoint{
			Protocol:   protocolHint,
			ListenHost: host,
			ListenPort: port,
			TargetHost: host,
			TargetPort: port,
			Status:     "active",
		})
	}
	if len(convertedEndpoints) == 0 {
		return agentCoreRegistrationPayload{}, "", 0, fmt.Errorf("at least one endpoint is required")
	}

	metadataAsString := metadataToStringMap(metadata)
	metadataAsString["env"] = env
	metadataAsString["version"] = version
	if key := strings.TrimSpace(service.GetKey()); key != "" {
		metadataAsString["serviceKey"] = key
	}
	if prefix := strings.TrimSpace(service.GetPrefix()); prefix != "" {
		metadataAsString["servicePrefix"] = prefix
	}

	return agentCoreRegistrationPayload{
		ServiceName: serviceName,
		Env:         env,
		InstanceID:  instanceID,
		TTLSeconds:  ttlSeconds,
		Metadata:    metadataAsString,
		Endpoints:   convertedEndpoints,
	}, buildSDKLeaseKey(service), ttlSeconds, nil
}

func (c *AgentCoreClientSDK) mapPayloadToService(payload agentCoreRegistrationPayload, source Service) Service {
	metadata := metadataFromStringMap(payload.Metadata)
	metadata["env"] = pickFirstNonBlank(getStringMetadata(metadata, "env"), strings.TrimSpace(payload.Env), "base")
	metadata["instanceId"] = pickFirstNonBlank(getStringMetadata(metadata, "instanceId"), strings.TrimSpace(payload.InstanceID))
	version := pickFirstNonBlank(getStringMetadata(metadata, "version"), sourceVersion(source), "latest")
	metadata["version"] = version

	endpoints := make(Endpoints, 0, len(payload.Endpoints))
	for _, endpoint := range payload.Endpoints {
		host := pickFirstNonBlank(strings.TrimSpace(endpoint.TargetHost), strings.TrimSpace(endpoint.ListenHost), "127.0.0.1")
		port := endpoint.TargetPort
		if port <= 0 {
			port = endpoint.ListenPort
		}
		if port <= 0 {
			continue
		}
		endpoints = append(endpoints, NewBasicEndpoint(host, port))
	}

	key := strings.TrimSpace(getStringMetadata(metadata, "serviceKey"))
	if key == "" {
		key = sourceKey(source)
	}
	prefix := strings.TrimSpace(getStringMetadata(metadata, "servicePrefix"))
	if prefix == "" {
		prefix = sourcePrefix(source)
	}

	return NewBasicService(
		payload.ServiceName,
		WithServiceVersion(version),
		WithServiceMetadata(metadata),
		WithServiceEndpoints(endpoints),
		WithServiceKey(key),
		WithServicePrefix(prefix),
	)
}

func (c *AgentCoreClientSDK) doJSON(ctx context.Context, method string, path string, requestBody any, responseBody any) error {
	rawURL := strings.TrimRight(c.baseURL, "/") + "/" + strings.TrimLeft(strings.TrimSpace(path), "/")

	var bodyReader io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request failed: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return fmt.Errorf("build request failed: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("agent-core request failed: %w", err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		message := strings.TrimSpace(string(payload))
		if len(payload) > 0 {
			var body map[string]any
			if json.Unmarshal(payload, &body) == nil {
				message = pickFirstNonBlank(
					strings.TrimSpace(fmt.Sprintf("%v", body["message"])),
					strings.TrimSpace(fmt.Sprintf("%v", body["error"])),
					message,
				)
			}
		}
		return fmt.Errorf("agent-core request failed with status %d: %s", response.StatusCode, message)
	}

	if responseBody == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, responseBody); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}
	return nil
}

func (c *AgentCoreClientSDK) replaceLease(leaseKey, instanceID string, ttlSeconds int) {
	if strings.TrimSpace(leaseKey) == "" || strings.TrimSpace(instanceID) == "" {
		return
	}
	interval := heartbeatInterval(ttlSeconds)

	c.mu.Lock()
	if current, exists := c.leases[leaseKey]; exists && current.cancel != nil {
		current.cancel()
	}
	leaseCtx, cancel := context.WithCancel(context.Background())
	c.leases[leaseKey] = agentLeaseState{
		instanceID: instanceID,
		cancel:     cancel,
	}
	c.mu.Unlock()

	go c.heartbeatLoop(leaseCtx, instanceID, interval)
}

func (c *AgentCoreClientSDK) stopLease(leaseKey string) {
	c.mu.Lock()
	lease, exists := c.leases[leaseKey]
	if exists {
		delete(c.leases, leaseKey)
	}
	c.mu.Unlock()

	if exists && lease.cancel != nil {
		lease.cancel()
	}
}

func (c *AgentCoreClientSDK) lookupLeaseInstance(leaseKey string) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	lease, exists := c.leases[leaseKey]
	if !exists {
		return ""
	}
	return strings.TrimSpace(lease.instanceID)
}

func (c *AgentCoreClientSDK) heartbeatLoop(ctx context.Context, instanceID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 心跳失败不立即停止续租循环，避免瞬时抖动导致服务直接过期。
			hbCtx, cancel := context.WithTimeout(ctx, minDuration(interval, 5*time.Second))
			_ = c.sendHeartbeat(hbCtx, instanceID)
			cancel()
		}
	}
}

func (c *AgentCoreClientSDK) sendHeartbeat(ctx context.Context, instanceID string) error {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return fmt.Errorf("instanceID is required")
	}
	path := fmt.Sprintf("/api/v1/registrations/%s/heartbeat", url.PathEscape(id))
	return c.doJSON(ctx, http.MethodPost, path, map[string]any{}, &map[string]any{})
}

func normalizeAgentCoreBaseURL(value string) (string, error) {
	baseURL := strings.TrimSpace(value)
	if baseURL == "" {
		baseURL = defaultAgentCoreBaseURL
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse agent-core url failed: %w", err)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("agent-core host is empty")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func matchSDKSearchInput(service Service, input SearchInput) bool {
	if service == nil {
		return false
	}
	if name := strings.TrimSpace(input.Name); name != "" && !strings.EqualFold(name, service.GetName()) {
		return false
	}
	if version := strings.TrimSpace(input.Version); version != "" && !strings.EqualFold(version, service.GetVersion()) {
		return false
	}
	if prefix := strings.TrimSpace(input.Prefix); prefix != "" && !strings.HasPrefix(service.GetKey(), prefix) {
		return false
	}
	if len(input.Metadata) == 0 {
		return true
	}
	serviceMetadata := service.GetMetadata()
	for key, expected := range input.Metadata {
		actual, exists := serviceMetadata[key]
		if !exists {
			return false
		}
		if strings.TrimSpace(fmt.Sprintf("%v", actual)) != strings.TrimSpace(fmt.Sprintf("%v", expected)) {
			return false
		}
	}
	return true
}

func normalizeSDKMetadata(metadata Metadata) Metadata {
	if len(metadata) == 0 {
		return Metadata{}
	}
	copied := make(Metadata, len(metadata))
	for key, value := range metadata {
		copied[strings.TrimSpace(key)] = value
	}
	return copied
}

func metadataToStringMap(metadata Metadata) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	result := make(map[string]string, len(metadata))
	for key, value := range metadata {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" || value == nil {
			continue
		}
		result[normalizedKey] = strings.TrimSpace(fmt.Sprintf("%v", value))
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func metadataFromStringMap(metadata map[string]string) Metadata {
	if len(metadata) == 0 {
		return Metadata{}
	}
	result := make(Metadata, len(metadata))
	for key, value := range metadata {
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result
}

func getStringMetadata(metadata Metadata, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, exists := metadata[key]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func getIntMetadata(metadata Metadata, key string) int {
	raw := getStringMetadata(metadata, key)
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return parsed
}

func buildSDKLeaseKey(service Service) string {
	if service == nil {
		return ""
	}
	metadata := service.GetMetadata()
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(service.GetName())),
		strings.ToLower(strings.TrimSpace(service.GetVersion())),
		strings.ToLower(strings.TrimSpace(getStringMetadata(metadata, "env"))),
	}, "|")
}

func sourceVersion(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetVersion())
}

func sourceKey(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetKey())
}

func sourcePrefix(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetPrefix())
}

func heartbeatInterval(ttlSeconds int) time.Duration {
	ttl := ttlSeconds
	if ttl <= 0 {
		ttl = defaultHeartbeatTTL
	}
	interval := time.Duration(ttl/3) * time.Second
	if interval < time.Second {
		return time.Second
	}
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func pickFirstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minDuration(left, right time.Duration) time.Duration {
	if left <= right {
		return left
	}
	return right
}
