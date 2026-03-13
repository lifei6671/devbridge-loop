package transport

import (
	"testing"
	"time"
)

// TestTransportMetricsRecorderSnapshot 验证指标聚合与快照输出。
func TestTransportMetricsRecorderSnapshot(testingObject *testing.T) {
	recorder := NewTransportMetricsRecorder()
	baseTime := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)

	recorder.ObserveHeartbeatRTT(25 * time.Millisecond)
	recorder.ObservePoolCounts(7, 3)
	recorder.ObserveRefill(4, baseTime)
	recorder.ObserveRefill(2, baseTime.Add(2*time.Second))
	recorder.IncOpenTimeout()
	recorder.IncOpenTimeout()
	recorder.IncReset()
	recorder.IncBrokenTunnel()

	snapshot := recorder.Snapshot(baseTime.Add(2 * time.Second))
	if snapshot.HeartbeatRTT != 25*time.Millisecond {
		testingObject.Fatalf("unexpected heartbeat RTT: %s", snapshot.HeartbeatRTT)
	}
	if snapshot.IdleCount != 7 || snapshot.InUseCount != 3 {
		testingObject.Fatalf("unexpected pool counts: idle=%d in_use=%d", snapshot.IdleCount, snapshot.InUseCount)
	}
	if snapshot.RefillOpenedTotal != 6 {
		testingObject.Fatalf("unexpected refill opened total: %d", snapshot.RefillOpenedTotal)
	}
	if snapshot.RefillRatePerSec != 3 {
		testingObject.Fatalf("unexpected refill rate: %f", snapshot.RefillRatePerSec)
	}
	if snapshot.OpenTimeoutCount != 2 {
		testingObject.Fatalf("unexpected open timeout count: %d", snapshot.OpenTimeoutCount)
	}
	if snapshot.ResetCount != 1 {
		testingObject.Fatalf("unexpected reset count: %d", snapshot.ResetCount)
	}
	if snapshot.BrokenCount != 1 {
		testingObject.Fatalf("unexpected broken count: %d", snapshot.BrokenCount)
	}
	logFields := snapshot.Fields()
	if logFields["heartbeat_rtt_ms"] != int64(25) {
		testingObject.Fatalf("unexpected heartbeat_rtt_ms field: %v", logFields["heartbeat_rtt_ms"])
	}
}

// TestTransportMetricsRecorderZeroRateWithoutWindow 验证同一时刻样本不计算速率。
func TestTransportMetricsRecorderZeroRateWithoutWindow(testingObject *testing.T) {
	recorder := NewTransportMetricsRecorder()
	baseTime := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	recorder.ObserveRefill(5, baseTime)

	snapshot := recorder.Snapshot(baseTime)
	if snapshot.RefillRatePerSec != 0 {
		testingObject.Fatalf("expected zero refill rate, got %f", snapshot.RefillRatePerSec)
	}
}

// TestTransportMetricsRecorderClampNegativePoolCounts 验证 pool 负值计数会被归零。
func TestTransportMetricsRecorderClampNegativePoolCounts(testingObject *testing.T) {
	recorder := NewTransportMetricsRecorder()
	recorder.ObservePoolCounts(-1, -5)
	snapshot := recorder.Snapshot(time.Time{})
	if snapshot.IdleCount != 0 || snapshot.InUseCount != 0 {
		testingObject.Fatalf("expected clamped pool counts 0/0, got %d/%d", snapshot.IdleCount, snapshot.InUseCount)
	}
}
