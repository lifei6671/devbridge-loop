package control

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
)

// reporterTestSnapshotSource 提供可控的池快照。
type reporterTestSnapshotSource struct {
	mutex    sync.Mutex
	snapshot tunnel.Snapshot
}

// Snapshot 返回当前测试快照。
func (source *reporterTestSnapshotSource) Snapshot() tunnel.Snapshot {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	return source.snapshot
}

// reporterTestSender 记录发送出去的 report。
type reporterTestSender struct {
	reportChannel chan TunnelPoolReport
}

// SendTunnelPoolReport 记录发送结果，供测试断言。
func (sender *reporterTestSender) SendTunnelPoolReport(ctx context.Context, report TunnelPoolReport) error {
	select {
	case <-ctx.Done():
		// 上下文取消时返回取消错误。
		return ctx.Err()
	case sender.reportChannel <- report:
		return nil
	}
}

// TestTunnelReporterEventAndPeriodic 验证事件驱动上报与周期纠偏上报。
func TestTunnelReporterEventAndPeriodic(testingObject *testing.T) {
	testingObject.Parallel()
	source := &reporterTestSnapshotSource{
		snapshot: tunnel.Snapshot{
			OpeningCount:  1,
			IdleCount:     3,
			ReservedCount: 2,
			ActiveCount:   1,
			ClosingCount:  1,
		},
	}
	sender := &reporterTestSender{reportChannel: make(chan TunnelPoolReport, 8)}
	reporter, err := NewTunnelReporter(source, sender, TunnelReporterConfig{
		Period:         40 * time.Millisecond,
		EventBuffer:    8,
		TargetIdleHint: 6,
	})
	if err != nil {
		testingObject.Fatalf("new tunnel reporter failed: %v", err)
	}
	reporter.SetSession("session-a", 7)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reportDone := make(chan error, 1)
	go func() {
		// 启动上报循环，等待事件和周期触发。
		reportDone <- reporter.Run(ctx)
	}()

	reporter.NotifyEvent("idle_acquired")
	firstReport := waitTunnelPoolReport(testingObject, sender.reportChannel, 500*time.Millisecond)
	if !strings.HasPrefix(firstReport.Trigger, "event:") {
		testingObject.Fatalf("expected event trigger, got %s", firstReport.Trigger)
	}
	if firstReport.SessionID != "session-a" || firstReport.SessionEpoch != 7 {
		testingObject.Fatalf("unexpected session fields: id=%s epoch=%d", firstReport.SessionID, firstReport.SessionEpoch)
	}
	if firstReport.IdleCount != 3 {
		testingObject.Fatalf("unexpected idle count: %d", firstReport.IdleCount)
	}
	if firstReport.InUseCount != 5 {
		testingObject.Fatalf("unexpected in_use count: %d", firstReport.InUseCount)
	}

	foundPeriodic := false
	deadline := time.After(500 * time.Millisecond)
	for !foundPeriodic {
		select {
		case <-deadline:
			testingObject.Fatalf("expected periodic report not received")
		case report := <-sender.reportChannel:
			if report.Trigger == "periodic" {
				foundPeriodic = true
			}
		}
	}

	cancel()
	err = <-reportDone
	if err == nil {
		testingObject.Fatalf("expected reporter run return context canceled")
	}
}

// waitTunnelPoolReport 等待一条 report，超时则失败。
func waitTunnelPoolReport(testingObject *testing.T, reportChannel <-chan TunnelPoolReport, timeout time.Duration) TunnelPoolReport {
	testingObject.Helper()
	select {
	case report := <-reportChannel:
		return report
	case <-time.After(timeout):
		testingObject.Fatalf("wait tunnel pool report timeout: %v", timeout)
		return TunnelPoolReport{}
	}
}
