package ingress

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestMatchRouteL7Shared 验证 l7 维度匹配逻辑。
func TestMatchRouteL7Shared(t *testing.T) {
	t.Parallel()

	matched, reason := MatchRoute(pb.Route{
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/v1",
		},
	}, RequestContext{
		Protocol: "http",
		Host:     "api.dev.example.com",
		Path:     "/v1/orders",
	})
	if !matched || reason != "" {
		t.Fatalf("unexpected match result: matched=%v reason=%s", matched, reason)
	}
}

// TestMatchRouteRejectSNI 验证 sni 不匹配会拒绝。
func TestMatchRouteRejectSNI(t *testing.T) {
	t.Parallel()

	matched, reason := MatchRoute(pb.Route{
		Match: pb.RouteMatch{
			SNI: "order.dev.example.com",
		},
	}, RequestContext{
		SNI: "pay.dev.example.com",
	})
	if matched || reason != "sni_mismatch" {
		t.Fatalf("unexpected match result: matched=%v reason=%s", matched, reason)
	}
}

// TestValidateDedicatedPortAssignments 验证 dedicated port 冲突检测。
func TestValidateDedicatedPortAssignments(t *testing.T) {
	t.Parallel()

	err := ValidateDedicatedPortAssignments([]pb.Service{
		{
			ServiceID: "svc-1",
			Exposure: pb.ServiceExposure{
				IngressMode: pb.IngressModeL4DedicatedPort,
				ListenPort:  18080,
			},
		},
		{
			ServiceID: "svc-2",
			Exposure: pb.ServiceExposure{
				IngressMode: pb.IngressModeL4DedicatedPort,
				ListenPort:  18080,
			},
		},
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeIngressPortConflict) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildRouteMappings 验证 route/service/exposure 映射构建。
func TestBuildRouteMappings(t *testing.T) {
	t.Parallel()

	mappings := BuildRouteMappings(
		[]pb.Route{
			{
				RouteID: "route-1",
				Target: pb.RouteTarget{
					Type: pb.RouteTargetTypeConnectorService,
					ConnectorService: &pb.ConnectorServiceTarget{
						ServiceKey: "dev/alice/order-service",
					},
				},
			},
			{
				RouteID: "route-2",
				Target: pb.RouteTarget{
					Type: pb.RouteTargetTypeExternalService,
					ExternalService: &pb.ExternalServiceTarget{
						ServiceName: "payment",
					},
				},
			},
		},
		[]pb.Service{
			{
				ServiceID:   "svc-1",
				ServiceKey:  "dev/alice/order-service",
				Namespace:   "dev",
				Environment: "alice",
				Exposure: pb.ServiceExposure{
					IngressMode: pb.IngressModeL7Shared,
					Host:        "api.dev.example.com",
				},
			},
		},
	)
	if len(mappings) != 2 {
		t.Fatalf("unexpected mapping count: %d", len(mappings))
	}
	if mappings[0].ServiceID != "svc-1" || mappings[0].IngressMode != pb.IngressModeL7Shared {
		t.Fatalf("unexpected mapping[0]: %+v", mappings[0])
	}
	if mappings[1].TargetType != pb.RouteTargetTypeExternalService || mappings[1].ServiceID != "" {
		t.Fatalf("unexpected mapping[1]: %+v", mappings[1])
	}
}
