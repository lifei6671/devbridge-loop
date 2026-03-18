package routing

import (
	"testing"
	"time"
)

// TestTrafficAffinityStoreLoadStore 验证同一 traffic_id 可读取到已写入的实例映射。
func TestTrafficAffinityStoreLoadStore(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	store := newTrafficAffinityStore(time.Minute, 32)
	store.Store("traffic-1", "svcinst:a", now)
	instanceID, exists := store.Load("traffic-1", now.Add(10*time.Second))
	if !exists {
		testingObject.Fatalf("expected traffic affinity entry exists")
	}
	if instanceID != "svcinst:a" {
		testingObject.Fatalf("unexpected service_instance_id: got=%s want=svcinst:a", instanceID)
	}
}

// TestTrafficAffinityStoreExpiredRecord 验证过期粘性记录会被自动清理并返回未命中。
func TestTrafficAffinityStoreExpiredRecord(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	store := newTrafficAffinityStore(2*time.Second, 32)
	store.Store("traffic-1", "svcinst:a", now)
	if _, exists := store.Load("traffic-1", now.Add(3*time.Second)); exists {
		testingObject.Fatalf("expected expired traffic affinity entry removed")
	}
}

// TestTrafficAffinityStoreCapacityEviction 验证容量达到上限时会淘汰最早过期记录。
func TestTrafficAffinityStoreCapacityEviction(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	store := newTrafficAffinityStore(time.Minute, 2)
	store.Store("traffic-1", "svcinst:a", now)
	store.Store("traffic-2", "svcinst:b", now.Add(time.Second))
	store.Store("traffic-3", "svcinst:c", now.Add(2*time.Second))
	if _, exists := store.Load("traffic-1", now.Add(2*time.Second)); exists {
		testingObject.Fatalf("expected oldest traffic affinity entry evicted")
	}
	if instanceID, exists := store.Load("traffic-3", now.Add(2*time.Second)); !exists || instanceID != "svcinst:c" {
		testingObject.Fatalf("expected latest entry retained, got id=%s exists=%v", instanceID, exists)
	}
}
