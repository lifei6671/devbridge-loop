package app

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestTrafficOwnershipStoreObserveAndLoad 验证 traffic 归属索引可写入并查询最新记录。
func TestTrafficOwnershipStoreObserveAndLoad(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	currentTime := now
	store := newTrafficOwnershipStore(5*time.Minute, 8, func() time.Time { return currentTime })

	store.Observe(trafficOwnershipRecord{
		TrafficID:        "traffic-1",
		RouteID:          "route-1",
		LogicalServiceID: "ls-1",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
		InstanceID:       "inst-1",
		ConnectorID:      "connector-1",
		SessionID:        "session-1",
	})
	record, exists := store.Load("traffic-1")
	if !exists {
		testingObject.Fatalf("expected ownership record exists")
	}
	if record.LogicalServiceID != "ls-1" || record.InstanceID == "" {
		testingObject.Fatalf("unexpected ownership record: %+v", record)
	}
	if record.UpdatedAt.IsZero() {
		testingObject.Fatalf("expected updated_at populated")
	}

	// 同一 traffic_id 覆盖写入后应返回最新版本。
	currentTime = now.Add(time.Minute)
	store.Observe(trafficOwnershipRecord{
		TrafficID:        "traffic-1",
		RouteID:          "route-2",
		LogicalServiceID: "ls-2",
		InstanceID:       "inst-2",
	})
	record, exists = store.Load("traffic-1")
	if !exists {
		testingObject.Fatalf("expected ownership record exists after overwrite")
	}
	if record.RouteID != "route-2" || record.LogicalServiceID != "ls-2" {
		testingObject.Fatalf("expected latest ownership record returned, got=%+v", record)
	}
}

// TestTrafficOwnershipStoreTTLAndCapacity 验证索引会按 TTL 过期并按容量驱逐。
func TestTrafficOwnershipStoreTTLAndCapacity(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	currentTime := now
	store := newTrafficOwnershipStore(time.Minute, 2, func() time.Time { return currentTime })

	store.Observe(trafficOwnershipRecord{TrafficID: "traffic-a", LogicalServiceID: "ls-a"})
	currentTime = currentTime.Add(30 * time.Second)
	store.Observe(trafficOwnershipRecord{TrafficID: "traffic-b", LogicalServiceID: "ls-b"})
	currentTime = currentTime.Add(30 * time.Second)
	store.Observe(trafficOwnershipRecord{TrafficID: "traffic-c", LogicalServiceID: "ls-c"})
	if _, exists := store.Load("traffic-a"); exists {
		testingObject.Fatalf("expected oldest record evicted by capacity")
	}

	currentTime = currentTime.Add(2 * time.Minute)
	if _, exists := store.Load("traffic-b"); exists {
		testingObject.Fatalf("expected record expired by ttl")
	}
	if _, exists := store.Load("traffic-c"); exists {
		testingObject.Fatalf("expected record expired by ttl")
	}
}
