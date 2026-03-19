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
	canonical.UpsertLogicalService(pb.LogicalService{
		LogicalServiceID: "ls-001",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
	})
	canonical.UpsertServiceInstance(pb.ServiceInstance{
		InstanceID:       "si-001",
		LogicalServiceID: "ls-001",
		ConnectorID:      "conn-001",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})

	result, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID:         "route-001",
			Scope:           pb.Scope{Namespace: "dev", Environment: "alice"},
			ResourceVersion: 3,
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeConnectorService,
				ConnectorService: &pb.ConnectorServiceTarget{
					Selector: pb.ServiceSelector{
						ServiceName: "order-service",
						Scope:       pb.Scope{Namespace: "dev", Environment: "alice"},
					},
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
	if result.TrafficOpen.LogicalServiceID != "ls-001" || result.TrafficOpen.InstanceID != "si-001" || result.TrafficOpen.RouteID != "route-001" || result.TrafficOpen.TraceID != "trace-001" {
		t.Fatalf("unexpected traffic open: %+v", result.TrafficOpen)
	}
}

// TestResolveConnectorRejectScopeMismatch 验证 scope 不匹配会拒绝解析。
func TestResolveConnectorRejectScopeMismatch(t *testing.T) {
	t.Parallel()

	resolver, canonical := buildResolverWithRegistry()
	canonical.UpsertLogicalService(pb.LogicalService{
		LogicalServiceID: "ls-001",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "prod"},
		Status:           pb.ServiceStatusActive,
	})

	_, err := resolver.Resolve(context.Background(), ResolveRequest{
		Route: pb.Route{
			RouteID: "route-001",
			Scope:   pb.Scope{Namespace: "dev", Environment: "alice"},
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeConnectorService,
				ConnectorService: &pb.ConnectorServiceTarget{
					Selector: pb.ServiceSelector{
						ServiceName: "order-service",
						Scope:       pb.Scope{Namespace: "dev", Environment: "prod"},
					},
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
			RouteID: "route-002",
			Scope:   pb.Scope{Namespace: "dev", Environment: "alice"},
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
	if result.ExternalQuery.Namespace != "dev" || result.ExternalQuery.Environment != "alice" {
		t.Fatalf("unexpected query scope: %+v", result.ExternalQuery)
	}
}
