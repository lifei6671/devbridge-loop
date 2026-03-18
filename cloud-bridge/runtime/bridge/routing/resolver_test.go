package routing

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// fixedConnectorIDServiceInstanceSelector 是测试用选择器：优先命中指定 connector。
type fixedConnectorIDServiceInstanceSelector struct {
	connectorID string
}

// Select 从候选实例中选择指定 connector 对应实例，未命中则回退首个候选。
func (selector fixedConnectorIDServiceInstanceSelector) Select(candidates []ConnectorResolution) ConnectorResolution {
	if len(candidates) == 0 {
		return ConnectorResolution{}
	}
	for _, candidate := range candidates {
		if candidate.Session.ConnectorID == selector.connectorID {
			return candidate
		}
	}
	return candidates[0]
}

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

// TestMatcherNormalizesHostAndAuthority 验证 Host/Authority 匹配会规整大小写与端口差异。
func TestMatcherNormalizesHostAndAuthority(testingObject *testing.T) {
	testingObject.Parallel()
	matcher := NewMatcher()
	routes := []pb.Route{
		{
			RouteID: "route-host",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol: "http",
				Host:     "api.dev.local",
			},
		},
		{
			RouteID: "route-authority",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol:  "http",
				Authority: "api.dev.local",
			},
		},
		{
			RouteID: "route-authority-port",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol:  "http",
				Authority: "api.dev.local:8081",
			},
		},
	}

	request := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "HTTP",
		Host:        "API.DEV.LOCAL:8080",
		Authority:   "API.DEV.LOCAL:8080",
		Path:        "/v1/orders",
	}
	matchedRoutes := matcher.Match(request, routes)
	if len(matchedRoutes) != 2 {
		testingObject.Fatalf("unexpected normalized match size: got=%d want=2", len(matchedRoutes))
	}
	if matchedRoutes[0].RouteID != "route-authority" {
		testingObject.Fatalf("expected authority route first by score, got=%s", matchedRoutes[0].RouteID)
	}
	if matchedRoutes[1].RouteID != "route-host" {
		testingObject.Fatalf("expected host route second, got=%s", matchedRoutes[1].RouteID)
	}
}

// TestMatcherSupportsHeaderMatches 验证 header_matches 支持名字大小写无关、值精确匹配、全部条件命中。
func TestMatcherSupportsHeaderMatches(testingObject *testing.T) {
	testingObject.Parallel()
	matcher := NewMatcher()
	routes := []pb.Route{
		{
			RouteID: "route-header-strict",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol:   "http",
				Host:       "api.header.example.com",
				PathPrefix: "/v1",
				HeaderMatches: map[string]string{
					"X-Tenant":  "alice",
					"x-release": "2026-03",
				},
			},
		},
	}
	fullyMatchedRequest := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "api.header.example.com",
		Authority:   "api.header.example.com",
		Path:        "/v1/orders",
		Headers: map[string][]string{
			// header 名大小写不同仍应命中。
			"x-tenant":  []string{"alice"},
			"X-Release": []string{"2026-03"},
		},
	}
	matchedRoutes := matcher.Match(fullyMatchedRequest, routes)
	if len(matchedRoutes) != 1 || matchedRoutes[0].RouteID != "route-header-strict" {
		testingObject.Fatalf("expected header-matched route selected, got=%v", matchedRoutes)
	}

	partialMatchedRequest := fullyMatchedRequest
	partialMatchedRequest.Headers = map[string][]string{
		"x-tenant": []string{"alice"},
	}
	if matchedRoutes := matcher.Match(partialMatchedRequest, routes); len(matchedRoutes) != 0 {
		testingObject.Fatalf("all header conditions must match, got=%v", matchedRoutes)
	}

	multiValueRequest := fullyMatchedRequest
	multiValueRequest.Headers = map[string][]string{
		"x-tenant":  []string{"alice"},
		"x-release": []string{"2026-02", "2026-03"},
	}
	if matchedRoutes := matcher.Match(multiValueRequest, routes); len(matchedRoutes) != 1 {
		testingObject.Fatalf("expected multi-value header matched route, got=%v", matchedRoutes)
	}

	valueMismatchRequest := fullyMatchedRequest
	valueMismatchRequest.Headers = map[string][]string{
		"x-tenant":  []string{"alice"},
		"x-release": []string{"2026-03-hotfix"},
	}
	if matchedRoutes := matcher.Match(valueMismatchRequest, routes); len(matchedRoutes) != 0 {
		testingObject.Fatalf("header value must exact match, got=%v", matchedRoutes)
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

// TestResolverRoutesByHeaderMatches 验证同 host/path 下可按 header 条件分流到不同 target service。
func TestResolverRoutesByHeaderMatches(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-header-alpha",
		ServiceKey:   "svc-alpha/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-alpha",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-alpha")
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-header-beta",
		ServiceKey:   "svc-beta/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-beta",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-beta")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-alpha",
		ConnectorID: "connector-alpha",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-beta",
		ConnectorID: "connector-beta",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-header-alpha",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "route-header.example.com",
			HeaderMatches: map[string]string{
				"X-Tenant": "alpha",
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "svc-alpha/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-header-beta",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "route-header.example.com",
			HeaderMatches: map[string]string{
				"x-tenant": "beta",
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "svc-beta/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "route-header.example.com",
		Authority:   "route-header.example.com",
		Path:        "/",
		Namespace:   "dev",
		Environment: "demo",
		Headers: map[string][]string{
			// 名字大小写不同仍应按同一 header 参与匹配。
			"X-TENANT": []string{"beta"},
		},
	})
	if err != nil {
		testingObject.Fatalf("resolve by header route failed: %v", err)
	}
	if result.Route.RouteID != "route-header-beta" {
		testingObject.Fatalf("unexpected routed route_id: got=%s want=route-header-beta", result.Route.RouteID)
	}
	if result.Connector == nil || result.Connector.Service.ServiceKey != "svc-beta/http" {
		testingObject.Fatalf("unexpected routed service: %+v", result.Connector)
	}

	multiValueResult, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "route-header.example.com",
		Authority:   "route-header.example.com",
		Path:        "/",
		Namespace:   "dev",
		Environment: "demo",
		Headers: map[string][]string{
			// 多值 header 中只要有一个值命中条件，就应命中对应 route。
			"x-tenant": []string{"legacy", "beta"},
		},
	})
	if err != nil {
		testingObject.Fatalf("resolve by multi-value header route failed: %v", err)
	}
	if multiValueResult.Route.RouteID != "route-header-beta" {
		testingObject.Fatalf(
			"unexpected routed route_id for multi-value header: got=%s want=route-header-beta",
			multiValueResult.Route.RouteID,
		)
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

// TestResolverAllowsScopedRequestWhenRouteScopeIsEmpty 验证 route scope 为空时，请求携带 scope 也可命中。
func TestResolverAllowsScopedRequestWhenRouteScopeIsEmpty(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-empty-scope",
		Namespace:   "",
		Environment: "",
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
		ServiceID:    "svc-empty-scope",
		ServiceKey:   "dev/alice/order-service",
		Namespace:    "dev",
		Environment:  "alice",
		ConnectorID:  "connector-empty-scope",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-empty-scope",
		ConnectorID: "connector-empty-scope",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "api.dev.example.com",
		Authority:   "api.dev.example.com",
		Namespace:   "dev",
		Environment: "alice",
	})
	if err != nil {
		testingObject.Fatalf("resolve with empty route scope failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeConnectorService {
		testingObject.Fatalf("unexpected target kind: got=%s want=%s", result.TargetKind, pb.RouteTargetTypeConnectorService)
	}
}

// TestResolverBalancesAcrossServiceInstances 验证同一服务池多实例会被轮询选中。
func TestResolverBalancesAcrossServiceInstances(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-balance",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "api.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "order-service/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-balance",
		ServiceKey:   "order-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-balance",
		ServiceKey:   "order-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	selectedConnectorIDs := map[string]struct{}{}
	for index := 0; index < 8; index++ {
		result, err := resolver.Resolve(ingress.RouteLookupRequest{
			IngressMode: pb.IngressModeL7Shared,
			Protocol:    "http",
			Host:        "api.demo.example.com",
			Authority:   "api.demo.example.com",
			Namespace:   "dev",
			Environment: "demo",
		})
		if err != nil {
			testingObject.Fatalf("resolve balance case failed: %v", err)
		}
		selectedConnectorIDs[result.Connector.Session.ConnectorID] = struct{}{}
	}
	if len(selectedConnectorIDs) != 2 {
		testingObject.Fatalf("expected both connectors selected, got=%v", selectedConnectorIDs)
	}
}

// TestResolverUsesInjectedServiceInstanceSelector 验证 Resolver 支持注入实例选择算法。
func TestResolverUsesInjectedServiceInstanceSelector(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-selector-inject",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "selector.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "selector-service/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-selector",
		ServiceKey:   "selector-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-selector",
		ServiceKey:   "selector-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:           routeRegistry,
		ServiceRegistry:         serviceRegistry,
		SessionRegistry:         sessionRegistry,
		ServiceInstanceSelector: fixedConnectorIDServiceInstanceSelector{connectorID: "connector-b"},
	})

	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "selector.demo.example.com",
		Authority:   "selector.demo.example.com",
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		testingObject.Fatalf("resolve with injected selector failed: %v", err)
	}
	if result.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	// 自定义选择器固定选择 connector-b，用于验证接口注入已生效。
	if result.Connector.Session.ConnectorID != "connector-b" {
		testingObject.Fatalf("unexpected selected connector: got=%s want=connector-b", result.Connector.Session.ConnectorID)
	}
}

// TestNewResolverUsesConfiguredSelectorAlgorithm 验证 Resolver 在未注入实例选择器时会按算法配置创建实现。
func TestNewResolverUsesConfiguredSelectorAlgorithm(testingObject *testing.T) {
	testingObject.Parallel()

	resolver := NewResolver(ResolverOptions{
		ServiceInstanceSelectorAlgorithm: ServiceInstanceSelectorAlgorithmRandom,
	})
	if resolver.serviceInstanceSelector == nil {
		testingObject.Fatalf("expected serviceInstanceSelector not nil")
	}
	if _, ok := resolver.serviceInstanceSelector.(*RandomServiceInstanceSelector); !ok {
		testingObject.Fatalf(
			"unexpected selector type: got=%T want=*routing.RandomServiceInstanceSelector",
			resolver.serviceInstanceSelector,
		)
	}
}

// TestResolverKeepsInstanceStickyWithinTrafficLifecycle 验证同一 traffic_id 在生命周期内固定实例。
func TestResolverKeepsInstanceStickyWithinTrafficLifecycle(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-sticky",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "sticky.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "sticky-service/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-sticky",
		ServiceKey:   "sticky-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-sticky",
		ServiceKey:   "sticky-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	lookupRequest := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "sticky.demo.example.com",
		Authority:   "sticky.demo.example.com",
		Namespace:   "dev",
		Environment: "demo",
		Metadata: map[string]string{
			RouteLookupMetadataTrafficIDKey: "traffic-sticky-1",
		},
	}
	firstResolution, err := resolver.Resolve(lookupRequest)
	if err != nil {
		testingObject.Fatalf("first sticky resolve failed: %v", err)
	}
	secondResolution, err := resolver.Resolve(lookupRequest)
	if err != nil {
		testingObject.Fatalf("second sticky resolve failed: %v", err)
	}
	if firstResolution.Connector == nil || secondResolution.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	if firstResolution.Connector.ServiceInstanceID == "" || secondResolution.Connector.ServiceInstanceID == "" {
		testingObject.Fatalf("expected non-empty service_instance_id for sticky check")
	}
	if firstResolution.Connector.ServiceInstanceID != secondResolution.Connector.ServiceInstanceID {
		testingObject.Fatalf(
			"same traffic should stick to same instance: first=%s second=%s",
			firstResolution.Connector.ServiceInstanceID,
			secondResolution.Connector.ServiceInstanceID,
		)
	}
}

// TestResolverStickyTrafficRejectsMidStreamFailover 验证粘性实例不可用时不会切换到其他实例。
func TestResolverStickyTrafficRejectsMidStreamFailover(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-sticky-failover",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "sticky-failover.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "sticky-failover/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	firstInstanceID := serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-sticky-failover",
		ServiceKey:   "sticky-failover/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	secondInstanceID := serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-sticky-failover",
		ServiceKey:   "sticky-failover/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	if firstInstanceID == "" || secondInstanceID == "" || firstInstanceID == secondInstanceID {
		testingObject.Fatalf(
			"expected two different service instances, first=%s second=%s",
			firstInstanceID,
			secondInstanceID,
		)
	}
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	lookupRequest := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "sticky-failover.demo.example.com",
		Authority:   "sticky-failover.demo.example.com",
		Namespace:   "dev",
		Environment: "demo",
		Metadata: map[string]string{
			RouteLookupMetadataTrafficIDKey: "traffic-no-failover-1",
		},
	}
	firstResolution, err := resolver.Resolve(lookupRequest)
	if err != nil {
		testingObject.Fatalf("first sticky resolve failed: %v", err)
	}
	if firstResolution.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	stickyInstanceID := firstResolution.Connector.ServiceInstanceID
	if stickyInstanceID == "" {
		testingObject.Fatalf("expected sticky instance id not empty")
	}
	// 把已绑定实例降级为不健康，模拟 traffic 生命周期中的实例失活。
	stickyInstances := serviceRegistry.ListInstancesByServiceID("svc-sticky-failover")
	for _, stickyInstance := range stickyInstances {
		if stickyInstance.ServiceInstanceID != stickyInstanceID {
			continue
		}
		stickyService := stickyInstance.Service
		stickyService.HealthStatus = pb.HealthStatusUnhealthy
		serviceRegistry.UpsertWithRuntime(now.Add(2*time.Second), stickyService, stickyInstance.SessionID)
	}
	_, stickyErr := resolver.Resolve(lookupRequest)
	if stickyErr == nil {
		testingObject.Fatalf("expected sticky resolve rejected when pinned instance unavailable")
	}
	if errorCode := ltfperrors.ExtractCode(stickyErr); errorCode != ltfperrors.CodeResolveServiceUnavailable {
		testingObject.Fatalf(
			"unexpected sticky resolve error code: got=%s want=%s err=%v",
			errorCode,
			ltfperrors.CodeResolveServiceUnavailable,
			stickyErr,
		)
	}
}

// TestResolverConvergesNewTrafficToHealthyInstanceAfterStickyFailure
// 验证已粘性 traffic 绑定实例失效后，新 traffic 可收敛到仍健康的实例。
func TestResolverConvergesNewTrafficToHealthyInstanceAfterStickyFailure(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-sticky-converge",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "sticky-converge.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "sticky-converge/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	firstInstanceID := serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-sticky-converge",
		ServiceKey:   "sticky-converge/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	secondInstanceID := serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-sticky-converge",
		ServiceKey:   "sticky-converge/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	if firstInstanceID == "" || secondInstanceID == "" || firstInstanceID == secondInstanceID {
		testingObject.Fatalf(
			"expected two different service instances, first=%s second=%s",
			firstInstanceID,
			secondInstanceID,
		)
	}
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	stickyTrafficRequest := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "sticky-converge.demo.example.com",
		Authority:   "sticky-converge.demo.example.com",
		Namespace:   "dev",
		Environment: "demo",
		Metadata: map[string]string{
			RouteLookupMetadataTrafficIDKey: "traffic-sticky-converge-1",
		},
	}
	firstResolution, err := resolver.Resolve(stickyTrafficRequest)
	if err != nil {
		testingObject.Fatalf("first sticky resolve failed: %v", err)
	}
	if firstResolution.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	stickyInstanceID := firstResolution.Connector.ServiceInstanceID
	if stickyInstanceID == "" {
		testingObject.Fatalf("expected sticky service_instance_id not empty")
	}
	expectedNewTrafficInstanceID := firstInstanceID
	if stickyInstanceID == firstInstanceID {
		expectedNewTrafficInstanceID = secondInstanceID
	}
	// 将已绑定实例标记为不健康，模拟实例失效。
	stickyInstances := serviceRegistry.ListInstancesByServiceID("svc-sticky-converge")
	for _, stickyInstance := range stickyInstances {
		if stickyInstance.ServiceInstanceID != stickyInstanceID {
			continue
		}
		stickyService := stickyInstance.Service
		stickyService.HealthStatus = pb.HealthStatusUnhealthy
		serviceRegistry.UpsertWithRuntime(now.Add(2*time.Second), stickyService, stickyInstance.SessionID)
	}

	// 同一 traffic 仍应保持粘性并直接报不可用，不允许 mid-stream failover。
	_, stickyErr := resolver.Resolve(stickyTrafficRequest)
	if stickyErr == nil {
		testingObject.Fatalf("expected sticky traffic resolve rejected after pinned instance failure")
	}
	if errorCode := ltfperrors.ExtractCode(stickyErr); errorCode != ltfperrors.CodeResolveServiceUnavailable {
		testingObject.Fatalf(
			"unexpected sticky resolve error code: got=%s want=%s err=%v",
			errorCode,
			ltfperrors.CodeResolveServiceUnavailable,
			stickyErr,
		)
	}

	// 新 traffic 不受旧粘性约束，应收敛到仍健康的实例。
	newTrafficRequest := stickyTrafficRequest
	newTrafficRequest.Metadata = map[string]string{
		RouteLookupMetadataTrafficIDKey: "traffic-sticky-converge-2",
	}
	newTrafficResolution, newTrafficErr := resolver.Resolve(newTrafficRequest)
	if newTrafficErr != nil {
		testingObject.Fatalf("expected new traffic converges to healthy instance, err=%v", newTrafficErr)
	}
	if newTrafficResolution.Connector == nil {
		testingObject.Fatalf("expected new traffic connector resolution not nil")
	}
	if newTrafficResolution.Connector.ServiceInstanceID != expectedNewTrafficInstanceID {
		testingObject.Fatalf(
			"unexpected new traffic instance after convergence: got=%s want=%s",
			newTrafficResolution.Connector.ServiceInstanceID,
			expectedNewTrafficInstanceID,
		)
	}
}

// TestResolverSkipsUnhealthyInstanceInPool 验证服务池内仅 ACTIVE+HEALTHY 实例可被选中。
func TestResolverSkipsUnhealthyInstanceInPool(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-healthy-filter",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "pay.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "pay-service/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-filter",
		ServiceKey:   "pay-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-unhealthy",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusUnhealthy,
	}, "session-unhealthy")
	serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-filter",
		ServiceKey:   "pay-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-healthy",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-healthy")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-unhealthy",
		ConnectorID: "connector-unhealthy",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-healthy",
		ConnectorID: "connector-healthy",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})

	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "pay.demo.example.com",
		Authority:   "pay.demo.example.com",
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		testingObject.Fatalf("resolve healthy filter case failed: %v", err)
	}
	if result.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	if result.Connector.Session.ConnectorID != "connector-healthy" {
		testingObject.Fatalf("expected healthy connector selected, got=%s", result.Connector.Session.ConnectorID)
	}
}

// TestResolverRecordsServiceDimensionMetrics 验证 resolver 会记录命中数、可用实例数与失败原因维度指标。
func TestResolverRecordsServiceDimensionMetrics(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	metrics := obs.NewMetrics()

	routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-metrics",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol: "http",
			Host:     "metrics.demo.example.com",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "metrics-service/http",
			},
		},
		Metadata: map[string]string{
			routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
		},
	})
	instanceA := serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-metrics",
		ServiceKey:   "metrics-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	instanceB := serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-metrics",
		ServiceKey:   "metrics-service/http",
		Namespace:    "dev",
		Environment:  "demo",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       1,
		State:       registry.SessionActive,
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		Metrics:         metrics,
		ServiceInstanceSelector: fixedConnectorIDServiceInstanceSelector{
			connectorID: "connector-b",
		},
	})

	resolveRequest := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "metrics.demo.example.com",
		Authority:   "metrics.demo.example.com",
		Path:        "/v1/metrics",
		Namespace:   "dev",
		Environment: "demo",
	}
	result, err := resolver.Resolve(resolveRequest)
	if err != nil {
		testingObject.Fatalf("resolve metrics case failed: %v", err)
	}
	if result.Connector == nil {
		testingObject.Fatalf("expected connector resolution not nil")
	}
	if metrics.BridgeServiceRouteHitTotal("svc-metrics") != 1 {
		testingObject.Fatalf("unexpected route hit total: got=%d want=1", metrics.BridgeServiceRouteHitTotal("svc-metrics"))
	}
	if metrics.BridgeServiceInstanceRouteHitTotal("svc-metrics", instanceB) != 1 {
		testingObject.Fatalf(
			"unexpected instance route hit total: got=%d want=1",
			metrics.BridgeServiceInstanceRouteHitTotal("svc-metrics", instanceB),
		)
	}
	if metrics.BridgeServiceAvailableInstanceTotal("svc-metrics") != 2 {
		testingObject.Fatalf(
			"unexpected available instance total: got=%d want=2",
			metrics.BridgeServiceAvailableInstanceTotal("svc-metrics"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceA) != 1 {
		testingObject.Fatalf(
			"expected instance-a available=1, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceA),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceB) != 1 {
		testingObject.Fatalf(
			"expected instance-b available=1, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceB),
		)
	}

	serviceRegistry.MarkLifecycleByConnectorAndSession(
		now.Add(2*time.Second),
		"connector-b",
		"session-b",
		pb.ServiceStatusInactive,
		pb.HealthStatusUnknown,
	)
	if _, err = resolver.Resolve(resolveRequest); err != nil {
		testingObject.Fatalf("resolve after lifecycle update failed: %v", err)
	}
	if metrics.BridgeServiceAvailableInstanceTotal("svc-metrics") != 1 {
		testingObject.Fatalf(
			"unexpected available instance total after degrade: got=%d want=1",
			metrics.BridgeServiceAvailableInstanceTotal("svc-metrics"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceB) != 0 {
		testingObject.Fatalf(
			"expected degraded instance available=0, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-metrics", instanceB),
		)
	}

	serviceRegistry.MarkLifecycleByConnectorAndSession(
		now.Add(3*time.Second),
		"connector-a",
		"session-a",
		pb.ServiceStatusInactive,
		pb.HealthStatusUnknown,
	)
	_, err = resolver.Resolve(resolveRequest)
	if ltfperrors.ExtractCode(err) != ltfperrors.CodeResolveServiceUnavailable {
		testingObject.Fatalf("unexpected resolve failure code: got=%s err=%v", ltfperrors.ExtractCode(err), err)
	}
	if metrics.BridgeServiceRouteFailureReasonTotal("svc-metrics", ltfperrors.CodeResolveServiceUnavailable) == 0 {
		testingObject.Fatalf("expected resolve_service_unavailable failure reason recorded")
	}
}
