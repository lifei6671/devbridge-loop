package adminview

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

// TestBuildTunnelSummaryFromRuntimesUsesLatestTunnelUpdate
// 验证过滤聚合的 updated_at_ms 来源于 runtime 最新更新时间，而不是当前请求时间。
func TestBuildTunnelSummaryFromRuntimesUsesLatestTunnelUpdate(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_000_000, 0).UTC()
	oldestUpdate := now.Add(-30 * time.Minute)
	latestUpdate := now.Add(-5 * time.Minute)

	summary := BuildTunnelSummaryFromRuntimes(now, []registry.TunnelRuntime{
		{
			TunnelID:  "tunnel-a",
			State:     registry.TunnelStateIdle,
			UpdatedAt: oldestUpdate,
		},
		{
			TunnelID:  "tunnel-b",
			State:     registry.TunnelStateActive,
			UpdatedAt: latestUpdate,
		},
	})

	if summary.UpdatedAtMS != uint64(latestUpdate.UnixMilli()) {
		testingObject.Fatalf(
			"unexpected updated_at_ms: got=%d want=%d",
			summary.UpdatedAtMS,
			uint64(latestUpdate.UnixMilli()),
		)
	}
}
