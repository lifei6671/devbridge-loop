package registry

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestServiceRegistryUpsertReplaceServiceKeyAlias 验证 serviceKey 变更时旧别名会被清理。
func TestServiceRegistryUpsertReplaceServiceKeyAlias(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, pb.Service{
		ServiceID:       "svc-1",
		ServiceKey:      "dev/alice/old-key",
		ResourceVersion: 1,
	})
	registry.Upsert(now.Add(time.Second), pb.Service{
		ServiceID:       "svc-1",
		ServiceKey:      "dev/alice/new-key",
		ResourceVersion: 2,
	})

	if _, exists := registry.GetByServiceKey("dev/alice/old-key"); exists {
		testingObject.Fatalf("expected old service key alias removed")
	}
	service, exists := registry.GetByServiceKey("dev/alice/new-key")
	if !exists {
		testingObject.Fatalf("expected new service key alias exists")
	}
	if service.ServiceID != "svc-1" {
		testingObject.Fatalf("unexpected service ID: got=%s want=svc-1", service.ServiceID)
	}
	if removed := registry.RemoveByServiceKey("dev/alice/old-key"); removed {
		testingObject.Fatalf("expected remove by old key returns false")
	}
}

// TestServiceRegistryMarkLifecycleByConnector 验证按 connector 批量更新服务生命周期状态。
func TestServiceRegistryMarkLifecycleByConnector(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, pb.Service{
		ServiceID:    "svc-1",
		ServiceKey:   "dev/alice/order-service",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	registry.Upsert(now, pb.Service{
		ServiceID:    "svc-2",
		ServiceKey:   "dev/alice/pay-service",
		ConnectorID:  "connector-2",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})

	updatedCount := registry.MarkLifecycleByConnector(
		now.Add(time.Second),
		"connector-1",
		pb.ServiceStatusStale,
		pb.HealthStatusUnknown,
	)
	if updatedCount != 1 {
		testingObject.Fatalf("unexpected updated count: got=%d want=1", updatedCount)
	}
	serviceOne, exists := registry.GetByServiceID("svc-1")
	if !exists {
		testingObject.Fatalf("expected svc-1 exists")
	}
	if serviceOne.Status != pb.ServiceStatusStale || serviceOne.HealthStatus != pb.HealthStatusUnknown {
		testingObject.Fatalf(
			"unexpected svc-1 lifecycle: status=%s health=%s",
			serviceOne.Status,
			serviceOne.HealthStatus,
		)
	}
	serviceTwo, exists := registry.GetByServiceID("svc-2")
	if !exists {
		testingObject.Fatalf("expected svc-2 exists")
	}
	if serviceTwo.Status != pb.ServiceStatusActive || serviceTwo.HealthStatus != pb.HealthStatusHealthy {
		testingObject.Fatalf(
			"unexpected svc-2 lifecycle: status=%s health=%s",
			serviceTwo.Status,
			serviceTwo.HealthStatus,
		)
	}
}
