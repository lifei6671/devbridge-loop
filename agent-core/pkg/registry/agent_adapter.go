package registry

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

	serviceregistry "github.com/lifei6671/devbridge-loop/service-registry"
)

const (
	defaultAgentBaseURL   = "http://127.0.0.1:39090"
	defaultWatchInterval  = 2 * time.Second
	defaultLeaseTTLSecond = 30
)

// AgentOptions 定义 agent registry 适配器初始化参数。
type AgentOptions struct {
	AgentAddr  string
	RuntimeEnv string
	HTTPClient *http.Client
	WatchEvery time.Duration
}

// AgentAdapter 通过 dev-agent HTTP API 提供 Registry 抽象能力。
type AgentAdapter struct {
	baseURL    string
	runtimeEnv string
	httpClient *http.Client
	watchEvery time.Duration

	mu     sync.Mutex
	leases map[string]leaseState
}

type leaseState struct {
	instanceID string
	cancel     context.CancelFunc
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

	watchEvery := options.WatchEvery
	if watchEvery <= 0 {
		watchEvery = defaultWatchInterval
	}

	return &AgentAdapter{
		baseURL:    baseURL,
		runtimeEnv: strings.TrimSpace(options.RuntimeEnv),
		httpClient: httpClient,
		watchEvery: watchEvery,
		leases:     make(map[string]leaseState),
	}, nil
}

// Register 将服务注册到本地 dev-agent，并自动维护后台心跳续租。
func (a *AgentAdapter) Register(ctx context.Context, service Service) (Service, error) {
	payload, leaseKey, leaseTTL, err := a.mapServiceToRegistrationPayload(service)
	if err != nil {
		return nil, err
	}

	var response localRegistrationPayload
	if err := a.doJSON(ctx, http.MethodPost, "/api/v1/registrations", payload, &response); err != nil {
		return nil, err
	}

	registered := a.mapServiceFromRegistrationPayload(response, service)
	instanceID := strings.TrimSpace(response.InstanceID)
	a.replaceLease(leaseKey, instanceID, leaseTTL)
	return registered, nil
}

// Deregister 将服务从 dev-agent 注销，并停止该服务的后台心跳。
func (a *AgentAdapter) Deregister(ctx context.Context, service Service) error {
	leaseKey := buildLeaseKey(service)
	instanceID := strings.TrimSpace(metadataString(service.GetMetadata(), "instanceId"))
	if instanceID == "" {
		instanceID = a.lookupLeaseInstance(leaseKey)
	}
	if instanceID == "" {
		return fmt.Errorf("instanceId is required for deregister")
	}

	path := fmt.Sprintf("/api/v1/registrations/%s", url.PathEscape(instanceID))
	if err := a.doJSON(ctx, http.MethodDelete, path, nil, &map[string]any{}); err != nil {
		return err
	}

	a.stopLease(leaseKey)
	return nil
}

// Search 从 dev-agent 的注册列表中检索服务并按条件过滤。
func (a *AgentAdapter) Search(ctx context.Context, in SearchInput) ([]Service, error) {
	var registrations []localRegistrationPayload
	if err := a.doJSON(ctx, http.MethodGet, "/api/v1/registrations", nil, &registrations); err != nil {
		return nil, err
	}

	filtered := make([]Service, 0, len(registrations))
	for _, payload := range registrations {
		service := a.mapServiceFromRegistrationPayload(payload, nil)
		if !matchSearchInput(service, in) {
			continue
		}
		filtered = append(filtered, service)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GetKey() < filtered[j].GetKey()
	})
	return filtered, nil
}

// Watch 基于轮询实现服务变更监听。
func (a *AgentAdapter) Watch(ctx context.Context, key string) (Watcher, error) {
	return serviceregistry.NewPollingWatcher(ctx, key, a.watchEvery, func(searchCtx context.Context, in SearchInput) ([]Service, error) {
		return a.Search(searchCtx, in)
	})
}

func (a *AgentAdapter) replaceLease(leaseKey, instanceID string, ttlSeconds int) {
	if strings.TrimSpace(leaseKey) == "" || strings.TrimSpace(instanceID) == "" {
		return
	}
	interval := heartbeatInterval(ttlSeconds)

	a.mu.Lock()
	if previous, exists := a.leases[leaseKey]; exists && previous.cancel != nil {
		previous.cancel()
	}
	leaseCtx, cancel := context.WithCancel(context.Background())
	a.leases[leaseKey] = leaseState{
		instanceID: instanceID,
		cancel:     cancel,
	}
	a.mu.Unlock()

	go a.heartbeatLoop(leaseCtx, instanceID, interval)
}

func (a *AgentAdapter) stopLease(leaseKey string) {
	a.mu.Lock()
	lease, exists := a.leases[leaseKey]
	if exists {
		delete(a.leases, leaseKey)
	}
	a.mu.Unlock()
	if exists && lease.cancel != nil {
		lease.cancel()
	}
}

func (a *AgentAdapter) lookupLeaseInstance(leaseKey string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	lease, exists := a.leases[leaseKey]
	if !exists {
		return ""
	}
	return strings.TrimSpace(lease.instanceID)
}

func (a *AgentAdapter) heartbeatLoop(ctx context.Context, instanceID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 心跳失败时不直接中断循环，避免短暂网络抖动导致租约完全丢失。
			heartbeatCtx, cancel := context.WithTimeout(ctx, minDuration(interval, 5*time.Second))
			_ = a.sendHeartbeat(heartbeatCtx, instanceID)
			cancel()
		}
	}
}

func (a *AgentAdapter) sendHeartbeat(ctx context.Context, instanceID string) error {
	id := strings.TrimSpace(instanceID)
	if id == "" {
		return fmt.Errorf("instanceID is required")
	}
	path := fmt.Sprintf("/api/v1/registrations/%s/heartbeat", url.PathEscape(id))
	return a.doJSON(ctx, http.MethodPost, path, map[string]any{}, &map[string]any{})
}

func (a *AgentAdapter) mapServiceToRegistrationPayload(service Service) (localRegistrationPayload, string, int, error) {
	if service == nil {
		return localRegistrationPayload{}, "", 0, fmt.Errorf("service is nil")
	}
	serviceName := strings.TrimSpace(service.GetName())
	if serviceName == "" {
		return localRegistrationPayload{}, "", 0, fmt.Errorf("service name is required")
	}

	metadata := normalizeMetadata(service.GetMetadata())
	env := firstNonBlank(metadataString(metadata, "env"), a.runtimeEnv, "base")
	version := firstNonBlank(strings.TrimSpace(service.GetVersion()), "latest")
	instanceID := strings.TrimSpace(metadataString(metadata, "instanceId"))
	ttlSeconds := metadataInt(metadata, "ttlSeconds")
	if ttlSeconds <= 0 {
		ttlSeconds = defaultLeaseTTLSecond
	}

	protocolHint := firstNonBlank(metadataString(metadata, "protocol"), "tcp")
	endpoints := service.GetEndpoints()
	if len(endpoints) == 0 {
		return localRegistrationPayload{}, "", 0, fmt.Errorf("at least one endpoint is required")
	}

	convertedEndpoints := make([]endpointPayload, 0, len(endpoints))
	for index, endpoint := range endpoints {
		if endpoint == nil {
			continue
		}
		host := firstNonBlank(endpoint.Host(), "127.0.0.1")
		port := endpoint.Port()
		if port <= 0 {
			return localRegistrationPayload{}, "", 0, fmt.Errorf("endpoints[%d].port must be positive", index)
		}
		convertedEndpoints = append(convertedEndpoints, endpointPayload{
			Protocol:   protocolHint,
			ListenHost: host,
			ListenPort: port,
			TargetHost: host,
			TargetPort: port,
			Status:     "active",
		})
	}
	if len(convertedEndpoints) == 0 {
		return localRegistrationPayload{}, "", 0, fmt.Errorf("at least one endpoint is required")
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

	payload := localRegistrationPayload{
		ServiceName: serviceName,
		Env:         env,
		InstanceID:  instanceID,
		TTLSeconds:  ttlSeconds,
		Metadata:    metadataAsString,
		Endpoints:   convertedEndpoints,
	}
	return payload, buildLeaseKey(service), ttlSeconds, nil
}

func (a *AgentAdapter) mapServiceFromRegistrationPayload(payload localRegistrationPayload, source Service) Service {
	metadata := metadataFromStringMap(payload.Metadata)
	metadata["env"] = firstNonBlank(metadataString(metadata, "env"), strings.TrimSpace(payload.Env), "base")
	metadata["instanceId"] = firstNonBlank(metadataString(metadata, "instanceId"), strings.TrimSpace(payload.InstanceID))
	version := firstNonBlank(metadataString(metadata, "version"), versionFromService(source), "latest")
	metadata["version"] = version

	endpoints := make(Endpoints, 0, len(payload.Endpoints))
	for _, endpoint := range payload.Endpoints {
		host := firstNonBlank(strings.TrimSpace(endpoint.TargetHost), strings.TrimSpace(endpoint.ListenHost), "127.0.0.1")
		port := endpoint.TargetPort
		if port <= 0 {
			port = endpoint.ListenPort
		}
		if port <= 0 {
			continue
		}
		endpoints = append(endpoints, serviceregistry.NewBasicEndpoint(host, port))
	}

	key := strings.TrimSpace(metadataString(metadata, "serviceKey"))
	if key == "" {
		key = keyFromService(source)
	}
	prefix := strings.TrimSpace(metadataString(metadata, "servicePrefix"))
	if prefix == "" {
		prefix = prefixFromService(source)
	}

	return serviceregistry.NewBasicService(
		payload.ServiceName,
		serviceregistry.WithServiceVersion(version),
		serviceregistry.WithServiceMetadata(metadata),
		serviceregistry.WithServiceEndpoints(endpoints),
		serviceregistry.WithServiceKey(key),
		serviceregistry.WithServicePrefix(prefix),
	)
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

func matchSearchInput(service Service, in SearchInput) bool {
	if service == nil {
		return false
	}
	if name := strings.TrimSpace(in.Name); name != "" && !strings.EqualFold(name, service.GetName()) {
		return false
	}
	if version := strings.TrimSpace(in.Version); version != "" && !strings.EqualFold(version, service.GetVersion()) {
		return false
	}
	if prefix := strings.TrimSpace(in.Prefix); prefix != "" && !strings.HasPrefix(service.GetKey(), prefix) {
		return false
	}
	if !containsMetadata(service.GetMetadata(), in.Metadata) {
		return false
	}
	return true
}

func containsMetadata(container Metadata, filter Metadata) bool {
	if len(filter) == 0 {
		return true
	}
	if len(container) == 0 {
		return false
	}
	for key, expected := range filter {
		actual, exists := container[key]
		if !exists {
			return false
		}
		if strings.TrimSpace(fmt.Sprintf("%v", actual)) != strings.TrimSpace(fmt.Sprintf("%v", expected)) {
			return false
		}
	}
	return true
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

func heartbeatInterval(ttlSeconds int) time.Duration {
	ttl := ttlSeconds
	if ttl <= 0 {
		ttl = defaultLeaseTTLSecond
	}
	interval := time.Duration(ttl/3) * time.Second
	if interval < 3*time.Second {
		return 3 * time.Second
	}
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func buildLeaseKey(service Service) string {
	if service == nil {
		return ""
	}
	metadata := service.GetMetadata()
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(service.GetName())),
		strings.ToLower(strings.TrimSpace(service.GetVersion())),
		strings.ToLower(strings.TrimSpace(metadataString(metadata, "env"))),
	}, "|")
}

func keyFromService(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetKey())
}

func prefixFromService(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetPrefix())
}

func versionFromService(service Service) string {
	if service == nil {
		return ""
	}
	return strings.TrimSpace(service.GetVersion())
}

func normalizeMetadata(metadata Metadata) Metadata {
	if len(metadata) == 0 {
		return Metadata{}
	}
	normalized := make(Metadata, len(metadata))
	for key, value := range metadata {
		normalized[strings.TrimSpace(key)] = value
	}
	return normalized
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

func metadataString(metadata Metadata, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, exists := metadata[key]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func metadataInt(metadata Metadata, key string) int {
	value := metadataString(metadata, key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
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

func minDuration(left, right time.Duration) time.Duration {
	if left <= right {
		return left
	}
	return right
}
