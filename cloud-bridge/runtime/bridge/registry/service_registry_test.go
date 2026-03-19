package registry

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestServiceRegistryFindLogicalServiceByNameScope 验证 name+scope 到 logicalServiceID 映射查询。
func TestServiceRegistryFindLogicalServiceByNameScope(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	logicalService := pb.LogicalService{
		LogicalServiceID: "ls-1",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		ResourceVersion:  1,
	}
	instance := pb.ServiceInstance{
		InstanceID:       "si-1",
		LogicalServiceID: "ls-1",
		ConnectorID:      "connector-1",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	}
	registry.Upsert(now, logicalService, instance)

	loaded, exists := registry.FindLogicalServiceByNameScope("order-service", pb.Scope{Namespace: "dev", Environment: "alice"})
	if !exists {
		testingObject.Fatalf("expected logical service exists")
	}
	if loaded.LogicalServiceID != "ls-1" {
		testingObject.Fatalf("unexpected logical service id: got=%s want=ls-1", loaded.LogicalServiceID)
	}
}

// TestServiceRegistryMarkLifecycleByConnector 验证按 connector 批量更新实例生命周期状态。
func TestServiceRegistryMarkLifecycleByConnector(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-1",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
	}, pb.ServiceInstance{
		InstanceID:       "si-1",
		LogicalServiceID: "ls-1",
		ConnectorID:      "connector-1",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})
	registry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-2",
		ServiceName:      "pay-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
	}, pb.ServiceInstance{
		InstanceID:       "si-2",
		LogicalServiceID: "ls-2",
		ConnectorID:      "connector-2",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
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
	instanceOne, exists := registry.GetInstanceByID("si-1")
	if !exists {
		testingObject.Fatalf("expected si-1 exists")
	}
	if instanceOne.Instance.InstanceStatus != pb.ServiceStatusStale || instanceOne.Instance.HealthStatus != pb.HealthStatusUnknown {
		testingObject.Fatalf(
			"unexpected si-1 lifecycle: status=%s health=%s",
			instanceOne.Instance.InstanceStatus,
			instanceOne.Instance.HealthStatus,
		)
	}
	instanceTwo, exists := registry.GetInstanceByID("si-2")
	if !exists {
		testingObject.Fatalf("expected si-2 exists")
	}
	if instanceTwo.Instance.InstanceStatus != pb.ServiceStatusActive || instanceTwo.Instance.HealthStatus != pb.HealthStatusHealthy {
		testingObject.Fatalf(
			"unexpected si-2 lifecycle: status=%s health=%s",
			instanceTwo.Instance.InstanceStatus,
			instanceTwo.Instance.HealthStatus,
		)
	}
}

// TestServiceRegistryUpsertKeepsMultiInstancesInOnePool 验证同一 logicalService 可挂接多实例且不互相覆盖。
func TestServiceRegistryUpsertKeepsMultiInstancesInOnePool(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	logicalService := pb.LogicalService{
		LogicalServiceID: "ls-pool-1",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		ResourceVersion:  1,
	}
	registry.Upsert(now, logicalService, pb.ServiceInstance{
		InstanceID:       "si-a",
		LogicalServiceID: "ls-pool-1",
		ConnectorID:      "connector-a",
		SessionID:        "session-a",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  1,
	})
	registry.Upsert(now.Add(time.Second), logicalService, pb.ServiceInstance{
		InstanceID:       "si-b",
		LogicalServiceID: "ls-pool-1",
		ConnectorID:      "connector-b",
		SessionID:        "session-b",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  2,
	})

	if services := registry.List(); len(services) != 1 {
		testingObject.Fatalf("unexpected logical service count: got=%d want=1", len(services))
	}
	instances := registry.ListInstancesByLogicalServiceID("ls-pool-1")
	if len(instances) != 2 {
		testingObject.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	serviceByScope, exists := registry.FindLogicalServiceByNameScope("order-service", pb.Scope{Namespace: "dev", Environment: "alice"})
	if !exists {
		testingObject.Fatalf("expected logical service by scope exists")
	}
	if serviceByScope.LogicalServiceID != "ls-pool-1" {
		testingObject.Fatalf("unexpected logical_service_id by scope: got=%s want=ls-pool-1", serviceByScope.LogicalServiceID)
	}
}

// TestServiceRegistryRemoveInstanceByRuntime 验证按 connector/session 删除仅影响目标实例而不删除整个逻辑服务。
func TestServiceRegistryRemoveInstanceByRuntime(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	logicalService := pb.LogicalService{
		LogicalServiceID: "ls-pool-2",
		ServiceName:      "pay-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		ResourceVersion:  1,
	}
	registry.Upsert(now, logicalService, pb.ServiceInstance{
		InstanceID:       "si-a",
		LogicalServiceID: "ls-pool-2",
		ConnectorID:      "connector-a",
		SessionID:        "session-a",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})
	registry.Upsert(now.Add(time.Second), logicalService, pb.ServiceInstance{
		InstanceID:       "si-b",
		LogicalServiceID: "ls-pool-2",
		ConnectorID:      "connector-b",
		SessionID:        "session-b",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})

	removed := registry.RemoveInstanceByLogicalServiceAndRuntime("ls-pool-2", "connector-a", "session-a")
	if !removed {
		testingObject.Fatalf("expected remove target instance success")
	}
	if instances := registry.ListInstancesByLogicalServiceID("ls-pool-2"); len(instances) != 1 {
		testingObject.Fatalf("unexpected remaining instance count: got=%d want=1", len(instances))
	}
	removed = registry.RemoveInstanceByLogicalServiceAndRuntime("ls-pool-2", "connector-b", "session-b")
	if !removed {
		testingObject.Fatalf("expected remove last instance success")
	}
	if instances := registry.ListInstancesByLogicalServiceID("ls-pool-2"); len(instances) != 0 {
		testingObject.Fatalf("expected empty instance list after removing all instances, got=%d", len(instances))
	}
}

// TestServiceRegistryMarkLifecycleByConnectorAndSession 验证实例级生命周期收敛不会跨 session 误伤。
func TestServiceRegistryMarkLifecycleByConnectorAndSession(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewServiceRegistry()
	now := time.Now().UTC()
	logicalService := pb.LogicalService{
		LogicalServiceID: "ls-session-scope",
		ServiceName:      "inventory-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
	}
	registry.Upsert(now, logicalService, pb.ServiceInstance{
		InstanceID:       "si-old",
		LogicalServiceID: "ls-session-scope",
		ConnectorID:      "connector-1",
		SessionID:        "session-old",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})
	registry.Upsert(now.Add(time.Second), logicalService, pb.ServiceInstance{
		InstanceID:       "si-new",
		LogicalServiceID: "ls-session-scope",
		ConnectorID:      "connector-1",
		SessionID:        "session-new",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})

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

	instances := registry.ListInstancesByLogicalServiceID("ls-session-scope")
	if len(instances) != 2 {
		testingObject.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	instanceStatusBySession := make(map[string]pb.ServiceStatus, len(instances))
	for _, instance := range instances {
		instanceStatusBySession[instance.Instance.SessionID] = instance.Instance.InstanceStatus
	}
	if instanceStatusBySession["session-old"] != pb.ServiceStatusInactive {
		testingObject.Fatalf("unexpected old session status: %+v", instanceStatusBySession)
	}
	if instanceStatusBySession["session-new"] != pb.ServiceStatusActive {
		testingObject.Fatalf("unexpected new session status: %+v", instanceStatusBySession)
	}
}

// TestServiceRegistryListLogicalServiceIDsByRuntime 验证可按 connector/session 反查受影响 logical_service_id。
func TestServiceRegistryListLogicalServiceIDsByRuntime(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewServiceRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "ls-x-1",
		ServiceName:      "svc-1",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
	}, pb.ServiceInstance{
		InstanceID:       "si-x-1",
		LogicalServiceID: "ls-x-1",
		ConnectorID:      "connector-x",
		SessionID:        "session-x-1",
	})
	registry.Upsert(now.Add(time.Second), pb.LogicalService{
		LogicalServiceID: "ls-x-2",
		ServiceName:      "svc-2",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
	}, pb.ServiceInstance{
		InstanceID:       "si-x-2",
		LogicalServiceID: "ls-x-2",
		ConnectorID:      "connector-x",
		SessionID:        "session-x-2",
	})
	registry.Upsert(now.Add(2*time.Second), pb.LogicalService{
		LogicalServiceID: "ls-y-1",
		ServiceName:      "svc-3",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
	}, pb.ServiceInstance{
		InstanceID:       "si-y-1",
		LogicalServiceID: "ls-y-1",
		ConnectorID:      "connector-y",
		SessionID:        "session-y-1",
	})

	logicalServiceIDs := registry.ListLogicalServiceIDsByRuntime("connector-x", "session-x-1")
	if len(logicalServiceIDs) != 1 || logicalServiceIDs[0] != "ls-x-1" {
		testingObject.Fatalf("unexpected logical service ids by runtime: got=%v want=[ls-x-1]", logicalServiceIDs)
	}
	logicalServiceIDs = registry.ListLogicalServiceIDsByRuntime("connector-x", "")
	if len(logicalServiceIDs) != 2 {
		testingObject.Fatalf("unexpected logical service ids by connector: got=%v want=2 items", logicalServiceIDs)
	}
	if logicalServiceIDs = registry.ListLogicalServiceIDsByRuntime("", "session-x-1"); len(logicalServiceIDs) != 0 {
		testingObject.Fatalf("unexpected logical service ids for empty connector: got=%v want=[]", logicalServiceIDs)
	}
}
