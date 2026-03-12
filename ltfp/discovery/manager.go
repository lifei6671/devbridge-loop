package discovery

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// QueryMode 定义 discovery 查询模式。
type QueryMode string

const (
	// QueryModeCacheFirst 表示优先使用缓存，缓存 miss 再查询 provider。
	QueryModeCacheFirst QueryMode = "cache_first"
	// QueryModeRefreshOnMiss 表示仅 miss 时刷新缓存。
	QueryModeRefreshOnMiss QueryMode = "refresh_on_miss"
	// QueryModeStaleIfError 表示 provider 查询失败时允许返回过期缓存。
	QueryModeStaleIfError QueryMode = "stale_if_error"
)

// Endpoint 描述 discovery 返回的 endpoint 结果。
type Endpoint struct {
	Host         string            `json:"host"`
	Port         uint32            `json:"port"`
	HealthStatus pb.HealthStatus   `json:"healthStatus"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// String 返回 endpoint 的 host:port 字符串形式。
func (endpoint Endpoint) String() string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(endpoint.Host), endpoint.Port)
}

// QueryRequest 描述 discovery 查询输入。
type QueryRequest struct {
	Provider        string
	Namespace       string
	Environment     string
	ServiceName     string
	Group           string
	CacheTTL        time.Duration
	StaleIfErrorTTL time.Duration
}

// Provider 定义 discovery provider 查询接口。
type Provider interface {
	// Query 查询服务 endpoint 列表，阻塞操作必须响应 context 取消。
	Query(ctx context.Context, request QueryRequest) ([]Endpoint, error)
}

type cacheEntry struct {
	endpoints  []Endpoint
	expiresAt  time.Time
	staleUntil time.Time
}

// SecurityPolicy 描述 discovery 查询安全约束。
type SecurityPolicy struct {
	ProviderAllowlist  map[string]struct{}
	NamespaceAllowlist map[string]struct{}
	ServiceAllowlist   map[string]struct{}
	EndpointAllowCIDRs []netip.Prefix
	EndpointDenyCIDRs  []netip.Prefix
	MaxConcurrent      int
}

// Manager 管理 provider、缓存与安全策略。
type Manager struct {
	mu        sync.RWMutex
	providers map[string]Provider
	cache     map[string]cacheEntry
	policy    SecurityPolicy
	semaphore chan struct{}
	nowFunc   func() time.Time
}

// NewManager 创建 discovery manager。
func NewManager(policy SecurityPolicy) *Manager {
	maxConcurrent := policy.MaxConcurrent
	if maxConcurrent <= 0 {
		// 并发限制未配置时回退到 1，防止无限并发。
		maxConcurrent = 1
	}
	return &Manager{
		providers: make(map[string]Provider),
		cache:     make(map[string]cacheEntry),
		policy:    policy,
		semaphore: make(chan struct{}, maxConcurrent),
		nowFunc:   time.Now,
	}
}

// RegisterProvider 注册 discovery provider。
func (manager *Manager) RegisterProvider(name string, provider Provider) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	// provider 名称归一化后作为主键存储。
	manager.providers[strings.TrimSpace(name)] = provider
}

// Query 按指定模式执行 discovery 查询。
func (manager *Manager) Query(ctx context.Context, mode QueryMode, request QueryRequest) ([]Endpoint, error) {
	if err := manager.validateSecurity(request); err != nil {
		return nil, err
	}

	cacheKey := manager.buildCacheKey(request)
	now := manager.nowFunc().UTC()

	if mode == QueryModeCacheFirst || mode == QueryModeRefreshOnMiss || mode == QueryModeStaleIfError {
		if endpoints, ok := manager.loadFreshCache(cacheKey, now); ok {
			// 缓存有效时直接返回，减少 provider 压力。
			return endpoints, nil
		}
	}

	endpoints, err := manager.queryProvider(ctx, request)
	if err == nil {
		filtered, filterErr := manager.filterEndpoints(endpoints)
		if filterErr != nil {
			return nil, filterErr
		}
		if len(filtered) == 0 {
			return nil, ltfperrors.New(ltfperrors.CodeDiscoveryNoEndpoint, "discovery returned no usable endpoint")
		}
		manager.storeCache(cacheKey, filtered, request.CacheTTL, request.StaleIfErrorTTL, now)
		return filtered, nil
	}

	// stale_if_error 模式允许返回过期缓存，作为兜底路径。
	if mode == QueryModeStaleIfError {
		if endpoints, ok := manager.loadStaleCache(cacheKey, now); ok {
			return endpoints, nil
		}
	}
	return nil, ltfperrors.Wrap(ltfperrors.CodeDiscoveryProviderUnavailable, "discovery provider query failed", err)
}

// SetNowFunc 设置当前时间函数，仅用于测试。
func (manager *Manager) SetNowFunc(nowFunc func() time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	// 测试中注入固定时间以控制缓存过期行为。
	manager.nowFunc = nowFunc
}

func (manager *Manager) validateSecurity(request QueryRequest) error {
	normalizedProvider := strings.TrimSpace(request.Provider)
	normalizedNamespace := strings.TrimSpace(request.Namespace)
	normalizedService := strings.TrimSpace(request.ServiceName)

	if len(manager.policy.ProviderAllowlist) > 0 {
		if _, exists := manager.policy.ProviderAllowlist[normalizedProvider]; !exists {
			return ltfperrors.New(ltfperrors.CodeDiscoveryProviderNotAllowed, "provider is not in allowlist")
		}
	}
	if len(manager.policy.NamespaceAllowlist) > 0 {
		if _, exists := manager.policy.NamespaceAllowlist[normalizedNamespace]; !exists {
			return ltfperrors.New(ltfperrors.CodeDiscoveryNamespaceNotAllowed, "namespace is not in allowlist")
		}
	}
	if len(manager.policy.ServiceAllowlist) > 0 {
		if _, exists := manager.policy.ServiceAllowlist[normalizedService]; !exists {
			return ltfperrors.New(ltfperrors.CodeDiscoveryServiceNotAllowed, "serviceName is not in allowlist")
		}
	}
	return nil
}

func (manager *Manager) queryProvider(ctx context.Context, request QueryRequest) ([]Endpoint, error) {
	manager.mu.RLock()
	provider, exists := manager.providers[strings.TrimSpace(request.Provider)]
	manager.mu.RUnlock()
	if !exists {
		return nil, ltfperrors.New(ltfperrors.CodeDiscoveryProviderUnavailable, "provider is not registered")
	}

	// 通过信号量限制并发查询数量。
	select {
	case manager.semaphore <- struct{}{}:
		defer func() { <-manager.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return provider.Query(ctx, request)
}

func (manager *Manager) filterEndpoints(endpoints []Endpoint) ([]Endpoint, error) {
	filtered := make([]Endpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.Port == 0 || strings.TrimSpace(endpoint.Host) == "" {
			continue
		}
		if endpoint.HealthStatus == pb.HealthStatusUnhealthy {
			// 不健康 endpoint 直接过滤。
			continue
		}
		allowed, err := manager.isEndpointAllowed(endpoint.Host)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		filtered = append(filtered, endpoint)
	}
	return filtered, nil
}

func (manager *Manager) isEndpointAllowed(host string) (bool, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		// 非 IP host（例如域名）默认放行，由上层 DNS 解析后再做网络策略。
		return true, nil
	}
	for _, deny := range manager.policy.EndpointDenyCIDRs {
		if deny.Contains(ip) {
			return false, ltfperrors.New(ltfperrors.CodeDiscoveryEndpointDenied, "endpoint host is denied by cidr policy")
		}
	}
	if len(manager.policy.EndpointAllowCIDRs) == 0 {
		return true, nil
	}
	for _, allow := range manager.policy.EndpointAllowCIDRs {
		if allow.Contains(ip) {
			return true, nil
		}
	}
	return false, ltfperrors.New(ltfperrors.CodeDiscoveryEndpointDenied, "endpoint host is not in allow cidr list")
}

func (manager *Manager) buildCacheKey(request QueryRequest) string {
	// 缓存键由 provider + namespace + environment + service + group 组成。
	return strings.Join([]string{
		strings.TrimSpace(request.Provider),
		strings.TrimSpace(request.Namespace),
		strings.TrimSpace(request.Environment),
		strings.TrimSpace(request.ServiceName),
		strings.TrimSpace(request.Group),
	}, "|")
}

func (manager *Manager) loadFreshCache(key string, now time.Time) ([]Endpoint, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	entry, exists := manager.cache[key]
	if !exists {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		return nil, false
	}
	return entry.endpoints, true
}

func (manager *Manager) loadStaleCache(key string, now time.Time) ([]Endpoint, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	entry, exists := manager.cache[key]
	if !exists {
		return nil, false
	}
	if now.After(entry.staleUntil) {
		return nil, false
	}
	return entry.endpoints, true
}

func (manager *Manager) storeCache(key string, endpoints []Endpoint, cacheTTL time.Duration, staleIfErrorTTL time.Duration, now time.Time) {
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Second
	}
	if staleIfErrorTTL <= 0 {
		staleIfErrorTTL = 15 * time.Second
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cache[key] = cacheEntry{
		endpoints:  endpoints,
		expiresAt:  now.Add(cacheTTL),
		staleUntil: now.Add(staleIfErrorTTL),
	}
}
