package observe

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestRecorderRecordAndQuery 验证 recorder 记录与查询行为。
func TestRecorderRecordAndQuery(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(4)
	recorder.Record(Event{
		TraceID:      "trace-001",
		PathKind:     pb.RouteTargetTypeConnectorService,
		ErrorCode:    "OLD_EPOCH",
		RejectReason: "stale_epoch",
		Message:      "reject old epoch message",
	})
	recorder.Record(Event{
		TraceID:        "trace-001",
		PathKind:       pb.RouteTargetTypeExternalService,
		FallbackReason: "service_unavailable",
		Message:        "fallback to external",
	})

	eventsByTrace := recorder.QueryByTrace("trace-001")
	if len(eventsByTrace) != 2 {
		t.Fatalf("unexpected trace event count: %d", len(eventsByTrace))
	}
	eventsByCode := recorder.QueryByErrorCode("OLD_EPOCH")
	if len(eventsByCode) != 1 {
		t.Fatalf("unexpected code event count: %d", len(eventsByCode))
	}
}

// TestRecorderSnapshotMetrics 验证 snapshot 指标统计。
func TestRecorderSnapshotMetrics(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(4)
	recorder.Record(Event{
		TraceID:      "trace-001",
		PathKind:     pb.RouteTargetTypeConnectorService,
		ErrorCode:    "E1",
		RejectReason: "scope_reject",
	})
	recorder.Record(Event{
		TraceID:        "trace-002",
		PathKind:       pb.RouteTargetTypeExternalService,
		ErrorCode:      "E2",
		FallbackReason: "resolve_miss",
	})
	recorder.Record(Event{
		TraceID:        "trace-003",
		PathKind:       pb.RouteTargetTypeExternalService,
		ErrorCode:      "E2",
		FallbackReason: "service_unavailable",
	})

	snapshot := recorder.Snapshot()
	if snapshot.Metrics.ByPathKind[pb.RouteTargetTypeConnectorService] != 1 {
		t.Fatalf("unexpected connector metric: %+v", snapshot.Metrics.ByPathKind)
	}
	if snapshot.Metrics.ByPathKind[pb.RouteTargetTypeExternalService] != 2 {
		t.Fatalf("unexpected external metric: %+v", snapshot.Metrics.ByPathKind)
	}
	if snapshot.Metrics.ByErrorCode["E2"] != 2 {
		t.Fatalf("unexpected error metric: %+v", snapshot.Metrics.ByErrorCode)
	}
	if snapshot.Metrics.ByFallbackReason["resolve_miss"] != 1 || snapshot.Metrics.ByFallbackReason["service_unavailable"] != 1 {
		t.Fatalf("unexpected fallback metric: %+v", snapshot.Metrics.ByFallbackReason)
	}
}

// TestRecorderCapacityWindow 验证 recorder 只保留最近事件窗口。
func TestRecorderCapacityWindow(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(2)
	recorder.Record(Event{TraceID: "trace-1", Message: "e1"})
	recorder.Record(Event{TraceID: "trace-2", Message: "e2"})
	recorder.Record(Event{TraceID: "trace-3", Message: "e3"})

	snapshot := recorder.Snapshot()
	if len(snapshot.Events) != 2 {
		t.Fatalf("unexpected event window size: %d", len(snapshot.Events))
	}
	if snapshot.Events[0].TraceID != "trace-2" || snapshot.Events[1].TraceID != "trace-3" {
		t.Fatalf("unexpected event window: %+v", snapshot.Events)
	}
}
