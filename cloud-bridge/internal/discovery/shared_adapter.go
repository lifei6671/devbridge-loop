package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

	serviceregistry "github.com/lifei6671/devbridge-loop/service-registry"
)

const (
	defaultSharedWatchEvery = 2 * time.Second
)

// SharedDiscoveryAdapter 将 bridge resolver 适配到共享 Discovery 抽象。
type SharedDiscoveryAdapter struct {
	resolver        Resolver
	defaultEnv      string
	defaultProtocol string
	watchEvery      time.Duration
}

// NewSharedDiscoveryAdapter 创建 Discovery 共享适配器。
func NewSharedDiscoveryAdapter(resolver Resolver, defaultEnv string, defaultProtocol string, watchEvery time.Duration) *SharedDiscoveryAdapter {
	interval := watchEvery
	if interval <= 0 {
		interval = defaultSharedWatchEvery
	}
	protocol := strings.TrimSpace(defaultProtocol)
	if protocol == "" {
		protocol = "http"
	}
	env := strings.TrimSpace(defaultEnv)
	if env == "" {
		env = "base"
	}

	return &SharedDiscoveryAdapter{
		resolver:        resolver,
		defaultEnv:      env,
		defaultProtocol: protocol,
		watchEvery:      interval,
	}
}

// Search 通过 bridge resolver 返回 Service 结果。
func (a *SharedDiscoveryAdapter) Search(ctx context.Context, input serviceregistry.SearchInput) ([]serviceregistry.Service, error) {
	if a == nil || a.resolver == nil {
		return []serviceregistry.Service{}, nil
	}
	serviceName := strings.TrimSpace(input.Name)
	if serviceName == "" {
		// bridge 当前 resolver 需要明确服务名；空名称场景直接返回空集。
		return []serviceregistry.Service{}, nil
	}

	metadata := normalizeSharedMetadata(input.Metadata)
	env := firstNonBlank(metadataString(metadata, "env"), a.defaultEnv, "base")
	protocol := firstNonBlank(metadataString(metadata, "protocol"), a.defaultProtocol, "http")

	endpoint, matched, err := a.resolver.Resolve(ctx, Query{
		Env:         env,
		ServiceName: serviceName,
		Protocol:    protocol,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve service failed: %w", err)
	}
	if !matched {
		return []serviceregistry.Service{}, nil
	}

	resultMetadata := normalizeSharedMetadata(metadata)
	resultMetadata["env"] = env
	resultMetadata["protocol"] = protocol
	resultMetadata["source"] = firstNonBlank(strings.TrimSpace(endpoint.Source), a.resolver.Name())
	for key, value := range endpoint.Metadata {
		resultMetadata[key] = value
	}

	version := firstNonBlank(strings.TrimSpace(input.Version), metadataString(resultMetadata, "version"), "latest")
	resultMetadata["version"] = version

	service := serviceregistry.NewBasicService(
		serviceName,
		serviceregistry.WithServiceVersion(version),
		serviceregistry.WithServiceMetadata(resultMetadata),
		serviceregistry.WithServiceEndpoints(serviceregistry.Endpoints{
			serviceregistry.NewBasicEndpoint(endpoint.Host, endpoint.Port),
		}),
	)
	if !matchesSearchFilters(service, input) {
		return []serviceregistry.Service{}, nil
	}
	return []serviceregistry.Service{service}, nil
}

// Watch 通过轮询 Search 实现服务变更监听。
func (a *SharedDiscoveryAdapter) Watch(ctx context.Context, key string) (serviceregistry.Watcher, error) {
	return serviceregistry.NewPollingWatcher(ctx, key, a.watchEvery, func(searchCtx context.Context, input serviceregistry.SearchInput) ([]serviceregistry.Service, error) {
		return a.Search(searchCtx, input)
	})
}

func normalizeSharedMetadata(metadata serviceregistry.Metadata) serviceregistry.Metadata {
	if len(metadata) == 0 {
		return serviceregistry.Metadata{}
	}
	copied := make(serviceregistry.Metadata, len(metadata))
	for key, value := range metadata {
		copied[strings.TrimSpace(key)] = value
	}
	return copied
}

func metadataString(metadata serviceregistry.Metadata, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, exists := metadata[key]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func matchesSearchFilters(service serviceregistry.Service, input serviceregistry.SearchInput) bool {
	if service == nil {
		return false
	}
	if prefix := strings.TrimSpace(input.Prefix); prefix != "" && !strings.HasPrefix(service.GetKey(), prefix) {
		return false
	}
	if name := strings.TrimSpace(input.Name); name != "" && !strings.EqualFold(name, service.GetName()) {
		return false
	}
	if version := strings.TrimSpace(input.Version); version != "" && !strings.EqualFold(version, service.GetVersion()) {
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
