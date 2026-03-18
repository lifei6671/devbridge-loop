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
		ServiceKey:      "old-service/http",
		ResourceVersion: 1,
	})
	registry.Upsert(now.Add(time.Second), pb.Service{
		ServiceID:       "svc-1",
		ServiceKey:      "new-service/http",
		ResourceVersion: 2,
	})

	if _, exists := registry.GetByServiceKey("old-service/http"); exists {
		testingObject.Fatalf("expected old service key alias removed")
	}
	service, exists := registry.GetByServiceKey("new-service/http")
	if !exists {
		testingObject.Fatalf("expected new service key alias exists")
	}
	if service.ServiceID != "svc-1" {
		testingObject.Fatalf("unexpected service ID: got=%s want=svc-1", service.ServiceID)
	}
	if removed := registry.RemoveByServiceKey("old-service/http"); removed {
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
		ServiceKey:   "order-service/http",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	registry.Upsert(now, pb.Service{
		ServiceID:    "svc-2",
		ServiceKey:   "pay-service/http",
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

// TestServiceRegistryUpsertWithRuntimeKeepsMultiInstancesInOnePool 验证同一 service_key/service_id 可挂接多实例且不互相覆盖。
func TestServiceRegistryUpsertWithRuntimeKeepsMultiInstancesInOnePool(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-pool-1",
		ServiceKey:   "order-service/http",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	registry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-pool-1",
		ServiceKey:   "order-service/http",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")

	// 池级列表保持一条逻辑服务，兼容旧接口语义。
	if services := registry.List(); len(services) != 1 {
		testingObject.Fatalf("unexpected service pool size: got=%d want=1", len(services))
	}
	instances := registry.ListInstancesByServiceKey("order-service/http")
	if len(instances) != 2 {
		testingObject.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	serviceByKey, exists := registry.GetByServiceKey("order-service/http")
	if !exists {
		testingObject.Fatalf("expected service by key exists")
	}
	if serviceByKey.ServiceID != "svc-pool-1" {
		testingObject.Fatalf("unexpected service_id by key: got=%s want=svc-pool-1", serviceByKey.ServiceID)
	}
}

// TestServiceRegistryUpsertWithRuntimeCollapsesLegacyEmptySessionInstance
// 验证 full-sync 产生的空 session 实例会在同 runtime 上报会话后被收敛。
func TestServiceRegistryUpsertWithRuntimeCollapsesLegacyEmptySessionInstance(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.ReplaceAll(now, []pb.Service{
		{
			ServiceID:       "svc-collapse",
			ServiceKey:      "billing-service/http",
			ConnectorID:     "connector-collapse",
			Status:          pb.ServiceStatusActive,
			HealthStatus:    pb.HealthStatusUnknown,
			ResourceVersion: 10,
		},
	})
	legacyInstances := registry.ListInstancesByServiceID("svc-collapse")
	if len(legacyInstances) != 1 {
		testingObject.Fatalf("unexpected legacy instance count: got=%d want=1", len(legacyInstances))
	}
	if legacyInstances[0].SessionID != "" {
		testingObject.Fatalf("expected legacy instance session_id empty, got=%s", legacyInstances[0].SessionID)
	}

	serviceInstanceID := registry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:       "svc-collapse",
		ServiceKey:      "billing-service/http",
		ConnectorID:     "connector-collapse",
		Status:          pb.ServiceStatusActive,
		HealthStatus:    pb.HealthStatusHealthy,
		ResourceVersion: 11,
	}, "session-collapse")
	if serviceInstanceID == "" {
		testingObject.Fatalf("expected service_instance_id not empty")
	}

	instances := registry.ListInstancesByServiceID("svc-collapse")
	if len(instances) != 1 {
		testingObject.Fatalf("unexpected collapsed instance count: got=%d want=1", len(instances))
	}
	if instances[0].SessionID != "session-collapse" {
		testingObject.Fatalf("expected session-bound instance only, got session=%s", instances[0].SessionID)
	}
	if instances[0].ServiceInstanceID != serviceInstanceID {
		testingObject.Fatalf(
			"unexpected service_instance_id after collapse: got=%s want=%s",
			instances[0].ServiceInstanceID,
			serviceInstanceID,
		)
	}
	if serviceIDs := registry.ListServiceIDsByRuntime("connector-collapse", "session-collapse"); len(serviceIDs) != 1 ||
		serviceIDs[0] != "svc-collapse" {
		testingObject.Fatalf("unexpected service ids by runtime after collapse: got=%v want=[svc-collapse]", serviceIDs)
	}
}

// TestServiceRegistryRemoveInstanceByRuntime 验证按 connector/session 删除仅影响目标实例而不删除整个服务池。
func TestServiceRegistryRemoveInstanceByRuntime(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-pool-2",
		ServiceKey:   "pay-service/http",
		ConnectorID:  "connector-a",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-a")
	registry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-pool-2",
		ServiceKey:   "pay-service/http",
		ConnectorID:  "connector-b",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-b")

	removed := registry.RemoveInstanceByServiceKeyAndRuntime("pay-service/http", "connector-a", "session-a")
	if !removed {
		testingObject.Fatalf("expected remove target instance success")
	}
	if instances := registry.ListInstancesByServiceID("svc-pool-2"); len(instances) != 1 {
		testingObject.Fatalf("unexpected remaining instance count: got=%d want=1", len(instances))
	}
	// 删除最后一个实例后，逻辑服务池应自动清理。
	removed = registry.RemoveInstanceByServiceIDAndRuntime("svc-pool-2", "connector-b", "session-b")
	if !removed {
		testingObject.Fatalf("expected remove last instance success")
	}
	if services := registry.List(); len(services) != 0 {
		testingObject.Fatalf("expected empty service pool after removing all instances, got=%d", len(services))
	}
}

// TestServiceRegistryMarkLifecycleByConnectorAndSession 验证实例级生命周期收敛不会跨 session 误伤。
func TestServiceRegistryMarkLifecycleByConnectorAndSession(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-session-scope",
		ServiceKey:   "inventory-service/http",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-old")
	registry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-session-scope",
		ServiceKey:   "inventory-service/http",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-new")

	updatedCount := registry.MarkLifecycleByConnectorAndSession(
		now.Add(2*time.Second),
		"connector-1",
		"session-old",
		pb.ServiceStatusInactive,
		pb.HealthStatusUnknown,
	)
	if updatedCount != 1 {
		testingObject.Fatalf("unexpected updated count: got=%d want=1", updatedCount)
	}

	instances := registry.ListInstancesByServiceID("svc-session-scope")
	if len(instances) != 2 {
		testingObject.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	instanceStatusBySession := make(map[string]pb.ServiceStatus, len(instances))
	instanceHealthBySession := make(map[string]pb.HealthStatus, len(instances))
	for _, instance := range instances {
		instanceStatusBySession[instance.SessionID] = instance.Service.Status
		instanceHealthBySession[instance.SessionID] = instance.Service.HealthStatus
	}
	if instanceStatusBySession["session-old"] != pb.ServiceStatusInactive ||
		instanceHealthBySession["session-old"] != pb.HealthStatusUnknown {
		testingObject.Fatalf(
			"unexpected old session lifecycle: status=%s health=%s",
			instanceStatusBySession["session-old"],
			instanceHealthBySession["session-old"],
		)
	}
	if instanceStatusBySession["session-new"] != pb.ServiceStatusActive ||
		instanceHealthBySession["session-new"] != pb.HealthStatusHealthy {
		testingObject.Fatalf(
			"unexpected new session lifecycle: status=%s health=%s",
			instanceStatusBySession["session-new"],
			instanceHealthBySession["session-new"],
		)
	}
}

// TestServiceRegistryMarkLifecycleByConnectorAndSessionFallbackLegacy 验证会话命中失败时仅回收空 session 的历史实例。
func TestServiceRegistryMarkLifecycleByConnectorAndSessionFallbackLegacy(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, pb.Service{
		ServiceID:    "svc-legacy",
		ServiceKey:   "legacy-service/http",
		ConnectorID:  "connector-legacy",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})

	updatedCount := registry.MarkLifecycleByConnectorAndSession(
		now.Add(time.Second),
		"connector-legacy",
		"session-not-found",
		pb.ServiceStatusStale,
		pb.HealthStatusUnknown,
	)
	if updatedCount != 1 {
		testingObject.Fatalf("unexpected updated count for legacy fallback: got=%d want=1", updatedCount)
	}
	serviceSnapshot, exists := registry.GetByServiceID("svc-legacy")
	if !exists {
		testingObject.Fatalf("expected legacy service exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusStale || serviceSnapshot.HealthStatus != pb.HealthStatusUnknown {
		testingObject.Fatalf(
			"unexpected legacy service lifecycle after fallback: status=%s health=%s",
			serviceSnapshot.Status,
			serviceSnapshot.HealthStatus,
		)
	}
}

// TestServiceRegistryListServiceIDsByRuntime 验证可按 connector/session 反查受影响 service_id。
func TestServiceRegistryListServiceIDsByRuntime(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-runtime-1",
		ServiceKey:   "runtime-1/http",
		ConnectorID:  "connector-x",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-x-1")
	registry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-runtime-1",
		ServiceKey:   "runtime-1/http",
		ConnectorID:  "connector-x",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-x-2")
	registry.UpsertWithRuntime(now.Add(2*time.Second), pb.Service{
		ServiceID:    "svc-runtime-2",
		ServiceKey:   "runtime-2/http",
		ConnectorID:  "connector-y",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-y-1")

	serviceIDs := registry.ListServiceIDsByRuntime("connector-x", "session-x-1")
	if len(serviceIDs) != 1 || serviceIDs[0] != "svc-runtime-1" {
		testingObject.Fatalf("unexpected service ids by runtime: got=%v want=[svc-runtime-1]", serviceIDs)
	}

	serviceIDs = registry.ListServiceIDsByRuntime("connector-x", "")
	if len(serviceIDs) != 1 || serviceIDs[0] != "svc-runtime-1" {
		testingObject.Fatalf("unexpected service ids by connector: got=%v want=[svc-runtime-1]", serviceIDs)
	}

	if serviceIDs = registry.ListServiceIDsByRuntime("", "session-x-1"); len(serviceIDs) != 0 {
		testingObject.Fatalf("expected empty result when connector is empty, got=%v", serviceIDs)
	}
}
