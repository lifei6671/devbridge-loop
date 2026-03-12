package routing

import (
	"context"
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/registry"
)

// buildResolverWithRegistry 构建带测试数据的 resolver 与 registry。
func buildResolverWithRegistry() (*Resolver, *registry.CanonicalRegistry) {
	canonical := registry.NewCanonicalRegistry()
	resolver := NewResolver(canonical)
	return resolver, canonical
}

// TestResolveConnectorSuccess 验证 connector_service 解析成功并生成 TrafficOpen。
func TestResolveConnectorSuccess(t *testing.T) {
	t.Parallel()

	resolver, canonical := buildResolverWithRegistry()
	canonical.UpsertConnector(pb.Connector{
		ConnectorID: "conn-001",
		Status:      "online",
	})
	canonical.UpsertSession(pb.Session{
		SessionID:    "sess-001",
		ConnectorID:  "conn-001",
		SessionEpoch: 7,
		State:        pb.SessionStateActive,
	})
	canonical.UpsertService(pb.Service{
		ServiceID:    "svc-001",
		ServiceKey:   "dev/alice/order-service",
		Namespace:    "dev",
		Environment:  "alice",
		ConnectorID:  "conn-001",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})

	result, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:         "route-001",
			Namespace:       "dev",
			Environment:     "alice",
			ResourceVersion: 3,
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeConnectorService,
				ConnectorService: &pb.ConnectorServiceTarget{
					ServiceKey: "dev/alice/order-service",
				},
			},
		},
		TrafficID: "traffic-001",
		TraceID:   "trace-001",
		EndpointSelectionHint: map[string]string{
			"zone": "cn-sh",
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if result.PathKind != pb.RouteTargetTypeConnectorService {
		t.Fatalf("unexpected path kind: %s", result.PathKind)
	}
	if result.TrafficOpen == nil {
		t.Fatalf("expected traffic open")
	}
	// TrafficOpen 不应携带权威 target_addr，仅保留 service 级信息与 hint。
	if result.TrafficOpen.ServiceID != "svc-001" || result.TrafficOpen.RouteID != "route-001" || result.TrafficOpen.TraceID != "trace-001" {
		t.Fatalf("unexpected traffic open: %+v", result.TrafficOpen)
	}
}

// TestResolveConnectorRejectScopeMismatch 验证 scope 不匹配会拒绝解析。
func TestResolveConnectorRejectScopeMismatch(t *testing.T) {
	t.Parallel()

	resolver, canonical := buildResolverWithRegistry()
	canonical.UpsertService(pb.Service{
		ServiceID:    "svc-001",
		ServiceKey:   "dev/prod/order-service",
		Namespace:    "dev",
		Environment:  "prod",
		ConnectorID:  "conn-001",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})

	_, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:     "route-001",
			Namespace:   "dev",
			Environment: "alice",
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeConnectorService,
				ConnectorService: &pb.ConnectorServiceTarget{
					ServiceKey: "dev/prod/order-service",
				},
			},
		},
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidScope) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveExternalSuccess 验证 external_service 解析输出 discovery 查询参数。
func TestResolveExternalSuccess(t *testing.T) {
	t.Parallel()

	resolver, _ := buildResolverWithRegistry()
	result, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:     "route-002",
			Namespace:   "dev",
			Environment: "alice",
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeExternalService,
				ExternalService: &pb.ExternalServiceTarget{
					Provider:    "nacos",
					ServiceName: "payment",
					Group:       "DEFAULT_GROUP",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if result.PathKind != pb.RouteTargetTypeExternalService {
		t.Fatalf("unexpected path kind: %s", result.PathKind)
	}
	if result.ExternalQuery == nil {
		t.Fatalf("expected external query")
	}
	// target scope 未设置时应继承 route scope。
	if result.ExternalQuery.Namespace != "dev" || result.ExternalQuery.Environment != "alice" {
		t.Fatalf("unexpected query scope: %+v", result.ExternalQuery)
	}
}

// TestResolveHybridFallbackOnResolveMiss 验证 hybrid 在 pre-open resolve miss 时回退 external。
func TestResolveHybridFallbackOnResolveMiss(t *testing.T) {
	t.Parallel()

	resolver, _ := buildResolverWithRegistry()
	result, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:     "route-003",
			Namespace:   "dev",
			Environment: "alice",
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeHybridGroup,
				HybridGroup: &pb.HybridGroupTarget{
					PrimaryConnectorService: pb.ConnectorServiceTarget{
						ServiceKey: "dev/alice/missing-service",
					},
					FallbackExternalService: pb.ExternalServiceTarget{
						Provider:    "nacos",
						ServiceName: "fallback-payment",
					},
					FallbackPolicy: pb.FallbackPolicyPreOpenOnly,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !result.FallbackUsed || result.PathKind != pb.RouteTargetTypeExternalService {
		t.Fatalf("unexpected fallback result: %+v", result)
	}
	if result.FallbackReason != "resolve_miss" || result.PrimaryErrorCode != ltfperrors.CodeResolveServiceNotFound {
		t.Fatalf("unexpected fallback metadata: %+v", result)
	}
}

// TestResolveHybridRejectUnsupportedPolicy 验证 hybrid 非 pre_open_only 策略会拒绝。
func TestResolveHybridRejectUnsupportedPolicy(t *testing.T) {
	t.Parallel()

	resolver, _ := buildResolverWithRegistry()
	_, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:     "route-004",
			Namespace:   "dev",
			Environment: "alice",
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeHybridGroup,
				HybridGroup: &pb.HybridGroupTarget{
					PrimaryConnectorService: pb.ConnectorServiceTarget{
						ServiceKey: "dev/alice/missing-service",
					},
					FallbackExternalService: pb.ExternalServiceTarget{
						Provider:    "nacos",
						ServiceName: "fallback-payment",
					},
					FallbackPolicy: "",
				},
			},
		},
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeResolveServiceNotFound) {
		// policy 不合法时 resolver 不应静默 fallback，应该保留 primary 失败信号。
		t.Fatalf("unexpected error: %v", err)
	}
}
