package discovery

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type fakeProvider struct {
	result []Endpoint
	err    error
}

// Query 返回预设结果，用于测试 manager 行为。
func (provider fakeProvider) Query(ctx context.Context, request QueryRequest) ([]Endpoint, error) {
	return provider.result, provider.err
}

// TestQueryCacheFirst 验证 cache_first 模式行为。
func TestQueryCacheFirst(t *testing.T) {
	t.Parallel()

	manager := NewManager(SecurityPolicy{
		ProviderAllowlist: map[string]struct{}{"nacos": {}},
	})
	manager.RegisterProvider("nacos", fakeProvider{
		result: []Endpoint{
			{Host: "10.0.0.2", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
		},
	})

	request := QueryRequest{
		Provider:        "nacos",
		Namespace:       "dev",
		Environment:     "alice",
		ServiceName:     "order-service",
		CacheTTL:        5 * time.Second,
		StaleIfErrorTTL: 10 * time.Second,
	}
	endpoints, err := manager.Query(context.Background(), QueryModeCacheFirst, request)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("unexpected endpoints length: %d", len(endpoints))
	}
}

// TestQueryStaleIfError 验证 stale_if_error 在 provider 失败时返回缓存。
func TestQueryStaleIfError(t *testing.T) {
	t.Parallel()

	manager := NewManager(SecurityPolicy{
		ProviderAllowlist: map[string]struct{}{"nacos": {}},
	})
	manager.RegisterProvider("nacos", fakeProvider{
		result: []Endpoint{
			{Host: "10.0.0.2", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
		},
	})
	request := QueryRequest{
		Provider:        "nacos",
		Namespace:       "dev",
		Environment:     "alice",
		ServiceName:     "order-service",
		CacheTTL:        2 * time.Second,
		StaleIfErrorTTL: 5 * time.Second,
	}
	_, err := manager.Query(context.Background(), QueryModeCacheFirst, request)
	if err != nil {
		t.Fatalf("warm cache failed: %v", err)
	}

	// 切换 provider 为失败场景，验证 stale_if_error 能回退缓存。
	manager.RegisterProvider("nacos", fakeProvider{err: errors.New("provider down")})
	endpoints, err := manager.Query(context.Background(), QueryModeStaleIfError, request)
	if err != nil {
		t.Fatalf("stale_if_error query failed: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("unexpected stale endpoints length: %d", len(endpoints))
	}
}

// TestQueryRejectDeniedEndpoint 验证 endpoint CIDR 拒绝策略。
func TestQueryRejectDeniedEndpoint(t *testing.T) {
	t.Parallel()

	denyPrefix := netip.MustParsePrefix("10.0.0.0/8")
	manager := NewManager(SecurityPolicy{
		ProviderAllowlist: map[string]struct{}{"nacos": {}},
		EndpointDenyCIDRs: []netip.Prefix{denyPrefix},
	})
	manager.RegisterProvider("nacos", fakeProvider{
		result: []Endpoint{
			{Host: "10.0.0.2", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
		},
	})
	_, err := manager.Query(context.Background(), QueryModeCacheFirst, QueryRequest{
		Provider:    "nacos",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 被 CIDR 策略拒绝时应返回 endpoint denied 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeDiscoveryEndpointDenied) {
		t.Fatalf("unexpected error: %v", err)
	}
}
