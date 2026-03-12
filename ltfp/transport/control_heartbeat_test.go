package transport

import (
	"testing"
	"time"
)

// TestHeartbeatPolicyNormalizeAndValidate 验证 heartbeat 策略默认值归一化。
func TestHeartbeatPolicyNormalizeAndValidate(testingObject *testing.T) {
	normalizedPolicy, err := (HeartbeatPolicy{}).NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("normalize heartbeat policy failed: %v", err)
	}
	if normalizedPolicy.SendInterval <= 0 {
		testingObject.Fatalf("expected positive send interval, got %s", normalizedPolicy.SendInterval)
	}
	if normalizedPolicy.MissThreshold <= 0 {
		testingObject.Fatalf("expected positive miss threshold, got %d", normalizedPolicy.MissThreshold)
	}
	if normalizedPolicy.QueueDelayGrace <= 0 {
		testingObject.Fatalf("expected positive queue delay grace, got %s", normalizedPolicy.QueueDelayGrace)
	}
}

// TestHeartbeatMonitorShouldSendAndReceiveReset 验证发送周期与收到对端 heartbeat 后的状态回收。
func TestHeartbeatMonitorShouldSendAndReceiveReset(testingObject *testing.T) {
	monitor, err := NewHeartbeatMonitor(HeartbeatPolicy{
		SendInterval:    5 * time.Second,
		MissThreshold:   3,
		QueueDelayGrace: 2 * time.Second,
	})
	if err != nil {
		testingObject.Fatalf("create heartbeat monitor failed: %v", err)
	}

	startTime := time.Unix(100, 0).UTC()
	monitor.Start(startTime)
	if monitor.ShouldSend(startTime) {
		testingObject.Fatalf("expected not send immediately at start")
	}
	if !monitor.ShouldSend(startTime.Add(5 * time.Second)) {
		testingObject.Fatalf("expected send due after one send interval")
	}

	monitor.ObserveSend(startTime.Add(5 * time.Second))
	monitor.ObserveReceive(startTime.Add(6 * time.Second))
	status := monitor.Snapshot(startTime.Add(10 * time.Second))
	if status.ConsecutiveMisses != 0 {
		testingObject.Fatalf("expected zero misses after receive, got %d", status.ConsecutiveMisses)
	}
	if status.Dead {
		testingObject.Fatalf("expected monitor alive after receive")
	}
	if status.NextSendAt != startTime.Add(10*time.Second) {
		testingObject.Fatalf("unexpected next send time: got=%s want=%s", status.NextSendAt, startTime.Add(10*time.Second))
	}
}

// TestHeartbeatMonitorShouldSendWithZeroNow 验证 ShouldSend 对零时间按当前时钟口径判定。
func TestHeartbeatMonitorShouldSendWithZeroNow(testingObject *testing.T) {
	monitor, err := NewHeartbeatMonitor(HeartbeatPolicy{
		SendInterval:    5 * time.Second,
		MissThreshold:   3,
		QueueDelayGrace: 2 * time.Second,
	})
	if err != nil {
		testingObject.Fatalf("create heartbeat monitor failed: %v", err)
	}

	monitor.Start(time.Now().UTC().Add(-30 * time.Second))
	if !monitor.ShouldSend(time.Time{}) {
		testingObject.Fatalf("expected ShouldSend(time.Time{}) to use current time and return true")
	}
}

// TestHeartbeatMonitorMarksDeadAfterMissThreshold 验证连续丢失达到阈值后会进入判死状态。
func TestHeartbeatMonitorMarksDeadAfterMissThreshold(testingObject *testing.T) {
	monitor, err := NewHeartbeatMonitor(HeartbeatPolicy{
		SendInterval:    5 * time.Second,
		MissThreshold:   3,
		QueueDelayGrace: 2 * time.Second,
	})
	if err != nil {
		testingObject.Fatalf("create heartbeat monitor failed: %v", err)
	}

	startTime := time.Unix(200, 0).UTC()
	monitor.Start(startTime)
	status := monitor.Snapshot(startTime.Add(17 * time.Second))
	if status.ConsecutiveMisses != 3 {
		testingObject.Fatalf("expected 3 misses, got %d", status.ConsecutiveMisses)
	}
	if !status.Dead {
		testingObject.Fatalf("expected monitor dead after miss threshold")
	}
	if status.FailureDeadline != startTime.Add(17*time.Second) {
		testingObject.Fatalf("unexpected failure deadline: got=%s want=%s", status.FailureDeadline, startTime.Add(17*time.Second))
	}
}

// TestHeartbeatMonitorObservePeerActivity 验证收到任意对端控制活动也可刷新存活窗口。
func TestHeartbeatMonitorObservePeerActivity(testingObject *testing.T) {
	monitor, err := NewHeartbeatMonitor(HeartbeatPolicy{
		SendInterval:    4 * time.Second,
		MissThreshold:   2,
		QueueDelayGrace: 1500 * time.Millisecond,
	})
	if err != nil {
		testingObject.Fatalf("create heartbeat monitor failed: %v", err)
	}

	startTime := time.Unix(300, 0).UTC()
	monitor.Start(startTime)
	monitor.ObservePeerActivity(startTime.Add(7 * time.Second))
	status := monitor.Snapshot(startTime.Add(11 * time.Second))
	if status.ConsecutiveMisses != 0 {
		testingObject.Fatalf("expected zero misses after peer activity, got %d", status.ConsecutiveMisses)
	}
	if status.Dead {
		testingObject.Fatalf("expected monitor alive after peer activity")
	}
}
