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

type fixedConnectorIDServiceInstanceSelector struct {
	connectorID string
}

func (selector fixedConnectorIDServiceInstanceSelector) Select(candidates []ConnectorResolution, _ ServiceInstanceSelectionRequest) ConnectorResolution {
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

type stickyKeyCaptureServiceInstanceSelector struct {
	lastRequest ServiceInstanceSelectionRequest
}

func (selector *stickyKeyCaptureServiceInstanceSelector) Select(
	candidates []ConnectorResolution,
	request ServiceInstanceSelectionRequest,
) ConnectorResolution {
	selector.lastRequest = request
	if len(candidates) == 0 {
		return ConnectorResolution{}
	}
	return candidates[0]
}

// TestMatcherMatchIngressIsolation 验证三类入口路由不会互相串扰。
func TestMatcherMatchIngressIsolation(t *testing.T) {
	t.Parallel()

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
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			matchedRoutes := matcher.Match(testCase.request, routes)
			if len(matchedRoutes) != 1 {
				t.Fatalf("unexpected match size: got=%d want=1", len(matchedRoutes))
			}
			if matchedRoutes[0].RouteID != testCase.wantRouteID {
				t.Fatalf("unexpected route matched: got=%s want=%s", matchedRoutes[0].RouteID, testCase.wantRouteID)
			}
		})
	}
}

// TestMatcherSupportsHeaderMatchers 验证 header matcher 支持大小写无关与 exact/prefix 语义。
func TestMatcherSupportsHeaderMatchers(t *testing.T) {
	t.Parallel()

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
				Headers: []pb.HeaderMatcher{
					{Name: "X-Tenant", Exact: "alice"},
					{Name: "x-release", Prefix: "2026-03"},
				},
			},
		},
	}

	request := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "api.header.example.com",
		Authority:   "api.header.example.com",
		Path:        "/v1/orders",
		Headers: map[string][]string{
			"x-tenant":  {"alice"},
			"X-Release": {"2026-03-hotfix"},
		},
	}
	matchedRoutes := matcher.Match(request, routes)
	if len(matchedRoutes) != 1 || matchedRoutes[0].RouteID != "route-header-strict" {
		t.Fatalf("expected header-matched route selected, got=%v", matchedRoutes)
	}
}

// TestMatcherSupportsQueryMatchers 验证 query matcher 支持 exact/prefix/regex/present 组合。
func TestMatcherSupportsQueryMatchers(t *testing.T) {
	t.Parallel()

	matcher := NewMatcher()
	present := true
	routes := []pb.Route{
		{
			RouteID: "route-query-strict",
			Metadata: map[string]string{
				routeMetadataIngressModeKey: string(pb.IngressModeL7Shared),
			},
			Match: pb.RouteMatch{
				Protocol:   "http",
				Host:       "api.query.example.com",
				PathPrefix: "/v1",
				Queries: []pb.QueryMatcher{
					{Name: "tenant", Exact: "alice"},
					{Name: "version", Prefix: "2026-03"},
					{Name: "trace", Regex: "^req-[0-9]+$"},
					{Name: "debug", Present: &present},
				},
			},
		},
	}

	request := ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    "http",
		Host:        "api.query.example.com",
		Authority:   "api.query.example.com",
		Path:        "/v1/orders",
		Queries: map[string][]string{
			"tenant":  {"alice"},
			"version": {"2026-03-hotfix"},
			"trace":   {"req-42"},
			"debug":   {"1"},
		},
	}
	matchedRoutes := matcher.Match(request, routes)
	if len(matchedRoutes) != 1 || matchedRoutes[0].RouteID != "route-query-strict" {
		t.Fatalf("expected query-matched route selected, got=%v", matchedRoutes)
	}
}

// TestResolverResolveConnectorTarget 验证 resolver 可按 selector 解析 logical service 与实例。
func TestResolverResolveConnectorTarget(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstance(serviceRegistry, "ls-1", "inst-1", "order-service", "connector-1", "session-1", 1, pb.HealthStatusHealthy, map[string]string{"az": "a"})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-connector",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "order-service",
				},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		FallbackPolicies: []pb.ScopeFallbackPolicy{{
			PolicyID:  "fallback-dev-external",
			Namespace: "dev",
			Enabled:   true,
			External:  pb.ExternalFallbackConfig{Enabled: true},
		}},
	})
	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("resolve connector target failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeConnectorService {
		t.Fatalf("unexpected target kind: %s", result.TargetKind)
	}
	if result.Connector == nil {
		t.Fatalf("expected connector resolution exists")
	}
	if result.Connector.LogicalService.LogicalServiceID != "ls-1" {
		t.Fatalf("unexpected logical_service_id: %s", result.Connector.LogicalService.LogicalServiceID)
	}
	if result.Connector.Instance.InstanceID != "inst-1" {
		t.Fatalf("unexpected instance_id: %s", result.Connector.Instance.InstanceID)
	}
}

// TestResolverResolveConnectorTargetByMatchLabels 验证 selector 仅携带 match_labels 时可按同 scope 的 labels 解析逻辑服务。
func TestResolverResolveConnectorTargetByMatchLabels(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-labels",
		ServiceName:      "orders-canary",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		Labels:           map[string]string{"lane": "canary", "team": "payments"},
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-labels",
		LogicalServiceID: "ls-labels",
		ConnectorID:      "connector-labels",
		SessionID:        "session-labels",
		SessionEpoch:     1,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-labels",
		ConnectorID: "connector-labels",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-labels",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					Scope:       pb.Scope{Namespace: "dev", Environment: "demo"},
					MatchLabels: map[string]string{"lane": "canary", "team": "payments"},
				},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
	})
	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("resolve by match labels failed: %v", err)
	}
	if result.Connector == nil || result.Connector.LogicalService.LogicalServiceID != "ls-labels" {
		t.Fatalf("unexpected label-based logical service: %+v", result.Connector)
	}
}

// TestResolverRespectsTrafficAffinity 验证同 traffic_id 会粘到首次命中的实例。
func TestResolverRespectsTrafficAffinity(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstance(serviceRegistry, "ls-sticky", "inst-a", "sticky-service", "connector-a", "session-a", 1, pb.HealthStatusHealthy, nil)
	seedServiceInstance(serviceRegistry, "ls-sticky", "inst-b", "sticky-service", "connector-b", "session-b", 1, pb.HealthStatusHealthy, nil)
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
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-sticky",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "sticky-service",
				},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:           routeRegistry,
		ServiceRegistry:         serviceRegistry,
		SessionRegistry:         sessionRegistry,
		ServiceInstanceSelector: fixedConnectorIDServiceInstanceSelector{connectorID: "connector-b"},
	})

	firstResult, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
		Metadata: map[string]string{
			RouteLookupMetadataTrafficIDKey: "traffic-sticky",
		},
	})
	if err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	secondResult, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
		Metadata: map[string]string{
			RouteLookupMetadataTrafficIDKey: "traffic-sticky",
		},
	})
	if err != nil {
		t.Fatalf("second resolve failed: %v", err)
	}
	if firstResult.Connector.Instance.InstanceID != secondResult.Connector.Instance.InstanceID {
		t.Fatalf(
			"expected sticky instance reused: first=%s second=%s",
			firstResult.Connector.Instance.InstanceID,
			secondResult.Connector.Instance.InstanceID,
		)
	}
}

// TestResolverUsesHeaderStickyKey 验证 sticky_by=header:* 会把粘性 key 传给实例选择器。
func TestResolverUsesHeaderStickyKey(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstance(serviceRegistry, "ls-sticky-header", "inst-header", "sticky-header-service", "connector-header", "session-header", 1, pb.HealthStatusHealthy, nil)
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-header",
		ConnectorID: "connector-header",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID:    "route-sticky-header",
		Scope:      pb.Scope{Namespace: "dev", Environment: "demo"},
		PolicyJSON: `{"sticky_by":"header:X-Session-ID"}`,
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				LoadBalancePolicy: ServiceInstanceSelectorAlgorithmSticky,
				Selector: pb.ServiceSelector{
					ServiceName: "sticky-header-service",
				},
			},
		},
	})
	captureSelector := &stickyKeyCaptureServiceInstanceSelector{}
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:           routeRegistry,
		ServiceRegistry:         serviceRegistry,
		SessionRegistry:         sessionRegistry,
		ServiceInstanceSelector: captureSelector,
	})
	_, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
		Headers: map[string][]string{
			"X-Session-ID": {"sess-42"},
		},
	})
	if err != nil {
		t.Fatalf("resolve sticky header failed: %v", err)
	}
	if captureSelector.lastRequest.Policy != ServiceInstanceSelectorAlgorithmSticky {
		t.Fatalf("unexpected sticky policy: %s", captureSelector.lastRequest.Policy)
	}
	if captureSelector.lastRequest.StickyKey != "sess-42" {
		t.Fatalf("unexpected sticky key: %s", captureSelector.lastRequest.StickyKey)
	}
}

// TestResolverUsesWeightedLoadBalancePolicy 验证 weighted 策略会按实例 metadata.weight 轮转。
func TestResolverUsesWeightedLoadBalancePolicy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-weighted",
		ServiceName:      "weighted-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-a",
		LogicalServiceID: "ls-weighted",
		ConnectorID:      "connector-a",
		SessionID:        "session-a",
		SessionEpoch:     1,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
		Metadata:         map[string]string{"weight": "1"},
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-weighted",
		ServiceName:      "weighted-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-b",
		LogicalServiceID: "ls-weighted",
		ConnectorID:      "connector-b",
		SessionID:        "session-b",
		SessionEpoch:     2,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
		Metadata:         map[string]string{"weight": "3"},
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       2,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-weighted",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				LoadBalancePolicy: ServiceInstanceSelectorAlgorithmWeighted,
				Selector: pb.ServiceSelector{
					ServiceName: "weighted-service",
				},
			},
		},
	})
	metrics := obs.NewMetrics()
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		Metrics:         metrics,
	})

	observed := make([]string, 0, 4)
	for index := 0; index < 4; index++ {
		result, err := resolver.Resolve(ingress.RouteLookupRequest{
			IngressMode: pb.IngressModeL7Shared,
			Namespace:   "dev",
			Environment: "demo",
			Metadata: map[string]string{
				RouteLookupMetadataTrafficIDKey: "traffic-weighted-" + string(rune('a'+index)),
			},
		})
		if err != nil {
			t.Fatalf("resolve weighted candidate failed at index=%d: %v", index, err)
		}
		observed = append(observed, result.Connector.Instance.InstanceID)
	}
	expected := []string{"inst-a", "inst-b", "inst-b", "inst-b"}
	for index := range expected {
		if observed[index] != expected[index] {
			t.Fatalf("unexpected weighted resolve order at index=%d: got=%s want=%s full=%v", index, observed[index], expected[index], observed)
		}
	}
	if metrics.BridgeInstanceSelectorPickTotal("inst-b", ServiceInstanceSelectorAlgorithmWeighted) != 3 {
		t.Fatalf("unexpected weighted selector metric total: %d", metrics.BridgeInstanceSelectorPickTotal("inst-b", ServiceInstanceSelectorAlgorithmWeighted))
	}
}

// TestResolverExplicitRoundRobinOverridesDefaultAlgorithm 验证显式 round_robin 不会被全局默认算法覆盖。
func TestResolverExplicitRoundRobinOverridesDefaultAlgorithm(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-round-robin",
		ServiceName:      "round-robin-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-a",
		LogicalServiceID: "ls-round-robin",
		ConnectorID:      "connector-a",
		SessionID:        "session-a",
		SessionEpoch:     1,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
		Metadata:         map[string]string{"weight": "1"},
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-round-robin",
		ServiceName:      "round-robin-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-b",
		LogicalServiceID: "ls-round-robin",
		ConnectorID:      "connector-b",
		SessionID:        "session-b",
		SessionEpoch:     2,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
		Metadata:         map[string]string{"weight": "3"},
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       2,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-round-robin",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				LoadBalancePolicy: ServiceInstanceSelectorAlgorithmRoundRobin,
				Selector: pb.ServiceSelector{
					ServiceName: "round-robin-service",
				},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:                    routeRegistry,
		ServiceRegistry:                  serviceRegistry,
		SessionRegistry:                  sessionRegistry,
		ServiceInstanceSelectorAlgorithm: ServiceInstanceSelectorAlgorithmWeighted,
	})

	observed := make([]string, 0, 4)
	for index := 0; index < 4; index++ {
		result, err := resolver.Resolve(ingress.RouteLookupRequest{
			IngressMode: pb.IngressModeL7Shared,
			Namespace:   "dev",
			Environment: "demo",
			Metadata: map[string]string{
				RouteLookupMetadataTrafficIDKey: "traffic-round-robin-" + string(rune('a'+index)),
			},
		})
		if err != nil {
			t.Fatalf("resolve round robin candidate failed at index=%d: %v", index, err)
		}
		observed = append(observed, result.Connector.Instance.InstanceID)
	}
	expected := []string{"inst-a", "inst-b", "inst-a", "inst-b"}
	for index := range expected {
		if observed[index] != expected[index] {
			t.Fatalf("unexpected round robin resolve order at index=%d: got=%s want=%s full=%v", index, observed[index], expected[index], observed)
		}
	}
}

// TestResolverObserveRouteFailureMetrics 验证解析失败会记录 logical_service 维度失败原因。
func TestResolverObserveRouteFailureMetrics(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstance(serviceRegistry, "ls-metrics", "inst-metrics", "metrics-service", "connector-metrics", "session-metrics", 1, pb.HealthStatusUnhealthy, nil)
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-metrics",
		ConnectorID: "connector-metrics",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-metrics",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "metrics-service",
				},
			},
		},
	})
	metrics := obs.NewMetrics()
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		Metrics:         metrics,
	})

	_, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err == nil {
		t.Fatalf("expected resolve failure")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeResolveServiceUnavailable) {
		t.Fatalf("unexpected error code: %s", ltfperrors.ExtractCode(err))
	}
	if metrics.BridgeServiceRouteFailureReasonTotal("ls-metrics", ltfperrors.CodeResolveServiceUnavailable) != 1 {
		t.Fatalf("expected route failure metric incremented")
	}
}

// TestResolverUsesDefaultScope 验证请求缺失 scope 时会回落到配置的 default_scope。
func TestResolverUsesDefaultScope(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstanceWithScope(serviceRegistry, pb.Scope{Namespace: "default", Environment: "base"}, "ls-default", "inst-default", "default-service", "connector-default", "session-default", 1, pb.HealthStatusHealthy, nil)
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-default",
		ConnectorID: "connector-default",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-default-scope",
		Scope: pb.Scope{
			Namespace:   "default",
			Environment: "base",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "default-service",
				},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		DefaultScope:    pb.Scope{Namespace: "default", Environment: "base"},
	})
	result, err := resolver.Resolve(ingress.RouteLookupRequest{IngressMode: pb.IngressModeL7Shared})
	if err != nil {
		t.Fatalf("resolve with default scope failed: %v", err)
	}
	if result.MatchedScope != (pb.Scope{Namespace: "default", Environment: "base"}) {
		t.Fatalf("unexpected matched scope: %+v", result.MatchedScope)
	}
}

// TestResolverAppliesScopeFallbackPolicy 验证 request_scope miss 后会按配置降级到下一级 scope。
func TestResolverAppliesScopeFallbackPolicy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstanceWithScope(serviceRegistry, pb.Scope{Namespace: "dev", Environment: "base"}, "ls-base", "inst-base", "orders", "connector-base", "session-base", 1, pb.HealthStatusHealthy, nil)
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-base",
		ConnectorID: "connector-base",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-base-scope",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "base",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "orders",
				},
			},
		},
	})
	metrics := obs.NewMetrics()
	resolver := NewResolver(ResolverOptions{
		RouteRegistry:    routeRegistry,
		ServiceRegistry:  serviceRegistry,
		SessionRegistry:  sessionRegistry,
		Metrics:          metrics,
		FallbackPolicies: []pb.ScopeFallbackPolicy{{PolicyID: "fallback-dev", Namespace: "dev", Enabled: true, Chain: []pb.FallbackStep{{TargetScope: pb.Scope{Namespace: "dev", Environment: "base"}}}}},
	})

	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "alice",
	})
	if err != nil {
		t.Fatalf("resolve with fallback policy failed: %v", err)
	}
	if result.MatchedScope != (pb.Scope{Namespace: "dev", Environment: "base"}) {
		t.Fatalf("unexpected matched scope: %+v", result.MatchedScope)
	}
	if len(result.ScopeFallbackPath) != 2 {
		t.Fatalf("unexpected fallback path: %+v", result.ScopeFallbackPath)
	}
	if metrics.BridgeScopeFallbackTotal() != 1 {
		t.Fatalf("expected scope fallback metric incremented")
	}
}

// TestResolverPrefersConnectorBeforeExternal 验证存在本地 connector 命中时，不会提前落到 external fallback。
func TestResolverPrefersConnectorBeforeExternal(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	seedServiceInstance(serviceRegistry, "ls-local", "inst-local", "orders", "connector-local", "session-local", 1, pb.HealthStatusHealthy, nil)
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-local",
		ConnectorID: "connector-local",
		Epoch:       1,
		State:       registry.SessionActive,
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-local",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-external",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				ServiceName: "orders",
				Selector:    map[string]string{"endpoint": "127.0.0.1:19090"},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		FallbackPolicies: []pb.ScopeFallbackPolicy{{
			PolicyID:  "fallback-dev-external",
			Namespace: "dev",
			Enabled:   true,
			External:  pb.ExternalFallbackConfig{Enabled: true},
		}},
	})
	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("resolve local before external failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeConnectorService {
		t.Fatalf("unexpected target kind: %s", result.TargetKind)
	}
	if result.IsExternalFallback {
		t.Fatalf("expected connector path not marked as external fallback")
	}
}

// TestResolverFallsBackToExternalAfterLocalMiss 验证本地 route miss 后才落到 external route。
func TestResolverFallsBackToExternalAfterLocalMiss(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-local-miss",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-external-fallback",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				ServiceName: "orders",
				Selector:    map[string]string{"endpoint": "127.0.0.1:19090"},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		FallbackPolicies: []pb.ScopeFallbackPolicy{{
			PolicyID:  "fallback-dev-external",
			Namespace: "dev",
			Enabled:   true,
			External:  pb.ExternalFallbackConfig{Enabled: true},
		}},
	})
	result, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err != nil {
		t.Fatalf("resolve external after local miss failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeExternalService {
		t.Fatalf("unexpected target kind: %s", result.TargetKind)
	}
	if !result.IsExternalFallback {
		t.Fatalf("expected external target marked as fallback")
	}
}

// TestResolverRejectsExternalFallbackWhenPolicyDisabled 验证未显式启用 external 时不会落到 external route。
func TestResolverRejectsExternalFallbackWhenPolicyDisabled(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	routeRegistry := registry.NewRouteRegistry()
	serviceRegistry := registry.NewServiceRegistry()
	sessionRegistry := registry.NewSessionRegistry()
	routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-external-disabled",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				ServiceName: "orders",
				Selector:    map[string]string{"endpoint": "127.0.0.1:19090"},
			},
		},
	})

	resolver := NewResolver(ResolverOptions{
		RouteRegistry:   routeRegistry,
		ServiceRegistry: serviceRegistry,
		SessionRegistry: sessionRegistry,
		FallbackPolicies: []pb.ScopeFallbackPolicy{{
			PolicyID:  "fallback-dev-disabled",
			Namespace: "dev",
			Enabled:   true,
			External:  pb.ExternalFallbackConfig{Enabled: false},
		}},
	})
	_, err := resolver.Resolve(ingress.RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Namespace:   "dev",
		Environment: "demo",
	})
	if err == nil {
		t.Fatalf("expected external fallback disabled error")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeIngressRouteMismatch) {
		t.Fatalf("unexpected error code: got=%s want=%s", ltfperrors.ExtractCode(err), ltfperrors.CodeIngressRouteMismatch)
	}
}

func seedServiceInstance(
	serviceRegistry *registry.ServiceRegistry,
	logicalServiceID string,
	instanceID string,
	serviceName string,
	connectorID string,
	sessionID string,
	resourceVersion uint64,
	healthStatus pb.HealthStatus,
	labels map[string]string,
) {
	seedServiceInstanceWithScope(
		serviceRegistry,
		pb.Scope{Namespace: "dev", Environment: "demo"},
		logicalServiceID,
		instanceID,
		serviceName,
		connectorID,
		sessionID,
		resourceVersion,
		healthStatus,
		labels,
	)
}

func seedServiceInstanceWithScope(
	serviceRegistry *registry.ServiceRegistry,
	scope pb.Scope,
	logicalServiceID string,
	instanceID string,
	serviceName string,
	connectorID string,
	sessionID string,
	resourceVersion uint64,
	healthStatus pb.HealthStatus,
	labels map[string]string,
) {
	serviceRegistry.Upsert(time.Now().UTC(), pb.LogicalService{
		LogicalServiceID: logicalServiceID,
		ServiceName:      serviceName,
		Scope:            scope,
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  resourceVersion,
	}, pb.ServiceInstance{
		InstanceID:       instanceID,
		LogicalServiceID: logicalServiceID,
		ConnectorID:      connectorID,
		SessionID:        sessionID,
		SessionEpoch:     resourceVersion,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     healthStatus,
		ResourceVersion:  resourceVersion,
		Labels:           labels,
	})
}
