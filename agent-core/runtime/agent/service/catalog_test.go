package service

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCatalogUpsertAndList 验证目录可维护稳定 service_key/service_id 映射。
func TestCatalogUpsertAndList(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	record := catalog.Upsert(now, adapter.LocalRegistration{
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if record.Registration.ServiceID == "" {
		t.Fatalf("expected service_id to be resolved")
	}
	if record.HealthStatus != pb.HealthStatusUnknown {
		t.Fatalf("expected default health status unknown, got=%s", record.HealthStatus)
	}

	list := catalog.List()
	if len(list) != 1 {
		t.Fatalf("unexpected service count: got=%d want=1", len(list))
	}
	if list[0].Registration.ServiceKey != "dev/demo/order-service" {
		t.Fatalf("unexpected service_key: %s", list[0].Registration.ServiceKey)
	}
	if list[0].Registration.ServiceID != record.Registration.ServiceID {
		t.Fatalf("service_id mismatch: got=%s want=%s", list[0].Registration.ServiceID, record.Registration.ServiceID)
	}
}

// TestCatalogSetServiceIDByKey 验证可基于 service_key 回写稳定 service_id。
func TestCatalogSetServiceIDByKey(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	catalog.Upsert(now, adapter.LocalRegistration{
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ok := catalog.SetServiceIDByKey(now.Add(time.Second), "dev/demo/order-service", "svc-1001"); !ok {
		t.Fatalf("expected set service id by key success")
	}

	list := catalog.List()
	if len(list) != 1 {
		t.Fatalf("unexpected service count: got=%d want=1", len(list))
	}
	if list[0].Registration.ServiceID != "svc-1001" {
		t.Fatalf("unexpected rewritten service_id: %s", list[0].Registration.ServiceID)
	}
}

// TestCatalogUpdateHealth 验证目录可维护 service 粒度健康状态。
func TestCatalogUpdateHealth(t *testing.T) {
	t.Parallel()

	catalog := NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	catalog.Upsert(now, adapter.LocalRegistration{
		ServiceID:   "svc-2001",
		ServiceKey:  "dev/demo/pay-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "pay-service",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	updated := catalog.UpdateHealth(
		now.Add(2*time.Second),
		"svc-2001",
		"dev/demo/pay-service",
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
