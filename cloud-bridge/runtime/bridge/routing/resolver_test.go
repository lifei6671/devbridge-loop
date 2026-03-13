package routing

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestMatcherMatchIngressIsolation 验证三类入口路由不会互相串扰。
func TestMatcherMatchIngressIsolation(testingObject *testing.T) {
	testingObject.Parallel()
	matcher := NewMatcher()
	routes := []pb.Route{
		{
			RouteID: "route-l7",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol:   "http",
				Host:       "api.dev.example.com",
				PathPrefix: "/v1",
			},
		},
		{
			RouteID: "route-sni",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeTLSSNIShared),
			},
			Match: pb.RouteMatch{
				SNI: "secure.dev.example.com",
			},
		},
		{
			RouteID: "route-l4",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL4DedicatedPort),
			},
			Match: pb.RouteMatch{
				Protocol:   "tcp",
				ListenPort: 18081,
			},
		},
	}
	testCases := []struct {
		name        string
		request     ingress.RouteLookupRequest
		wantRouteID string
	}{
		{
			name: "l7_request_hits_l7_route",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Path:        "/v1/order",
			},
			wantRouteID: "route-l7",
		},
		{
			name: "tls_sni_request_hits_sni_route",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeTLSSNIShared,
				SNI:         "secure.dev.example.com",
			},
			wantRouteID: "route-sni",
		},
		{
			name: "l4_request_hits_dedicated_route",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL4DedicatedPort,
				Protocol:    "tcp",
				ListenPort:  18081,
			},
			wantRouteID: "route-l4",
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			matchedRoutes := matcher.Match(testCase.request, routes)
			if len(matchedRoutes) != 1 {
				testingObject.Fatalf("unexpected match size: got=%d want=1", len(matchedRoutes))
			}
			if matchedRoutes[0].RouteID != testCase.wantRouteID {
				testingObject.Fatalf("unexpected route matched: got=%s want=%s", matchedRoutes[0].RouteID, testCase.wantRouteID)
			}
		})
	}
}

// TestResolverResolveTargetKinds 验证 resolver 可输出三类 target 分类结果。
func TestResolverResolveTargetKinds(testingObject *testing.T) {
	testingObject.Parallel()
	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-1",
		ServiceKey:   "dev/alice/order-service",
		Namespace:    "dev",
		Environment:  "alice",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-connector",
		Namespace:   "dev",
		Environment: "alice",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "api.dev.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/alice/order-service",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-external",
		Namespace:   "dev",
		Environment: "alice",
		Match: pb.RouteMatch{
			SNI: "pay.dev.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "alice",
				ServiceName: "pay",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeTLSSNIShared),
		},
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-hybrid",
		Namespace:   "dev",
		Environment: "alice",
		Match: pb.RouteMatch{
			Protocol:   "tcp",
			ListenPort: 18081,
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeHybridGroup,
			HybridGroup: &pb.HybridGroupTarget{
				PrimaryConnectorService: pb.ConnectorServiceTarget{
					ServiceKey: "dev/alice/order-service",
				},
				FallbackExternalService: pb.ExternalServiceTarget{
					Namespace:   "dev",
					Environment: "alice",
					ServiceName: "pay-fallback",
				},
				FallbackPolicy: pb.FallbackPolicyPreOpenOnly,
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL4DedicatedPort),
		},
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	testCases := []struct {
		name       string
		request    ingress.RouteLookupRequest
		targetKind pb.RouteTargetType
	}{
		{
			name: "connector_service",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Authority:   "api.dev.example.com",
				Namespace:   "dev",
				Environment: "alice",
			},
			targetKind: pb.RouteTargetTypeConnectorService,
		},
		{
			name: "external_service",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeTLSSNIShared,
				Protocol:    "tls",
				SNI:         "pay.dev.example.com",
				Namespace:   "dev",
				Environment: "alice",
			},
			targetKind: pb.RouteTargetTypeExternalService,
		},
		{
			name: "hybrid_group",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL4DedicatedPort,
				Protocol:    "tcp",
				ListenPort:  18081,
				Namespace:   "dev",
				Environment: "alice",
			},
			targetKind: pb.RouteTargetTypeHybridGroup,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			result, err := resolver.Resolve(testCase.request)
			if err != nil {
				testingObject.Fatalf("resolve failed: %v", err)
			}
			if result.TargetKind != testCase.targetKind {
				testingObject.Fatalf("unexpected target kind: got=%s want=%s", result.TargetKind, testCase.targetKind)
			}
		})
	}
}

// TestResolverFilters 验证 A5 定义的过滤规则。
func TestResolverFilters(testingObject *testing.T) {
	testingObject.Parallel()
	now := time.Now().UTC()
	testCases := []struct {
		name              string
		request           ingress.RouteLookupRequest
		serviceHealth     pb.HealthStatus
		sessionState      registry.SessionState
		registerConnector bool
		wantErrorCode     string
	}{
		{
			name: "scope_mismatch",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Namespace:   "prod",
				Environment: "alice",
			},
			serviceHealth:     pb.HealthStatusHealthy,
			sessionState:      registry.SessionActive,
			registerConnector: true,
			wantErrorCode:     ltfperrors.CodeInvalidScope,
		},
		{
			name: "service_unhealthy",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Namespace:   "dev",
				Environment: "alice",
			},
			serviceHealth:     pb.HealthStatusUnhealthy,
			sessionState:      registry.SessionActive,
			registerConnector: true,
			wantErrorCode:     ltfperrors.CodeResolveServiceUnavailable,
		},
		{
			name: "connector_offline",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Namespace:   "dev",
				Environment: "alice",
			},
			serviceHealth:     pb.HealthStatusHealthy,
			sessionState:      registry.SessionActive,
			registerConnector: false,
			wantErrorCode:     ltfperrors.CodeResolveServiceUnavailable,
		},
		{
			name: "session_not_active",
			request: ingress.RouteLookupRequest{
				IngressMode: pb.IngressModeL7Shared,
				Protocol:    "http",
				Host:        "api.dev.example.com",
				Namespace:   "dev",
				Environment: "alice",
			},
			serviceHealth:     pb.HealthStatusHealthy,
			sessionState:      registry.SessionDraining,
			registerConnector: true,
			wantErrorCode:     ltfperrors.CodeResolveSessionNotActive,
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			routeRegistry := registry.NewRouteRegistry()
			serviceRegistry := registry.NewServiceRegistry()
			sessionRegistry := registry.NewSessionRegistry()
			routeRegistry.Upsert(now, pb.Route{
				RouteID:     "route-1",
				Namespace:   "dev",
				Environment: "alice",
				Match: pb.RouteMatch{
					Protocol: "http",
					Host:     "api.dev.example.com",
				},
				Target: pb.RouteTarget{
					Type: pb.RouteTargetTypeConnectorService,
					ConnectorService: &pb.ConnectorServiceTarget{
						ServiceKey: "dev/alice/order-service",
					},
				},
				Metadata: map[string]string{
					routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
				},
			})
			serviceRegistry.Upsert(now, pb.Service{
				ServiceID:    "svc-1",
				ServiceKey:   "dev/alice/order-service",
				Namespace:    "dev",
				Environment:  "alice",
				ConnectorID:  "connector-1",
				Status:       pb.ServiceStatusActive,
				HealthStatus: testCase.serviceHealth,
			})
			if testCase.registerConnector {
				sessionRegistry.Upsert(now, registry.SessionRuntime{
					SessionID:   "session-1",
					ConnectorID: "connector-1",
					Epoch:       1,
					State:       testCase.sessionState,
				})
			}
			resolver := NewResolver(ResolverOptions{
				RouteRegistry:   routeRegistry,
				ServiceRegistry: serviceRegistry,
				SessionRegistry: sessionRegistry,
			})
			if _, err := resolver.Resolve(testCase.request); err == nil {
				testingObject.Fatalf("expected resolve filtered with code=%s", testCase.wantErrorCode)
			} else if ltfperrors.ExtractCode(err) != testCase.wantErrorCode {
				testingObject.Fatalf(
					"unexpected error code: got=%s want=%s err=%v",
					ltfperrors.ExtractCode(err),
					testCase.wantErrorCode,
					err,
				)
			}
		})
	}
}
