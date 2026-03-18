package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestHealthHandlerHandleReport 验证健康上报可更新服务注册表状态。
func TestHealthHandlerHandleReport(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       4,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-1",
		ServiceKey:      "dev/demo/order-service",
		Namespace:       "dev",
		Environment:     "demo",
		ServiceName:     "order-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 1,
		HealthStatus:    pb.HealthStatusUnknown,
	})
	handler := NewHealthHandler(HealthHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})

	handler.HandleReport(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-1",
		SessionEpoch: 4,
	}, pb.ServiceHealthReport{
		ServiceID:           "svc-1",
		ServiceKey:          "dev/demo/order-service",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-1")
	if !exists {
		t.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.HealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected health status: got=%s want=%s", serviceSnapshot.HealthStatus, pb.HealthStatusHealthy)
	}
}

// TestHealthHandlerIgnoreStaleEpoch 验证旧会话代际上报不会覆盖最新状态。
func TestHealthHandlerIgnoreStaleEpoch(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       6,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-2",
		ServiceKey:      "dev/demo/pay-service",
		Namespace:       "dev",
		Environment:     "demo",
		ServiceName:     "pay-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 3,
		HealthStatus:    pb.HealthStatusUnknown,
	})
	handler := NewHealthHandler(HealthHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	handler.HandleReport(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-1",
		SessionEpoch: 5,
	}, pb.ServiceHealthReport{
		ServiceID:           "svc-2",
		ServiceKey:          "dev/demo/pay-service",
		ServiceHealthStatus: pb.HealthStatusUnhealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-2")
	if !exists {
		t.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("stale epoch should be ignored, got=%s", serviceSnapshot.HealthStatus)
	}
}

// TestHealthHandlerIgnoreNonActiveSession 验证 draining 会话健康上报不会覆盖状态。
func TestHealthHandlerIgnoreNonActiveSession(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-draining",
		ConnectorID: "connector-1",
		Epoch:       7,
		State:       registry.SessionDraining,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-draining",
		ServiceKey:      "dev/demo/pay-service",
		Namespace:       "dev",
		Environment:     "demo",
		ServiceName:     "pay-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 3,
		HealthStatus:    pb.HealthStatusUnknown,
	})
	handler := NewHealthHandler(HealthHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	handler.HandleReport(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-draining",
		SessionEpoch: 7,
	}, pb.ServiceHealthReport{
		ServiceID:           "svc-draining",
		ServiceKey:          "dev/demo/pay-service",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-draining")
	if !exists {
		t.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("draining session should be ignored, got=%s", serviceSnapshot.HealthStatus)
	}
}

// TestHealthHandlerUpdatesMatchedInstanceOnly 验证同池多实例场景下仅更新上报实例健康状态。
func TestHealthHandlerUpdatesMatchedInstanceOnly(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       8,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       9,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.UpsertWithRuntime(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-shared",
		ServiceKey:      "order-service/http",
		Namespace:       "dev",
		Environment:     "demo",
		ConnectorID:     "connector-a",
		ServiceName:     "order-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 1,
		HealthStatus:    pb.HealthStatusUnknown,
	}, "session-a")
	serviceRegistry.UpsertWithRuntime(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-shared",
		ServiceKey:      "order-service/http",
		Namespace:       "dev",
		Environment:     "demo",
		ConnectorID:     "connector-b",
		ServiceName:     "order-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 2,
		HealthStatus:    pb.HealthStatusUnknown,
	}, "session-b")
	handler := NewHealthHandler(HealthHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
	})

	handler.HandleReport(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-a",
		SessionEpoch: 8,
		ConnectorID:  "connector-a",
	}, pb.ServiceHealthReport{
		ServiceID:           "svc-shared",
		ServiceKey:          "order-service/http",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	instances := serviceRegistry.ListInstancesByServiceKey("order-service/http")
	if len(instances) != 2 {
		t.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	healthByConnector := map[string]pb.HealthStatus{}
	for _, instance := range instances {
		healthByConnector[instance.Service.ConnectorID] = instance.Service.HealthStatus
	}
	if healthByConnector["connector-a"] != pb.HealthStatusHealthy {
		t.Fatalf("expected connector-a health updated to healthy, got=%s", healthByConnector["connector-a"])
	}
	if healthByConnector["connector-b"] != pb.HealthStatusUnknown {
		t.Fatalf("expected connector-b health remains unknown, got=%s", healthByConnector["connector-b"])
	}
}

// TestHealthHandlerRefreshesServiceAvailabilityMetrics 验证健康更新会刷新服务池/实例可用性指标。
func TestHealthHandlerRefreshesServiceAvailabilityMetrics(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       10,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       11,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	instanceA := serviceRegistry.UpsertWithRuntime(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-availability",
		ServiceKey:      "availability-service/http",
		Namespace:       "dev",
		Environment:     "demo",
		ConnectorID:     "connector-a",
		ServiceName:     "availability-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 1,
		HealthStatus:    pb.HealthStatusUnknown,
	}, "session-a")
	instanceB := serviceRegistry.UpsertWithRuntime(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-availability",
		ServiceKey:      "availability-service/http",
		Namespace:       "dev",
		Environment:     "demo",
		ConnectorID:     "connector-b",
		ServiceName:     "availability-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 2,
		HealthStatus:    pb.HealthStatusUnknown,
	}, "session-b")
	metrics := obs.NewMetrics()
	handler := NewHealthHandler(HealthHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		Metrics:         metrics,
	})

	handler.HandleReport(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-a",
		SessionEpoch: 10,
		ConnectorID:  "connector-a",
	}, pb.ServiceHealthReport{
		ServiceID:           "svc-availability",
		ServiceKey:          "availability-service/http",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	if metrics.BridgeServiceAvailableInstanceTotal("svc-availability") != 1 {
		t.Fatalf(
			"unexpected service available instance total: got=%d want=1",
			metrics.BridgeServiceAvailableInstanceTotal("svc-availability"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-availability", instanceA) != 1 {
		t.Fatalf(
			"expected instance-a available metric is 1, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-availability", instanceA),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("svc-availability", instanceB) != 0 {
		t.Fatalf(
			"expected instance-b available metric is 0, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("svc-availability", instanceB),
		)
	}
}
