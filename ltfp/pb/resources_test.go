package pb

import "testing"

// TestTrafficStateConstants 验证流量状态常量可用。
func TestTrafficStateConstants(t *testing.T) {
	t.Parallel()

	traffic := Traffic{
		TrafficID: "traffic-001",
		State:     TrafficStateOpen,
	}
	// 状态常量应能用于运行态对象。
	if traffic.State != TrafficStateOpen {
		t.Fatalf("unexpected traffic state: %s", traffic.State)
	}
}

// TestFullSyncStructs 验证 full-sync 数据结构可正常实例化。
func TestFullSyncStructs(t *testing.T) {
	t.Parallel()

	request := FullSyncRequest{
		RequestID:            "full-sync-001",
		ConnectorID:          "connector-dev-01",
		SessionID:            "session-001",
		SessionEpoch:         1,
		SinceResourceVersion: 0,
	}
	// 请求对象字段应保持可读可写。
	if request.RequestID == "" || request.SessionEpoch == 0 {
		t.Fatalf("invalid full sync request struct")
	}

	snapshot := FullSyncSnapshot{
		RequestID:       "full-sync-001",
		SessionEpoch:    1,
		SnapshotVersion: 10,
		Completed:       true,
	}
	// 快照对象字段应保持可读可写。
	if snapshot.SnapshotVersion == 0 {
		t.Fatalf("invalid full sync snapshot struct")
	}
}
