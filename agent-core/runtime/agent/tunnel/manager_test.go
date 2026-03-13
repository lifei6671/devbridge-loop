package tunnel

import (
	"context"
	"testing"
	"time"
)

// TestManagerPrebuildAndConsumeRefill 验证启动预建与消费后补建流程。
func TestManagerPrebuildAndConsumeRefill(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{}
	manager, err := NewManager(ManagerOptions{
		Config: ManagerConfig{
			MinIdle:           2,
			MaxIdle:           4,
			IdleTTL:           0,
			MaxInflightOpens:  2,
			TunnelOpenRate:    1000,
			TunnelOpenBurst:   1000,
			ReconcileInterval: time.Second,
		},
		Opener: opener,
	})
	if err != nil {
		testingObject.Fatalf("new manager failed: %v", err)
	}

	startupResult, err := manager.ReconcileNow(context.Background(), "startup")
	if err != nil {
		testingObject.Fatalf("startup reconcile failed: %v", err)
	}
	if startupResult.After.IdleCount != 2 {
		testingObject.Fatalf("unexpected startup idle count: %d", startupResult.After.IdleCount)
	}

	acquiredRecord, ok := manager.AcquireIdle(time.Now())
	if !ok {
		testingObject.Fatalf("expected acquire idle success")
	}
	if acquiredRecord.State != StateReserved {
		testingObject.Fatalf("unexpected acquired state: %s", acquiredRecord.State)
	}

	consumeResult, err := manager.ReconcileNow(context.Background(), "consume")
	if err != nil {
		testingObject.Fatalf("consume reconcile failed: %v", err)
	}
	if consumeResult.After.IdleCount != 2 {
		testingObject.Fatalf("unexpected idle after consume refill: %d", consumeResult.After.IdleCount)
	}
}

// TestManagerBrokenRemoveAndRefill 验证 broken 摘除后会触发补池。
func TestManagerBrokenRemoveAndRefill(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{}
	manager, err := NewManager(ManagerOptions{
		Config: ManagerConfig{
			MinIdle:           1,
			MaxIdle:           3,
			IdleTTL:           0,
			MaxInflightOpens:  1,
			TunnelOpenRate:    100,
			TunnelOpenBurst:   10,
			ReconcileInterval: time.Second,
		},
		Opener: opener,
	})
	if err != nil {
		testingObject.Fatalf("new manager failed: %v", err)
	}

	if _, err := manager.ReconcileNow(context.Background(), "startup"); err != nil {
		testingObject.Fatalf("startup reconcile failed: %v", err)
	}
	record, ok := manager.AcquireIdle(time.Now())
	if !ok {
		testingObject.Fatalf("expected acquire idle success")
	}
	if err := manager.MarkBrokenAndRemove(record.TunnelID, "read frame error"); err != nil {
		testingObject.Fatalf("mark broken and remove failed: %v", err)
	}
	if _, exists := manager.registry.Get(record.TunnelID); exists {
		testingObject.Fatalf("expected broken tunnel removed")
	}

	result, err := manager.ReconcileNow(context.Background(), "after_broken")
	if err != nil {
		testingObject.Fatalf("reconcile after broken failed: %v", err)
	}
	if result.After.IdleCount != 1 {
		testingObject.Fatalf("unexpected idle count after broken refill: %d", result.After.IdleCount)
	}
}

// TestManagerRefillRequestMergedByMax 验证 refill 请求按更高目标合并。
func TestManagerRefillRequestMergedByMax(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{}
	manager, err := NewManager(ManagerOptions{
		Config: ManagerConfig{
			MinIdle:           1,
			MaxIdle:           6,
			IdleTTL:           0,
			MaxInflightOpens:  2,
			TunnelOpenRate:    1000,
			TunnelOpenBurst:   1000,
			ReconcileInterval: time.Second,
		},
		Opener: opener,
	})
	if err != nil {
		testingObject.Fatalf("new manager failed: %v", err)
	}

	if _, err := manager.ReconcileNow(context.Background(), "startup"); err != nil {
		testingObject.Fatalf("startup reconcile failed: %v", err)
	}
	if !manager.RequestRefill(3, "manual") {
		testingObject.Fatalf("expected first refill request accepted")
	}
	if !manager.RequestRefill(5, "manual") {
		testingObject.Fatalf("expected higher target refill request accepted")
	}
	if manager.RequestRefill(4, "manual") {
		testingObject.Fatalf("expected lower target refill request merged without update")
	}

	result, err := manager.ReconcileNow(context.Background(), "merged_refill")
	if err != nil {
		testingObject.Fatalf("reconcile failed: %v", err)
	}
	if result.After.IdleCount != 5 {
		testingObject.Fatalf("unexpected idle count after merged refill: %d", result.After.IdleCount)
	}
}

// TestManagerTTLSweepIdlePath 验证 TTL 回收会收敛 idle -> closing -> closed。
func TestManagerTTLSweepIdlePath(testingObject *testing.T) {
	testingObject.Parallel()
	opener := &producerTestOpener{}
	manager, err := NewManager(ManagerOptions{
		Config: ManagerConfig{
			MinIdle:           0,
			MaxIdle:           4,
			IdleTTL:           10 * time.Millisecond,
			MaxInflightOpens:  1,
			TunnelOpenRate:    100,
			TunnelOpenBurst:   10,
			ReconcileInterval: time.Second,
		},
		Opener: opener,
	})
	if err != nil {
		testingObject.Fatalf("new manager failed: %v", err)
	}

	idleTunnel := &producerTestTunnel{id: "ttl-idle-1"}
	added, addErr := manager.registry.TryAddOpenedAsIdle(time.Now().Add(-time.Second), idleTunnel, 4)
	if addErr != nil {
		testingObject.Fatalf("add idle failed: %v", addErr)
	}
	if !added {
		testingObject.Fatalf("expected add idle success")
	}

	result, err := manager.ReconcileNow(context.Background(), "periodic")
	if err != nil {
		testingObject.Fatalf("reconcile with ttl sweep failed: %v", err)
	}
	if result.TTLSweep.Closed != 1 {
		testingObject.Fatalf("unexpected ttl closed count: %d", result.TTLSweep.Closed)
	}
	if _, exists := manager.registry.Get("ttl-idle-1"); exists {
		testingObject.Fatalf("expected ttl idle removed")
	}
	if idleTunnel.closeCount.Load() != 1 {
		testingObject.Fatalf("expected ttl reaper close once, got %d", idleTunnel.closeCount.Load())
	}
}
