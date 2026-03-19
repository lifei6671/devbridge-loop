package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type publishHandlerTestHostDeriver struct {
	host string
	err  error
}

func (deriver publishHandlerTestHostDeriver) Derive(_ string, _ pb.Scope) (string, error) {
	if deriver.err != nil {
		return "", deriver.err
	}
	return deriver.host, nil
}

// TestPublishHandlerHandlePublish 验证发布处理器的幂等与版本比较行为。
func TestPublishHandlerHandlePublish(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-1"
		},
	})

	message := pb.PublishService{
		InstanceID:  "inst-1",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	}

	testCases := []struct {
		name               string
		envelope           pb.ControlEnvelope
		expectAccepted     bool
		expectErrorCode    string
		expectCurrentVer   uint64
		expectRegistrySize int
	}{
		{
			name: "accepted new version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-1",
				ResourceVersion: 1,
			},
			expectAccepted:     true,
			expectCurrentVer:   1,
			expectRegistrySize: 1,
		},
		{
			name: "accepted newer version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-2",
				ResourceVersion: 2,
			},
			expectAccepted:     true,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "reject old resource version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-3",
				ResourceVersion: 1,
			},
			expectAccepted:     false,
			expectErrorCode:    ltfperrors.CodeVersionRollback,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "duplicate replay event id",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-2",
				ResourceVersion: 3,
			},
			expectAccepted:     true,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "reject stale epoch",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    2,
				EventID:         "evt-4",
				ResourceVersion: 3,
			},
			expectAccepted:     false,
			expectErrorCode:    ltfperrors.CodeStaleSessionEpoch,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			ack := handler.HandlePublish(testCase.envelope, message)
			if ack.Accepted != testCase.expectAccepted {
				t.Fatalf("unexpected accepted: got=%v want=%v", ack.Accepted, testCase.expectAccepted)
			}
			if testCase.expectErrorCode != "" && ack.ErrorCode != testCase.expectErrorCode {
				t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, testCase.expectErrorCode)
			}
			if ack.CurrentResourceVersion != testCase.expectCurrentVer {
				t.Fatalf("unexpected current version: got=%d want=%d", ack.CurrentResourceVersion, testCase.expectCurrentVer)
			}
			if services := handler.serviceRegistry.List(); len(services) != testCase.expectRegistrySize {
				t.Fatalf("unexpected registry size: got=%d want=%d", len(services), testCase.expectRegistrySize)
			}
		})
	}

	instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID("inst-1")
	if !exists {
		t.Fatalf("expected instance snapshot exists after publish flow")
	}
	if instanceSnapshot.Instance.ConnectorID != "connector-1" {
		t.Fatalf("unexpected connector_id: got=%s want=connector-1", instanceSnapshot.Instance.ConnectorID)
	}
	if instanceSnapshot.Instance.LogicalServiceID != "ls-1" {
		t.Fatalf("unexpected logical_service_id: got=%s want=ls-1", instanceSnapshot.Instance.LogicalServiceID)
	}
}

// TestPublishHandlerDerivesExposureHostAndPersistsPayload 验证空 exposure.host 会被派生并写入实例快照。
func TestPublishHandlerDerivesExposureHostAndPersistsPayload(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-derive",
		ConnectorID: "connector-derive",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		HostDeriver: publishHandlerTestHostDeriver{
			host: "orders.alice.dev.example.com",
		},
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-derive"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-derive",
		SessionEpoch:    3,
		EventID:         "evt-derive",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-derive",
		ServiceName: "orders",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			PathPrefix:  "/api",
		},
		DiscoveryPolicy: pb.DiscoveryPolicy{
			Enabled:   true,
			Providers: []string{"nacos"},
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}

	instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID("inst-derive")
	if !exists {
		t.Fatalf("expected derived instance snapshot exists")
	}
	if instanceSnapshot.Instance.Exposure.Host != "orders.alice.dev.example.com" {
		t.Fatalf("unexpected derived exposure host: %s", instanceSnapshot.Instance.Exposure.Host)
	}
	if len(instanceSnapshot.Instance.Endpoints) != 1 || instanceSnapshot.Instance.Endpoints[0].Port != 18080 {
		t.Fatalf("unexpected endpoints persisted: %+v", instanceSnapshot.Instance.Endpoints)
	}
	if !instanceSnapshot.Instance.DiscoveryPolicy.Enabled {
		t.Fatalf("expected discovery policy persisted")
	}
}

// TestPublishHandlerAutoRegistersRouteFromPublishService 验证 PublishService 会在 Bridge 侧自动派生路由。
func TestPublishHandlerAutoRegistersRouteFromPublishService(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-route",
		ConnectorID: "connector-route",
		Epoch:       7,
		State:       registry.SessionActive,
	})
	routeRegistry := registry.NewRouteRegistry()
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		RouteRegistry:   routeRegistry,
		HostDeriver: publishHandlerTestHostDeriver{
			host: "orders.alice.dev.example.com",
		},
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-route"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-route",
		SessionEpoch:    7,
		EventID:         "evt-route",
		ResourceVersion: 9,
	}, pb.PublishService{
		InstanceID:  "inst-route",
		ServiceName: "orders",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			PathPrefix:  "/api/orders",
		},
		RouteHint: pb.RouteHint{
			MatchHeaders: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
			MatchQueries: []pb.QueryMatcher{
				{Name: "version", Exact: "v2"},
			},
			Priority: 23,
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}

	routeSnapshot, exists := routeRegistry.Get("agent-auto-route-ls-route")
	if !exists {
		t.Fatalf("expected auto route registered")
	}
	if routeSnapshot.Match.Host != "orders.alice.dev.example.com" {
		t.Fatalf("unexpected route host: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Match.Protocol != "http" {
		t.Fatalf("unexpected route protocol: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Match.PathPrefix != "/api/orders" {
		t.Fatalf("unexpected route path prefix: %+v", routeSnapshot.Match)
	}
	if len(routeSnapshot.Match.Headers) != 1 || routeSnapshot.Match.Headers[0].Name != "x-tenant" {
		t.Fatalf("unexpected route headers: %+v", routeSnapshot.Match.Headers)
	}
	if len(routeSnapshot.Match.Queries) != 1 || routeSnapshot.Match.Queries[0].Name != "version" {
		t.Fatalf("unexpected route queries: %+v", routeSnapshot.Match.Queries)
	}
	if routeSnapshot.Priority != 23 {
		t.Fatalf("unexpected route priority: %+v", routeSnapshot)
	}
	if routeSnapshot.ScopeInjection.InjectPolicy != pb.ScopeInjectPolicyAlways {
		t.Fatalf("unexpected scope injection policy: %+v", routeSnapshot.ScopeInjection)
	}
	if routeSnapshot.ScopeInjection.InjectScope.Namespace != "dev" || routeSnapshot.ScopeInjection.InjectScope.Environment != "alice" {
		t.Fatalf("unexpected scope injection scope: %+v", routeSnapshot.ScopeInjection)
	}
}

// TestPublishHandlerAutoRegistersTLSSNIRoute 验证 tls_sni_shared 自动路由使用 TLS/SNI 维度匹配。
func TestPublishHandlerAutoRegistersTLSSNIRoute(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-tls-route",
		ConnectorID: "connector-tls-route",
		Epoch:       8,
		State:       registry.SessionActive,
	})
	routeRegistry := registry.NewRouteRegistry()
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		RouteRegistry:   routeRegistry,
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-tls-route"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-tls-route",
		SessionEpoch:    8,
		EventID:         "evt-tls-route",
		ResourceVersion: 10,
	}, pb.PublishService{
		InstanceID:  "inst-tls-route",
		ServiceName: "payments",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "https",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "https", Host: "127.0.0.1", Port: 18443},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeTLSSNIShared,
			ListenPort:  443,
			SNIName:     "pay.dev.example.com",
		},
		RouteHint: pb.RouteHint{
			MatchHeaders: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
			MatchQueries: []pb.QueryMatcher{
				{Name: "version", Exact: "v2"},
			},
			Priority: 7,
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}

	routeSnapshot, exists := routeRegistry.Get("agent-auto-route-ls-tls-route")
	if !exists {
		t.Fatalf("expected tls-sni auto route registered")
	}
	if routeSnapshot.Match.Protocol != "tls" || routeSnapshot.Match.SNI != "pay.dev.example.com" {
		t.Fatalf("unexpected tls-sni route match: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Match.ListenPort != 443 {
		t.Fatalf("unexpected tls-sni listen port: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Match.Host != "" || routeSnapshot.Match.PathPrefix != "" {
		t.Fatalf("unexpected tls-sni host/path match: %+v", routeSnapshot.Match)
	}
	if len(routeSnapshot.Match.Headers) != 0 || len(routeSnapshot.Match.Queries) != 0 {
		t.Fatalf("unexpected tls-sni header/query matchers: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Priority != 7 {
		t.Fatalf("unexpected tls-sni route priority: %+v", routeSnapshot)
	}
}

// TestPublishHandlerAutoRegistersL4DedicatedPortRoute 验证 l4_dedicated_port 自动路由使用 TCP/端口匹配。
func TestPublishHandlerAutoRegistersL4DedicatedPortRoute(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-l4-route",
		ConnectorID: "connector-l4-route",
		Epoch:       9,
		State:       registry.SessionActive,
	})
	routeRegistry := registry.NewRouteRegistry()
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		RouteRegistry:   routeRegistry,
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-l4-route"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-l4-route",
		SessionEpoch:    9,
		EventID:         "evt-l4-route",
		ResourceVersion: 12,
	}, pb.PublishService{
		InstanceID:  "inst-l4-route",
		ServiceName: "tcp-echo",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "tcp",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "tcp", Host: "127.0.0.1", Port: 19090},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL4DedicatedPort,
			ListenPort:  18081,
		},
		RouteHint: pb.RouteHint{
			MatchHeaders: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
			MatchQueries: []pb.QueryMatcher{
				{Name: "version", Exact: "v2"},
			},
			Priority: 9,
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}

	routeSnapshot, exists := routeRegistry.Get("agent-auto-route-ls-l4-route")
	if !exists {
		t.Fatalf("expected l4 auto route registered")
	}
	if routeSnapshot.Match.Protocol != "tcp" || routeSnapshot.Match.ListenPort != 18081 {
		t.Fatalf("unexpected l4 route match: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Match.Host != "" || routeSnapshot.Match.PathPrefix != "" || routeSnapshot.Match.SNI != "" {
		t.Fatalf("unexpected l4 host/path/sni match: %+v", routeSnapshot.Match)
	}
	if len(routeSnapshot.Match.Headers) != 0 || len(routeSnapshot.Match.Queries) != 0 {
		t.Fatalf("unexpected l4 header/query matchers: %+v", routeSnapshot.Match)
	}
	if routeSnapshot.Priority != 9 {
		t.Fatalf("unexpected l4 route priority: %+v", routeSnapshot)
	}
}

// TestPublishHandlerRejectsPublishWhenAutoRouteConflicts 验证自动派生路由冲突时会拒绝 PublishService。
func TestPublishHandlerRejectsPublishWhenAutoRouteConflicts(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-conflict",
		ConnectorID: "connector-conflict",
		Epoch:       5,
		State:       registry.SessionActive,
	})
	routeRegistry := registry.NewRouteRegistry()
	routeRegistry.Upsert(time.Now().UTC(), pb.Route{
		RouteID:         "route-existing",
		Scope:           pb.Scope{Namespace: "dev", Environment: "alice"},
		ResourceVersion: 1,
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
			Headers: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{LogicalServiceID: "ls-existing"},
			},
		},
		Priority: 0,
		Metadata: map[string]string{"source": "manual"},
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		RouteRegistry:   routeRegistry,
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-conflict"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-conflict",
		SessionEpoch:    5,
		EventID:         "evt-conflict",
		ResourceVersion: 11,
	}, pb.PublishService{
		InstanceID:  "inst-conflict",
		ServiceName: "orders",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 19090},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			Host:        "api.dev.example.com",
			PathPrefix:  "/orders",
		},
		RouteHint: pb.RouteHint{
			MatchHeaders: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected publish rejected on route conflict")
	}
	if ack.ErrorCode != ltfperrors.CodeIngressRouteMismatch {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeIngressRouteMismatch)
	}
	if services := handler.serviceRegistry.List(); len(services) != 0 {
		t.Fatalf("unexpected services persisted on conflict: %+v", services)
	}
}

// TestPublishHandlerRejectLegacyPayloadFields 验证旧字段一律拒绝。
func TestPublishHandlerRejectLegacyPayloadFields(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-legacy",
		ConnectorID: "connector-legacy",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-legacy",
		SessionEpoch:    3,
		EventID:         "evt-legacy",
		ResourceVersion: 1,
		Payload:         []byte(`{"serviceKey":"legacy/order-service"}`),
	}, pb.PublishService{
		InstanceID:  "inst-legacy",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected legacy payload rejected")
	}
	if ack.ErrorCode != ltfperrors.CodeUnsupportedLegacyProtocol {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeUnsupportedLegacyProtocol)
	}
}

// TestPublishHandlerRejectStaleInstancePublishAfterFullSync 验证 full-sync 回填版本后旧实例事件不能绕过回滚保护。
func TestPublishHandlerRejectStaleInstancePublishAfterFullSync(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-fs",
		ConnectorID: "connector-fs",
		Epoch:       5,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	handler.ReconcileFromFullSync(pb.FullSyncSnapshot{
		Completed: true,
		LogicalServices: []pb.LogicalService{
			{
				LogicalServiceID: "ls-fs",
				ServiceName:      "order-service",
				Scope: pb.Scope{
					Namespace:   "dev",
					Environment: "alice",
				},
				Status:          pb.ServiceStatusActive,
				ResourceVersion: 10,
			},
		},
		ServiceInstances: []pb.ServiceInstance{
			{
				InstanceID:       "inst-fs",
				LogicalServiceID: "ls-fs",
				ConnectorID:      "connector-fs",
				SessionID:        "session-fs",
				SessionEpoch:     5,
				InstanceStatus:   pb.ServiceStatusActive,
				HealthStatus:     pb.HealthStatusHealthy,
				ResourceVersion:  10,
			},
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-fs",
		SessionEpoch:    5,
		ConnectorID:     "connector-fs",
		EventID:         "evt-stale-after-full-sync",
		ResourceVersion: 5,
	}, pb.PublishService{
		InstanceID:  "inst-fs",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected stale publish rejected after full-sync")
	}
	if ack.ErrorCode != ltfperrors.CodeVersionRollback {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeVersionRollback)
	}
	if ack.CurrentResourceVersion != 10 {
		t.Fatalf("unexpected current resource version: got=%d want=10", ack.CurrentResourceVersion)
	}
	if currentVersion := handler.serviceRegistry.CurrentVersion("ls-fs", "inst-fs"); currentVersion != 10 {
		t.Fatalf("unexpected registry current version after stale publish: got=%d want=10", currentVersion)
	}
}

// TestPublishHandlerHandleUnpublish 验证下线处理器的幂等与删除行为。
func TestPublishHandlerHandleUnpublish(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-1"
		},
	})
	publishAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-1",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !publishAck.Accepted {
		t.Fatalf("publish should be accepted, got error=%s", publishAck.ErrorCode)
	}

	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-2",
		ResourceVersion: 2,
	}, pb.UnpublishService{
		InstanceID:       "inst-1",
		LogicalServiceID: "ls-1",
	})
	if !unpublishAck.Accepted {
		t.Fatalf("unpublish should be accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if instances := handler.serviceRegistry.ListInstancesByLogicalServiceID("ls-1"); len(instances) != 0 {
		t.Fatalf("instance should be removed, got=%d", len(instances))
	}

	logicalService, exists := handler.serviceRegistry.GetLogicalServiceByID("ls-1")
	if !exists {
		t.Fatalf("logical service should still exist after last instance removed")
	}
	if logicalService.Status != pb.ServiceStatusInactive {
		t.Fatalf("expected logical service becomes inactive, got=%s", logicalService.Status)
	}

	dupAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-2",
		ResourceVersion: 999,
	}, pb.UnpublishService{
		InstanceID:       "inst-1",
		LogicalServiceID: "ls-1",
	})
	if !dupAck.Accepted {
		t.Fatalf("duplicate unpublish should be accepted")
	}
	if dupAck.CurrentResourceVersion != 2 {
		t.Fatalf("unexpected current version: got=%d want=2", dupAck.CurrentResourceVersion)
	}
}

// TestPublishHandlerRejectMutationWhenSessionNotActive 验证非 ACTIVE 会话不能写入服务资源。
func TestPublishHandlerRejectMutationWhenSessionNotActive(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-draining",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionDraining,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
	})
	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-draining",
		SessionEpoch:    3,
		EventID:         "evt-draining",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-draining",
		ServiceName: "draining-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected draining session publish rejected")
	}
	if ack.ErrorCode != ltfperrors.CodeInvalidStateTransition {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeInvalidStateTransition)
	}
}

// TestPublishHandlerGroupsInstancesByLogicalService 验证同 scope/serviceName 的多实例会归并到同一 logical service。
func TestPublishHandlerGroupsInstancesByLogicalService(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       11,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       12,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-shared"
		},
	})
	firstAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-a",
		SessionEpoch:    11,
		ConnectorID:     "connector-a",
		EventID:         "evt-multi-a-1",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-a",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected first publish accepted, got error=%s", firstAck.ErrorCode)
	}

	secondAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-b",
		SessionEpoch:    12,
		ConnectorID:     "connector-b",
		EventID:         "evt-multi-b-1",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-b",
		ServiceName: "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 19090},
		},
	})
	if !secondAck.Accepted {
		t.Fatalf("expected second publish accepted, got error=%s", secondAck.ErrorCode)
	}
	if secondAck.LogicalServiceID != firstAck.LogicalServiceID {
		t.Fatalf("expected pooled logical_service_id reused: first=%s second=%s", firstAck.LogicalServiceID, secondAck.LogicalServiceID)
	}
	if services := handler.serviceRegistry.List(); len(services) != 1 {
		t.Fatalf("unexpected logical service size: got=%d want=1", len(services))
	}
	instances := handler.serviceRegistry.ListInstancesByLogicalServiceID("ls-shared")
	if len(instances) != 2 {
		t.Fatalf("unexpected service instance count: got=%d want=2", len(instances))
	}
}

// TestPublishHandlerUnpublishRemovesOnlyMatchedInstance 验证按实例下线时仅删除目标实例。
func TestPublishHandlerUnpublishRemovesOnlyMatchedInstance(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       21,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       22,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-pay"
		},
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-a-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-pay-a",
		ServiceName: "pay-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 28080},
		},
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-b",
		SessionEpoch:    22,
		ConnectorID:     "connector-b",
		EventID:         "evt-unpub-b-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-pay-b",
		ServiceName: "pay-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 29090},
		},
	})

	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-a-1",
		ResourceVersion: 2,
	}, pb.UnpublishService{
		InstanceID:       "inst-pay-a",
		LogicalServiceID: "ls-pay",
	})
	if !unpublishAck.Accepted {
		t.Fatalf("expected unpublish accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if instances := handler.serviceRegistry.ListInstancesByLogicalServiceID("ls-pay"); len(instances) != 1 {
		t.Fatalf("unexpected remaining instance count: got=%d want=1", len(instances))
	}
	if services := handler.serviceRegistry.List(); len(services) != 1 {
		t.Fatalf("expected pooled logical service still exists, got=%d", len(services))
	}
}

// TestPublishHandlerRejectsCrossConnectorUnpublishByInstanceID 验证 instance_id 下线会严格校验 connector 归属。
func TestPublishHandlerRejectsCrossConnectorUnpublishByInstanceID(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       21,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       22,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-pay"
		},
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-a-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-pay-a",
		ServiceName: "pay-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 28080},
		},
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-b",
		SessionEpoch:    22,
		ConnectorID:     "connector-b",
		EventID:         "evt-unpub-b-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-pay-b",
		ServiceName: "pay-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 29090},
		},
	})

	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-cross-1",
		ResourceVersion: 2,
	}, pb.UnpublishService{
		InstanceID:       "inst-pay-b",
		LogicalServiceID: "ls-pay",
	})
	if unpublishAck.Accepted {
		t.Fatalf("expected cross-connector unpublish rejected")
	}
	if unpublishAck.ErrorCode != ltfperrors.CodeInstanceOwnershipMismatch {
		t.Fatalf("unexpected error code: got=%s want=%s", unpublishAck.ErrorCode, ltfperrors.CodeInstanceOwnershipMismatch)
	}
	if instances := handler.serviceRegistry.ListInstancesByLogicalServiceID("ls-pay"); len(instances) != 2 {
		t.Fatalf("unexpected instance count after rejected cross unpublish: got=%d want=2", len(instances))
	}
}

// TestPublishHandlerRecordsMetrics 验证发布会刷新服务池/实例计数指标。
func TestPublishHandlerRecordsMetrics(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-metric",
		ConnectorID: "connector-metric",
		Epoch:       4,
		State:       registry.SessionActive,
	})
	metrics := obs.NewMetrics()
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Metrics:         metrics,
		ServiceIDGenerator: func(_ time.Time, _ string) string {
			return "ls-metric"
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-metric",
		SessionEpoch:    4,
		ConnectorID:     "connector-metric",
		EventID:         "evt-metric",
		ResourceVersion: 1,
	}, pb.PublishService{
		InstanceID:  "inst-metric",
		ServiceName: "metric-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}
	if metrics.BridgeServicePublishTotal("ls-metric") != 1 {
		t.Fatalf("unexpected service publish total: got=%d want=1", metrics.BridgeServicePublishTotal("ls-metric"))
	}
	if metrics.BridgeServiceInstancePublishTotal("ls-metric", "inst-metric") != 1 {
		t.Fatalf(
			"unexpected service instance publish total: got=%d want=1",
			metrics.BridgeServiceInstancePublishTotal("ls-metric", "inst-metric"),
		)
	}
	if metrics.BridgeServiceAvailableInstanceTotal("ls-metric") != 0 {
		t.Fatalf("newly published unknown-health instance should not be routable")
	}
}
