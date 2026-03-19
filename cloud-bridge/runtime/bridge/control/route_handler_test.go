package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type routeHandlerTestHostDeriver struct {
	host string
}

func (deriver routeHandlerTestHostDeriver) Derive(_ string, _ pb.Scope) (string, error) {
	return deriver.host, nil
}

// TestRouteHandlerHandleAssignAndRevoke 验证路由事件的幂等与版本语义。
func TestRouteHandlerHandleAssignAndRevoke(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})

	assign := pb.RouteAssign{
		RouteID: "route-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		ScopeInjection: pb.ScopeInjection{
			InjectScope:  pb.Scope{Namespace: "prod", Environment: "main"},
			InjectPolicy: pb.ScopeInjectPolicyAlways,
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					ServiceName: "orders",
				},
			},
		},
	}
	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 1,
		ResourceID:      "route-1",
	}, assign)
	if !assignAck.Accepted {
		t.Fatalf("assign should be accepted, got code=%s", assignAck.ErrorCode)
	}

	routeSnapshot, exists := handler.routeRegistry.Get("route-1")
	if !exists {
		t.Fatalf("expected route snapshot exists after assign")
	}
	if routeSnapshot.Scope.Namespace != "dev" || routeSnapshot.Scope.Environment != "alice" {
		t.Fatalf("unexpected route scope: %+v", routeSnapshot.Scope)
	}
	if routeSnapshot.ScopeInjection.InjectPolicy != pb.ScopeInjectPolicyAlways {
		t.Fatalf("unexpected route scope injection policy: %+v", routeSnapshot.ScopeInjection)
	}

	dupAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 2,
		ResourceID:      "route-1",
	}, assign)
	if !dupAck.Accepted {
		t.Fatalf("duplicate assign should be accepted")
	}
	if dupAck.CurrentResourceVersion != 1 {
		t.Fatalf("unexpected current version: got=%d want=1", dupAck.CurrentResourceVersion)
	}

	revokeAck := handler.HandleRevoke(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteRevoke,
		SessionID:       "session-1",
		SessionEpoch:    2,
		EventID:         "evt-2",
		ResourceVersion: 2,
		ResourceID:      "route-1",
	}, pb.RouteRevoke{RouteID: "route-1"})
	if revokeAck.Accepted {
		t.Fatalf("stale epoch revoke should be rejected")
	}
	if revokeAck.ErrorCode != ltfperrors.CodeStaleEpochEvent {
		t.Fatalf("unexpected revoke error code: got=%s want=%s", revokeAck.ErrorCode, ltfperrors.CodeStaleEpochEvent)
	}
}

// TestRouteHandlerDerivesHostFromSelector 验证 RouteAssign 缺失 host 时会按 selector 派生。
func TestRouteHandlerDerivesHostFromSelector(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-route-derive",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		HostDeriver: routeHandlerTestHostDeriver{
			host: "orders.alice.dev.example.com",
		},
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-route-derive",
		SessionEpoch:    3,
		EventID:         "evt-route-derive",
		ResourceVersion: 1,
		ResourceID:      "route-derive",
	}, pb.RouteAssign{
		RouteID: "route-derive",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			PathPrefix: "/",
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
	if !assignAck.Accepted {
		t.Fatalf("expected derived route accepted, got error=%s", assignAck.ErrorCode)
	}

	routeSnapshot, exists := handler.routeRegistry.Get("route-derive")
	if !exists {
		t.Fatalf("expected derived route snapshot exists")
	}
	if routeSnapshot.Match.Host != "orders.alice.dev.example.com" {
		t.Fatalf("unexpected derived route host: %s", routeSnapshot.Match.Host)
	}
}

// TestRouteHandlerRejectMutationWhenSessionNotActive 验证非 ACTIVE 会话不能写入路由资源。
func TestRouteHandlerRejectMutationWhenSessionNotActive(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-draining",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionDraining,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-draining",
		SessionEpoch:    3,
		EventID:         "evt-draining-route",
		ResourceVersion: 1,
		ResourceID:      "route-draining",
	}, pb.RouteAssign{
		RouteID: "route-draining",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "alice",
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
	if assignAck.Accepted {
		t.Fatalf("expected draining session route assign rejected")
	}
	if assignAck.ErrorCode != ltfperrors.CodeInvalidStateTransition {
		t.Fatalf("unexpected error code: got=%s want=%s", assignAck.ErrorCode, ltfperrors.CodeInvalidStateTransition)
	}
}

// TestRouteHandlerRejectsMissingTargetType 验证 route.target.type 缺失时直接拒绝。
func TestRouteHandlerRejectsMissingTargetType(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-missing-target-type",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-missing-target-type",
		SessionEpoch:    3,
		EventID:         "evt-missing-target-type",
		ResourceVersion: 1,
		ResourceID:      "route-missing-target-type",
	}, pb.RouteAssign{
		RouteID: "route-missing-target-type",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if assignAck.Accepted {
		t.Fatalf("expected missing route target type rejected")
	}
	if assignAck.ErrorCode != ltfperrors.CodeUnsupportedValue {
		t.Fatalf("unexpected error code: got=%s want=%s", assignAck.ErrorCode, ltfperrors.CodeUnsupportedValue)
	}
}

// TestRouteHandlerRejectsConflictingRoute 验证相同匹配条件且不同目标会被 admission 拒绝。
func TestRouteHandlerRejectsConflictingRoute(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-conflict",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders",
		LogicalServiceID: "ls-orders",
		ConnectorID:      "connector-1",
		SessionID:        "session-conflict",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-payments",
		ServiceName:      "payments",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-payments",
		LogicalServiceID: "ls-payments",
		ConnectorID:      "connector-2",
		SessionID:        "session-conflict",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})
	metrics := obs.NewMetrics()
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		Metrics:         metrics,
	})

	firstAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-conflict",
		SessionEpoch:    3,
		EventID:         "evt-route-ok",
		ResourceVersion: 1,
		ResourceID:      "route-orders",
	}, pb.RouteAssign{
		RouteID: "route-orders",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected first route accepted, got=%s", firstAck.ErrorCode)
	}

	conflictAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-conflict",
		SessionEpoch:    3,
		EventID:         "evt-route-conflict",
		ResourceVersion: 2,
		ResourceID:      "route-payments",
	}, pb.RouteAssign{
		RouteID: "route-payments",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "payments"},
			},
		},
	})
	if conflictAck.Accepted {
		t.Fatalf("expected conflicting route rejected")
	}
	if conflictAck.ErrorCode != ltfperrors.CodeIngressRouteMismatch {
		t.Fatalf("unexpected conflict error code: got=%s want=%s", conflictAck.ErrorCode, ltfperrors.CodeIngressRouteMismatch)
	}
	if conflictAck.Metadata["conflict_route_id"] != "route-orders" {
		t.Fatalf("unexpected conflict metadata: %+v", conflictAck.Metadata)
	}
	if metrics.BridgeRouteConflictRejectionTotal() != 1 {
		t.Fatalf("unexpected conflict rejection total: %d", metrics.BridgeRouteConflictRejectionTotal())
	}
}

// TestRouteHandlerRejectsConflictingRouteAcrossScopeWithAlwaysInjection 验证 always 注入下跨 scope 的相同入口也会冲突。
func TestRouteHandlerRejectsConflictingRouteAcrossScopeWithAlwaysInjection(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-conflict-scope-injection",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders-prod",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "prod", Environment: "main"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders-prod",
		LogicalServiceID: "ls-orders-prod",
		ConnectorID:      "connector-1",
		SessionID:        "session-conflict-scope-injection",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-payments-dev",
		ServiceName:      "payments",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-payments-dev",
		LogicalServiceID: "ls-payments-dev",
		ConnectorID:      "connector-2",
		SessionID:        "session-conflict-scope-injection",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})
	metrics := obs.NewMetrics()
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		Metrics:         metrics,
	})

	firstAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-conflict-scope-injection",
		SessionEpoch:    3,
		EventID:         "evt-route-prod-ok",
		ResourceVersion: 1,
		ResourceID:      "route-orders-prod",
	}, pb.RouteAssign{
		RouteID: "route-orders-prod",
		Scope:   pb.Scope{Namespace: "prod", Environment: "main"},
		ScopeInjection: pb.ScopeInjection{
			InjectScope:  pb.Scope{Namespace: "prod", Environment: "main"},
			InjectPolicy: pb.ScopeInjectPolicyAlways,
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.shared.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected first route accepted, got=%s", firstAck.ErrorCode)
	}

	conflictAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-conflict-scope-injection",
		SessionEpoch:    3,
		EventID:         "evt-route-dev-conflict",
		ResourceVersion: 2,
		ResourceID:      "route-payments-dev",
	}, pb.RouteAssign{
		RouteID: "route-payments-dev",
		Scope:   pb.Scope{Namespace: "dev", Environment: "alice"},
		ScopeInjection: pb.ScopeInjection{
			InjectScope:  pb.Scope{Namespace: "dev", Environment: "alice"},
			InjectPolicy: pb.ScopeInjectPolicyAlways,
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.shared.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "payments"},
			},
		},
	})
	if conflictAck.Accepted {
		t.Fatalf("expected conflicting route rejected")
	}
	if conflictAck.ErrorCode != ltfperrors.CodeIngressRouteMismatch {
		t.Fatalf("unexpected conflict error code: got=%s want=%s", conflictAck.ErrorCode, ltfperrors.CodeIngressRouteMismatch)
	}
	if conflictAck.Metadata["conflict_route_id"] != "route-orders-prod" {
		t.Fatalf("unexpected conflict metadata: %+v", conflictAck.Metadata)
	}
	if metrics.BridgeRouteConflictRejectionTotal() != 1 {
		t.Fatalf("unexpected conflict rejection total: %d", metrics.BridgeRouteConflictRejectionTotal())
	}
}

// TestRouteHandlerReturnsShadowWarningOnPriorityDifference 验证不同 priority 仅返回 warning，不阻断注册。
func TestRouteHandlerReturnsShadowWarningOnPriorityDifference(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-shadow",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders",
		LogicalServiceID: "ls-orders",
		ConnectorID:      "connector-1",
		SessionID:        "session-shadow",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-payments",
		ServiceName:      "payments",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-payments",
		LogicalServiceID: "ls-payments",
		ConnectorID:      "connector-2",
		SessionID:        "session-shadow",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	if !handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-shadow",
		SessionEpoch:    3,
		EventID:         "evt-shadow-1",
		ResourceVersion: 1,
		ResourceID:      "route-high",
	}, pb.RouteAssign{
		RouteID:  "route-high",
		Scope:    pb.Scope{Namespace: "dev", Environment: "demo"},
		Priority: 100,
		Match:    pb.RouteMatch{Protocol: "http", Host: "api.dev.example.com", PathPrefix: "/orders"},
		Target:   pb.RouteTarget{Type: pb.RouteTargetTypeConnectorService, ConnectorService: &pb.ConnectorServiceTarget{Selector: pb.ServiceSelector{ServiceName: "orders"}}},
	}).Accepted {
		t.Fatalf("expected high priority route accepted")
	}

	shadowAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-shadow",
		SessionEpoch:    3,
		EventID:         "evt-shadow-2",
		ResourceVersion: 2,
		ResourceID:      "route-low",
	}, pb.RouteAssign{
		RouteID:  "route-low",
		Scope:    pb.Scope{Namespace: "dev", Environment: "demo"},
		Priority: 10,
		Match:    pb.RouteMatch{Protocol: "http", Host: "api.dev.example.com", PathPrefix: "/orders"},
		Target:   pb.RouteTarget{Type: pb.RouteTargetTypeConnectorService, ConnectorService: &pb.ConnectorServiceTarget{Selector: pb.ServiceSelector{ServiceName: "payments"}}},
	})
	if !shadowAck.Accepted {
		t.Fatalf("expected lower priority route accepted with warning, got=%s", shadowAck.ErrorCode)
	}
	if len(shadowAck.Warnings) != 1 {
		t.Fatalf("expected one warning, got=%v", shadowAck.Warnings)
	}
}

// TestRouteHandlerReturnsShadowWarningAcrossScopeWithAlwaysInjection 验证 always 注入下跨 scope 且 priority 不同仅返回 warning。
func TestRouteHandlerReturnsShadowWarningAcrossScopeWithAlwaysInjection(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-shadow-scope-injection",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders-prod",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "prod", Environment: "main"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders-prod",
		LogicalServiceID: "ls-orders-prod",
		ConnectorID:      "connector-1",
		SessionID:        "session-shadow-scope-injection",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-payments-dev",
		ServiceName:      "payments",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-payments-dev",
		LogicalServiceID: "ls-payments-dev",
		ConnectorID:      "connector-2",
		SessionID:        "session-shadow-scope-injection",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	if !handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-shadow-scope-injection",
		SessionEpoch:    3,
		EventID:         "evt-shadow-scope-injection-1",
		ResourceVersion: 1,
		ResourceID:      "route-high-prod",
	}, pb.RouteAssign{
		RouteID:  "route-high-prod",
		Scope:    pb.Scope{Namespace: "prod", Environment: "main"},
		Priority: 100,
		ScopeInjection: pb.ScopeInjection{
			InjectScope:  pb.Scope{Namespace: "prod", Environment: "main"},
			InjectPolicy: pb.ScopeInjectPolicyAlways,
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.shared.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	}).Accepted {
		t.Fatalf("expected high priority route accepted")
	}

	shadowAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-shadow-scope-injection",
		SessionEpoch:    3,
		EventID:         "evt-shadow-scope-injection-2",
		ResourceVersion: 2,
		ResourceID:      "route-low-dev",
	}, pb.RouteAssign{
		RouteID:  "route-low-dev",
		Scope:    pb.Scope{Namespace: "dev", Environment: "alice"},
		Priority: 10,
		ScopeInjection: pb.ScopeInjection{
			InjectScope:  pb.Scope{Namespace: "dev", Environment: "alice"},
			InjectPolicy: pb.ScopeInjectPolicyAlways,
		},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.shared.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "payments"},
			},
		},
	})
	if !shadowAck.Accepted {
		t.Fatalf("expected lower priority route accepted with warning, got=%s", shadowAck.ErrorCode)
	}
	if len(shadowAck.Warnings) != 1 {
		t.Fatalf("expected one warning, got=%v", shadowAck.Warnings)
	}
}

// TestRouteHandlerAllowsIdenticalMatchAcrossScopes 验证 fallback scope 可复用相同匹配条件而不被 admission 误判冲突。
func TestRouteHandlerAllowsIdenticalMatchAcrossScopes(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-scope-fallback",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders-alice",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  1,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders-alice",
		LogicalServiceID: "ls-orders-alice",
		ConnectorID:      "connector-1",
		SessionID:        "session-scope-fallback",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-orders-base",
		ServiceName:      "orders",
		Scope:            pb.Scope{Namespace: "dev", Environment: "base"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  2,
	}, pb.ServiceInstance{
		InstanceID:       "inst-orders-base",
		LogicalServiceID: "ls-orders-base",
		ConnectorID:      "connector-1",
		SessionID:        "session-scope-fallback",
		SessionEpoch:     3,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	firstAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-scope-fallback",
		SessionEpoch:    3,
		EventID:         "evt-scope-primary",
		ResourceVersion: 1,
		ResourceID:      "route-orders-alice",
	}, pb.RouteAssign{
		RouteID: "route-orders-alice",
		Scope:   pb.Scope{Namespace: "dev", Environment: "alice"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected primary scope route accepted, got=%s", firstAck.ErrorCode)
	}

	fallbackAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-scope-fallback",
		SessionEpoch:    3,
		EventID:         "evt-scope-fallback",
		ResourceVersion: 2,
		ResourceID:      "route-orders-base",
	}, pb.RouteAssign{
		RouteID: "route-orders-base",
		Scope:   pb.Scope{Namespace: "dev", Environment: "base"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if !fallbackAck.Accepted {
		t.Fatalf("expected fallback scope route accepted, got=%s metadata=%+v", fallbackAck.ErrorCode, fallbackAck.Metadata)
	}
	if len(fallbackAck.Warnings) != 0 {
		t.Fatalf("expected fallback scope route without warnings, got=%v", fallbackAck.Warnings)
	}
}

// TestRouteHandlerRejectsInvalidHeaderRegex 验证非法 header regex 会在 RouteAssign 阶段被拒绝。
func TestRouteHandlerRejectsInvalidHeaderRegex(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-invalid-header-regex",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-invalid-header-regex",
		SessionEpoch:    3,
		EventID:         "evt-invalid-header-regex",
		ResourceVersion: 1,
		ResourceID:      "route-invalid-header-regex",
	}, pb.RouteAssign{
		RouteID: "route-invalid-header-regex",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
			Headers: []pb.HeaderMatcher{
				{Name: "x-user-id", Regex: "("},
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if assignAck.Accepted {
		t.Fatalf("expected invalid header regex route rejected")
	}
	if assignAck.ErrorCode != ltfperrors.CodeUnsupportedValue {
		t.Fatalf("unexpected error code: got=%s want=%s", assignAck.ErrorCode, ltfperrors.CodeUnsupportedValue)
	}
	if _, exists := handler.routeRegistry.Get("route-invalid-header-regex"); exists {
		t.Fatalf("invalid header regex route should not be persisted")
	}
}

// TestRouteHandlerRejectsReservedScopeHeaderMatcher 验证 RouteAssign 不允许匹配保留 scope header。
func TestRouteHandlerRejectsReservedScopeHeaderMatcher(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-reserved-scope-header",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-reserved-scope-header",
		SessionEpoch:    3,
		EventID:         "evt-reserved-scope-header",
		ResourceVersion: 1,
		ResourceID:      "route-reserved-scope-header",
	}, pb.RouteAssign{
		RouteID: "route-reserved-scope-header",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
			Headers: []pb.HeaderMatcher{
				{Name: "X-Bridge-Namespace", Exact: "dev"},
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if assignAck.Accepted {
		t.Fatalf("expected reserved scope header matcher route rejected")
	}
	if assignAck.ErrorCode != ltfperrors.CodeUnsupportedValue {
		t.Fatalf("unexpected error code: got=%s want=%s", assignAck.ErrorCode, ltfperrors.CodeUnsupportedValue)
	}
	if _, exists := handler.routeRegistry.Get("route-reserved-scope-header"); exists {
		t.Fatalf("reserved scope header matcher route should not be persisted")
	}
}

// TestRouteHandlerRejectsInvalidQueryRegex 验证非法 query regex 会在 RouteAssign 阶段被拒绝。
func TestRouteHandlerRejectsInvalidQueryRegex(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-invalid-query-regex",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
	})

	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-invalid-query-regex",
		SessionEpoch:    3,
		EventID:         "evt-invalid-query-regex",
		ResourceVersion: 1,
		ResourceID:      "route-invalid-query-regex",
	}, pb.RouteAssign{
		RouteID: "route-invalid-query-regex",
		Scope:   pb.Scope{Namespace: "dev", Environment: "demo"},
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.example.com",
			PathPrefix: "/orders",
			Queries: []pb.QueryMatcher{
				{Name: "uid", Regex: "["},
			},
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{ServiceName: "orders"},
			},
		},
	})
	if assignAck.Accepted {
		t.Fatalf("expected invalid query regex route rejected")
	}
	if assignAck.ErrorCode != ltfperrors.CodeUnsupportedValue {
		t.Fatalf("unexpected error code: got=%s want=%s", assignAck.ErrorCode, ltfperrors.CodeUnsupportedValue)
	}
	if _, exists := handler.routeRegistry.Get("route-invalid-query-regex"); exists {
		t.Fatalf("invalid query regex route should not be persisted")
	}
}
