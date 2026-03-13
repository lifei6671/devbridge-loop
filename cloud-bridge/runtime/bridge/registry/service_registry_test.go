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
