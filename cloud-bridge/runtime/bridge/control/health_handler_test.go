package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestHealthHandlerHandleReport 验证健康上报可更新实例状态。
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
	upsertInstance(serviceRegistry, "ls-1", "inst-1", "order-service", "connector-1", "session-1", 4, 1, pb.HealthStatusUnknown)
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
		ConnectorID:  "connector-1",
	}, pb.ServiceHealthReport{
		InstanceID:          "inst-1",
		LogicalServiceID:    "ls-1",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	instanceSnapshot, exists := serviceRegistry.GetInstanceByID("inst-1")
	if !exists {
		t.Fatalf("expected instance snapshot exists")
	}
	if instanceSnapshot.Instance.HealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected health status: got=%s want=%s", instanceSnapshot.Instance.HealthStatus, pb.HealthStatusHealthy)
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
	upsertInstance(serviceRegistry, "ls-2", "inst-2", "pay-service", "connector-1", "session-1", 6, 3, pb.HealthStatusUnknown)
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
		ConnectorID:  "connector-1",
	}, pb.ServiceHealthReport{
		InstanceID:          "inst-2",
		LogicalServiceID:    "ls-2",
		ServiceHealthStatus: pb.HealthStatusUnhealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	instanceSnapshot, exists := serviceRegistry.GetInstanceByID("inst-2")
	if !exists {
		t.Fatalf("expected instance snapshot exists")
	}
	if instanceSnapshot.Instance.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("stale epoch should be ignored, got=%s", instanceSnapshot.Instance.HealthStatus)
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
	upsertInstance(serviceRegistry, "ls-draining", "inst-draining", "pay-service", "connector-1", "session-draining", 7, 3, pb.HealthStatusUnknown)
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
		ConnectorID:  "connector-1",
	}, pb.ServiceHealthReport{
		InstanceID:          "inst-draining",
		LogicalServiceID:    "ls-draining",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	instanceSnapshot, exists := serviceRegistry.GetInstanceByID("inst-draining")
	if !exists {
		t.Fatalf("expected instance snapshot exists")
	}
	if instanceSnapshot.Instance.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("draining session should be ignored, got=%s", instanceSnapshot.Instance.HealthStatus)
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
	upsertInstance(serviceRegistry, "ls-shared", "inst-a", "order-service", "connector-a", "session-a", 8, 1, pb.HealthStatusUnknown)
	upsertInstance(serviceRegistry, "ls-shared", "inst-b", "order-service", "connector-b", "session-b", 9, 2, pb.HealthStatusUnknown)
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
		InstanceID:          "inst-a",
		LogicalServiceID:    "ls-shared",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	instanceA, exists := serviceRegistry.GetInstanceByID("inst-a")
	if !exists {
		t.Fatalf("expected inst-a exists")
	}
	instanceB, exists := serviceRegistry.GetInstanceByID("inst-b")
	if !exists {
		t.Fatalf("expected inst-b exists")
	}
	if instanceA.Instance.HealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("expected inst-a updated to healthy, got=%s", instanceA.Instance.HealthStatus)
	}
	if instanceB.Instance.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("expected inst-b remains unknown, got=%s", instanceB.Instance.HealthStatus)
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
	upsertInstance(serviceRegistry, "ls-availability", "inst-availability-a", "availability-service", "connector-a", "session-a", 10, 1, pb.HealthStatusUnknown)
	upsertInstance(serviceRegistry, "ls-availability", "inst-availability-b", "availability-service", "connector-b", "session-b", 11, 2, pb.HealthStatusUnknown)
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
		InstanceID:          "inst-availability-a",
		LogicalServiceID:    "ls-availability",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	})

	if metrics.BridgeServiceAvailableInstanceTotal("ls-availability") != 1 {
		t.Fatalf(
			"unexpected service available instance total: got=%d want=1",
			metrics.BridgeServiceAvailableInstanceTotal("ls-availability"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("ls-availability", "inst-availability-a") != 1 {
		t.Fatalf(
			"expected instance-a available metric is 1, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("ls-availability", "inst-availability-a"),
		)
	}
	if metrics.BridgeServiceInstanceAvailableTotal("ls-availability", "inst-availability-b") != 0 {
		t.Fatalf(
			"expected instance-b available metric is 0, got=%d",
			metrics.BridgeServiceInstanceAvailableTotal("ls-availability", "inst-availability-b"),
		)
	}
}

func upsertInstance(
	serviceRegistry *registry.ServiceRegistry,
	logicalServiceID string,
	instanceID string,
	serviceName string,
	connectorID string,
	sessionID string,
	sessionEpoch uint64,
	resourceVersion uint64,
	healthStatus pb.HealthStatus,
) {
	serviceRegistry.Upsert(time.Now().UTC(), pb.LogicalService{
		LogicalServiceID: logicalServiceID,
		ServiceName:      serviceName,
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Status:          pb.ServiceStatusActive,
		ResourceVersion: resourceVersion,
	}, pb.ServiceInstance{
		InstanceID:       instanceID,
		LogicalServiceID: logicalServiceID,
		ConnectorID:      connectorID,
		SessionID:        sessionID,
		SessionEpoch:     sessionEpoch,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     healthStatus,
		ResourceVersion:  resourceVersion,
	})
}
