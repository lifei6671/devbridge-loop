package consistency

import "testing"

// TestBuildReconnectSyncPlanInitial 验证首次同步需要 full-sync。
func TestBuildReconnectSyncPlanInitial(t *testing.T) {
	t.Parallel()

	plan := BuildReconnectSyncPlan("", 0, "session-001", 1, 0)
	// 初次同步应强制 full-sync。
	if !plan.NeedFullSync {
		t.Fatalf("expected full sync plan")
	}
}

// TestBuildReconnectSyncPlanEpochAdvanced 验证 epoch 提升需要 full-sync。
func TestBuildReconnectSyncPlanEpochAdvanced(t *testing.T) {
	t.Parallel()

	plan := BuildReconnectSyncPlan("session-001", 1, "session-001", 2, 10)
	// epoch 升级后必须重建全量快照。
	if !plan.NeedFullSync {
		t.Fatalf("expected full sync plan")
	}
	if plan.Reason != "session_epoch_advanced" {
		t.Fatalf("unexpected reason: %s", plan.Reason)
	}
}

// TestBuildReconnectSyncPlanDelta 验证同会话同 epoch 可走增量同步。
func TestBuildReconnectSyncPlanDelta(t *testing.T) {
	t.Parallel()

	plan := BuildReconnectSyncPlan("session-001", 2, "session-001", 2, 21)
	// 未发生代际变化时应允许增量同步。
	if plan.NeedFullSync {
		t.Fatalf("expected delta sync plan")
	}
	if plan.SinceResourceVersion != 21 {
		t.Fatalf("unexpected since resource version: %d", plan.SinceResourceVersion)
	}
}
