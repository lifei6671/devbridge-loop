package service

import (
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCatalogUpsertAndList 验证目录可维护稳定 scope/service_name 与 instance_id 映射。
func TestCatalogUpsertAndList(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	record := catalog.Upsert(now, adapter.LocalRegistration{
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if record.Registration.InstanceID == "" {
		t.Fatalf("expected instance_id to be resolved")
	}
	if !strings.HasPrefix(record.Registration.InstanceID, "order-service-http-") {
		t.Fatalf("unexpected generated instance_id prefix: %s", record.Registration.InstanceID)
	}
	suffix := strings.TrimPrefix(record.Registration.InstanceID, "order-service-http-")
	if len(suffix) != 11 {
		t.Fatalf("unexpected suffix length: got=%d want=11", len(suffix))
	}
	if record.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("expected default health status unknown, got=%s", record.HealthStatus)
	}

	list := catalog.List()
	if len(list) != 1 {
		t.Fatalf("unexpected service count: got=%d want=1", len(list))
	}
	if list[0].Registration.Scope.Namespace != "dev" || list[0].Registration.Scope.Environment != "demo" {
		t.Fatalf("unexpected scope: %+v", list[0].Registration.Scope)
	}
	if list[0].Registration.InstanceID != record.Registration.InstanceID {
		t.Fatalf("instance_id mismatch: got=%s want=%s", list[0].Registration.InstanceID, record.Registration.InstanceID)
	}
}

// TestCatalogApplyPublishIdentity 验证可基于 service_name + scope 回写稳定 logical_service_id。
func TestCatalogApplyPublishIdentity(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	record := catalog.Upsert(now, adapter.LocalRegistration{
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ok := catalog.ApplyPublishIdentity(
		now.Add(time.Second),
		"order-service",
		pb.Scope{Namespace: "dev", Environment: "demo"},
		"ls-1001",
		record.Registration.InstanceID,
	); !ok {
		t.Fatalf("expected apply publish identity success")
	}

	list := catalog.List()
	if len(list) != 1 {
		t.Fatalf("unexpected service count: got=%d want=1", len(list))
	}
	if list[0].Registration.LogicalServiceID != "ls-1001" {
		t.Fatalf("unexpected rewritten logical_service_id: %s", list[0].Registration.LogicalServiceID)
	}
}

// TestCatalogUpsertReuseMappedInstanceID 验证同一 scope/service_name 无显式 instance_id 时会复用既有映射。
func TestCatalogUpsertReuseMappedInstanceID(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	firstRecord := catalog.Upsert(now, adapter.LocalRegistration{
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if firstRecord.Registration.InstanceID == "" {
		t.Fatalf("expected first generated instance_id not empty")
	}
	secondRecord := catalog.Upsert(now.Add(time.Second), adapter.LocalRegistration{
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	if secondRecord.Registration.InstanceID != firstRecord.Registration.InstanceID {
		t.Fatalf(
			"expected instance_id mapping reused: got=%s want=%s",
			secondRecord.Registration.InstanceID,
			firstRecord.Registration.InstanceID,
		)
	}
}

// TestCatalogUpdateHealth 验证目录可维护 service 粒度健康状态。
func TestCatalogUpdateHealth(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	catalog.Upsert(now, adapter.LocalRegistration{
		LogicalServiceID: "ls-2001",
		InstanceID:       "inst-2001",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "pay-service",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	updated := catalog.UpdateHealth(
		now.Add(2*time.Second),
		"ls-2001",
		"inst-2001",
		pb.HealthStatusHealthy,
		[]pb.EndpointHealthStatus{
			{EndpointID: "ep-1", HealthStatus: pb.HealthStatusHealthy, Reason: "dial ok"},
		},
	)
	if !updated {
		t.Fatalf("expected update health success")
	}

	list := catalog.List()
	if len(list) != 1 {
		t.Fatalf("unexpected service count: got=%d want=1", len(list))
	}
	if list[0].HealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected health status: got=%s want=%s", list[0].HealthStatus, pb.HealthStatusHealthy)
	}
	if len(list[0].EndpointStatuses) != 1 {
		t.Fatalf("unexpected endpoint status count: got=%d want=1", len(list[0].EndpointStatuses))
	}
}
