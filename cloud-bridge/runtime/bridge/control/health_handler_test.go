package control

import (
	"testing"
	"time"

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
