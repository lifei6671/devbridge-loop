package discovery

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestEndpointSelectorRoundRobin 验证轮询策略按顺序选择 endpoint。
func TestEndpointSelectorRoundRobin(t *testing.T) {
	t.Parallel()

	selector := NewEndpointSelector(1)
	endpoints := []Endpoint{
		{Host: "10.0.0.1", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
		{Host: "10.0.0.2", Port: 8080, HealthStatus: pb.HealthStatusUnknown},
	}

	first, err := selector.Select(SelectStrategyRoundRobin, endpoints)
	if err != nil {
		t.Fatalf("select first failed: %v", err)
	}
	second, err := selector.Select(SelectStrategyRoundRobin, endpoints)
	if err != nil {
		t.Fatalf("select second failed: %v", err)
	}
	if first.Host != "10.0.0.1" || second.Host != "10.0.0.2" {
		t.Fatalf("unexpected round robin order: first=%s second=%s", first.Host, second.Host)
	}
}

// TestEndpointSelectorFilterUnhealthy 验证 unhealthy 与非法 endpoint 会被过滤。
func TestEndpointSelectorFilterUnhealthy(t *testing.T) {
	t.Parallel()

	selector := NewEndpointSelector(2)
	_, err := selector.Select(SelectStrategyRandom, []Endpoint{
		{Host: "10.0.0.1", Port: 0, HealthStatus: pb.HealthStatusHealthy},
		{Host: "", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
		{Host: "10.0.0.2", Port: 8080, HealthStatus: pb.HealthStatusUnhealthy},
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeDiscoveryNoEndpoint) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEndpointSelectorUnsupportedStrategy 验证未知选路策略会返回错误。
func TestEndpointSelectorUnsupportedStrategy(t *testing.T) {
	t.Parallel()

	selector := NewEndpointSelector(3)
	_, err := selector.Select("unknown", []Endpoint{
		{Host: "10.0.0.1", Port: 8080, HealthStatus: pb.HealthStatusHealthy},
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedValue) {
		t.Fatalf("unexpected error: %v", err)
	}
}
