package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestTunnelPoolReportStoreUpsertAndList 验证上报快照会被写入并可查询。
func TestTunnelPoolReportStoreUpsertAndList(testingObject *testing.T) {
	testingObject.Parallel()

	store := NewTunnelPoolReportStore()
	now := time.Unix(1773491430, 0).UTC()
	store.Upsert(now, "agent-a", "session-a", 8, pb.TunnelPoolReport{
		IdleCount:       6,
		InUseCount:      4,
		TargetIdleCount: 8,
		Trigger:         "event:pool_low",
		TimestampUnix:   now.Unix(),
	})

	items := store.List()
	if len(items) != 1 {
		testingObject.Fatalf("unexpected report item size: got=%d want=1", len(items))
	}
	if items[0].ConnectorID != "agent-a" || items[0].SessionID != "session-a" || items[0].SessionEpoch != 8 {
		testingObject.Fatalf("unexpected report identity: %+v", items[0])
	}
	if items[0].IdleCount != 6 || items[0].InUseCount != 4 || items[0].TargetIdleCount != 8 {
		testingObject.Fatalf("unexpected report counts: %+v", items[0])
	}
}

// TestTunnelPoolReportStoreRejectStaleEpoch 验证旧代际上报不会覆盖新代际快照。
func TestTunnelPoolReportStoreRejectStaleEpoch(testingObject *testing.T) {
	testingObject.Parallel()

	store := NewTunnelPoolReportStore()
	now := time.Unix(1773491430, 0).UTC()
	store.Upsert(now, "agent-a", "session-a", 9, pb.TunnelPoolReport{
		IdleCount:       5,
		InUseCount:      2,
		TargetIdleCount: 8,
		TimestampUnix:   now.Unix(),
	})
	store.Upsert(now.Add(2*time.Second), "agent-a", "session-a", 8, pb.TunnelPoolReport{
		IdleCount:       1,
		InUseCount:      0,
		TargetIdleCount: 8,
		TimestampUnix:   now.Add(2 * time.Second).Unix(),
	})

	items := store.List()
	if len(items) != 1 {
		testingObject.Fatalf("unexpected report item size: got=%d want=1", len(items))
	}
	if items[0].SessionEpoch != 9 || items[0].IdleCount != 5 {
		testingObject.Fatalf("stale epoch should not overwrite latest: %+v", items[0])
	}
}

// TestTunnelPoolReportStoreRemoveBySession 验证可按 session 删除上报快照。
func TestTunnelPoolReportStoreRemoveBySession(testingObject *testing.T) {
	testingObject.Parallel()

	store := NewTunnelPoolReportStore()
	now := time.Unix(1773491430, 0).UTC()
	store.Upsert(now, "agent-a", "session-a", 8, pb.TunnelPoolReport{
		IdleCount:       3,
		InUseCount:      1,
		TargetIdleCount: 8,
		TimestampUnix:   now.Unix(),
	})
	store.RemoveBySession("session-a", 8)
	if len(store.List()) != 0 {
		testingObject.Fatalf("expected report store empty after remove by session")
	}
}
