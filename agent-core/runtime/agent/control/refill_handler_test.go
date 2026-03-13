package control

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
)

// refillTestScheduler 记录 RequestRefill 调用参数。
type refillTestScheduler struct {
	mutex sync.Mutex

	snapshot tunnel.Snapshot
	results  []bool
	calls    []refillCall
}

// refillCall 记录一次 refill 调度请求。
type refillCall struct {
	target int
	reason string
}

// Snapshot 返回测试快照。
func (scheduler *refillTestScheduler) Snapshot() tunnel.Snapshot {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	return scheduler.snapshot
}

// RequestRefill 记录目标值并按预设结果返回。
func (scheduler *refillTestScheduler) RequestRefill(targetIdle int, reason string) bool {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	scheduler.calls = append(scheduler.calls, refillCall{target: targetIdle, reason: reason})
	resultIndex := len(scheduler.calls) - 1
	accepted := true
	if resultIndex < len(scheduler.results) {
		// 使用预设结果模拟调度器不同状态。
		accepted = scheduler.results[resultIndex]
	}
	if accepted {
		// 仅在接收请求时推进 idle 快照。
		scheduler.snapshot.IdleCount = targetIdle
	}
	return accepted
}

// Calls 返回调度调用记录副本。
func (scheduler *refillTestScheduler) Calls() []refillCall {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	copied := make([]refillCall, 0, len(scheduler.calls))
	copied = append(copied, scheduler.calls...)
	return copied
}

// TestRefillHandlerDeduplicateAndClamp 验证去重与 max_idle 截断逻辑。
func TestRefillHandlerDeduplicateAndClamp(testingObject *testing.T) {
	testingObject.Parallel()
	scheduler := &refillTestScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 2},
		results:  []bool{true, true},
	}
	handler, err := NewRefillHandler(scheduler, RefillHandlerConfig{
		RequestDeduplicateTTL: time.Minute,
		MaxIdle:               5,
	})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}
	handler.SetSession("session-a", 1)

	firstRequest := TunnelRefillRequest{
		SessionID:          "session-a",
		SessionEpoch:       1,
		RequestID:          "req-1",
		RequestedIdleDelta: 2,
		Reason:             TunnelRefillReasonManual,
		Timestamp:          time.Now().UTC(),
	}
	firstResult, err := handler.Handle(context.Background(), firstRequest)
	if err != nil {
		testingObject.Fatalf("handle first request failed: %v", err)
	}
	if firstResult.Deduplicated {
		testingObject.Fatalf("expected first request not deduplicated")
	}
	if !firstResult.Accepted {
		testingObject.Fatalf("expected first request accepted")
	}
	if firstResult.EffectiveTargetIdle != 4 {
		testingObject.Fatalf("unexpected first effective target: %d", firstResult.EffectiveTargetIdle)
	}

	dedupResult, err := handler.Handle(context.Background(), firstRequest)
	if err != nil {
		testingObject.Fatalf("handle deduplicated request failed: %v", err)
	}
	if !dedupResult.Deduplicated {
		testingObject.Fatalf("expected deduplicated result")
	}

	secondRequest := TunnelRefillRequest{
		SessionID:          "session-a",
		SessionEpoch:       1,
		RequestID:          "req-2",
		RequestedIdleDelta: 10,
		Reason:             TunnelRefillReasonLowWatermark,
		Timestamp:          time.Now().UTC().Add(time.Second),
	}
	secondResult, err := handler.Handle(context.Background(), secondRequest)
	if err != nil {
		testingObject.Fatalf("handle second request failed: %v", err)
	}
	if secondResult.EffectiveTargetIdle != 5 {
		testingObject.Fatalf("expected clamped target 5, got %d", secondResult.EffectiveTargetIdle)
	}

	calls := scheduler.Calls()
	if len(calls) != 2 {
		testingObject.Fatalf("unexpected request refill calls: %d", len(calls))
	}
	if calls[0].target != 4 || calls[1].target != 5 {
		testingObject.Fatalf("unexpected refill targets: %+v", calls)
	}
}

// TestRefillHandlerMergePendingTarget 验证调度未接收时的目标合并行为。
func TestRefillHandlerMergePendingTarget(testingObject *testing.T) {
	testingObject.Parallel()
	scheduler := &refillTestScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 1},
		results:  []bool{false, true},
	}
	handler, err := NewRefillHandler(scheduler, RefillHandlerConfig{
		RequestDeduplicateTTL: time.Minute,
		MaxIdle:               8,
	})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}
	handler.SetSession("session-b", 2)

	firstRequest := TunnelRefillRequest{
		SessionID:          "session-b",
		SessionEpoch:       2,
		RequestID:          "req-merge-1",
		RequestedIdleDelta: 2,
		Reason:             TunnelRefillReasonManual,
		Timestamp:          time.Now().UTC(),
	}
	firstResult, err := handler.Handle(context.Background(), firstRequest)
	if err != nil {
		testingObject.Fatalf("handle first merge request failed: %v", err)
	}
	if firstResult.Accepted {
		testingObject.Fatalf("expected first request not accepted")
	}

	secondRequest := TunnelRefillRequest{
		SessionID:          "session-b",
		SessionEpoch:       2,
		RequestID:          "req-merge-2",
		RequestedIdleDelta: 1,
		Reason:             TunnelRefillReasonLowWatermark,
		Timestamp:          time.Now().UTC().Add(time.Second),
	}
	secondResult, err := handler.Handle(context.Background(), secondRequest)
	if err != nil {
		testingObject.Fatalf("handle second merge request failed: %v", err)
	}
	if !secondResult.Accepted {
		testingObject.Fatalf("expected second request accepted")
	}

	calls := scheduler.Calls()
	if len(calls) != 2 {
		testingObject.Fatalf("unexpected request refill call count: %d", len(calls))
	}
	if calls[0].target != 3 {
		testingObject.Fatalf("unexpected first target: %d", calls[0].target)
	}
	if calls[1].target != 3 {
		testingObject.Fatalf("expected merged target remain 3, got %d", calls[1].target)
	}
}

// TestRefillHandlerRejectStaleSession 验证旧 session 请求会被拒绝。
func TestRefillHandlerRejectStaleSession(testingObject *testing.T) {
	testingObject.Parallel()
	scheduler := &refillTestScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 1},
	}
	handler, err := NewRefillHandler(scheduler, RefillHandlerConfig{})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}
	handler.SetSession("session-c", 3)

	_, err = handler.Handle(context.Background(), TunnelRefillRequest{
		SessionID:          "session-c",
		SessionEpoch:       2,
		RequestID:          "stale-req",
		RequestedIdleDelta: 1,
		Reason:             TunnelRefillReasonManual,
		Timestamp:          time.Now().UTC(),
	})
	if err == nil {
		testingObject.Fatalf("expected stale session request rejected")
	}
}

// TestRefillHandlerResetDeduplicateCacheOnSessionSwitch 验证切换会话后去重缓存会重置。
func TestRefillHandlerResetDeduplicateCacheOnSessionSwitch(testingObject *testing.T) {
	testingObject.Parallel()
	scheduler := &refillTestScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 1},
		results:  []bool{true, true},
	}
	handler, err := NewRefillHandler(scheduler, RefillHandlerConfig{
		RequestDeduplicateTTL: time.Minute,
		MaxIdle:               8,
	})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}

	handler.SetSession("session-1", 1)
	firstRequest := TunnelRefillRequest{
		SessionID:          "session-1",
		SessionEpoch:       1,
		RequestID:          "req-reused",
		RequestedIdleDelta: 1,
		Reason:             TunnelRefillReasonManual,
		Timestamp:          time.Now().UTC(),
	}
	firstResult, err := handler.Handle(context.Background(), firstRequest)
	if err != nil {
		testingObject.Fatalf("handle first request failed: %v", err)
	}
	if firstResult.Deduplicated {
		testingObject.Fatalf("expected first request not deduplicated")
	}

	// 切换 session 后复用 request_id，应该被视为新请求。
	handler.SetSession("session-2", 2)
	secondRequest := firstRequest
	secondRequest.SessionID = "session-2"
	secondRequest.SessionEpoch = 2
	secondRequest.Timestamp = firstRequest.Timestamp.Add(time.Second)

	secondResult, err := handler.Handle(context.Background(), secondRequest)
	if err != nil {
		testingObject.Fatalf("handle second request failed: %v", err)
	}
	if secondResult.Deduplicated {
		testingObject.Fatalf("expected second request not deduplicated after session switch")
	}

	calls := scheduler.Calls()
	if len(calls) != 2 {
		testingObject.Fatalf("unexpected request refill calls: %d", len(calls))
	}
}
