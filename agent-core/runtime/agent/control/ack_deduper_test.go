package control

import (
	"testing"
	"time"
)

// TestAckDeduperSeenWithinTTL 验证同一 event_id 在 TTL 窗口内会被去重，超窗后可重新通过。
func TestAckDeduperSeenWithinTTL(testingObject *testing.T) {
	testingObject.Parallel()
	deduper := NewAckDeduper(50 * time.Millisecond)
	baseNow := time.Unix(1_700_000_000, 0).UTC()

	// 首次看到事件必须放行，避免误判为重放。
	if deduper.Seen("evt-1", baseNow) {
		testingObject.Fatalf("expected first seen returns false")
	}
	// TTL 窗口内重复事件必须命中去重。
	if !deduper.Seen("evt-1", baseNow.Add(10*time.Millisecond)) {
		testingObject.Fatalf("expected duplicate within ttl returns true")
	}
	// 超过 TTL 后应允许同一事件重新进入处理链路。
	if deduper.Seen("evt-1", baseNow.Add(80*time.Millisecond)) {
		testingObject.Fatalf("expected expired event seen returns false")
	}
}

// TestAckDeduperEmptyEventIDIgnored 验证空 event_id 不参与去重缓存，避免污染内存。
func TestAckDeduperEmptyEventIDIgnored(testingObject *testing.T) {
	testingObject.Parallel()
	deduper := NewAckDeduper(time.Second)

	// 空 event_id 直接放行，保持调用方兼容性。
	if deduper.Seen("", time.Unix(1_700_000_100, 0).UTC()) {
		testingObject.Fatalf("expected empty event id not deduplicated")
	}
	// 空键不应写入缓存，防止无意义占用去重空间。
	if len(deduper.seen) != 0 {
		testingObject.Fatalf("expected empty event id not stored, got=%d", len(deduper.seen))
	}
}

// TestAckDeduperSweepRemovesExpiredEntries 验证 Sweep 只清理过期项并保留未过期项。
func TestAckDeduperSweepRemovesExpiredEntries(testingObject *testing.T) {
	testingObject.Parallel()
	deduper := NewAckDeduper(time.Minute)
	now := time.Unix(1_700_000_200, 0).UTC()

	deduper.mu.Lock()
	// 预置一条已过期记录，模拟真实运行中的历史缓存。
	deduper.seen["evt-expired"] = now.Add(-2 * time.Minute)
	// 同时预置一条仍在 TTL 窗口内的记录。
	deduper.seen["evt-active"] = now.Add(-30 * time.Second)
	deduper.mu.Unlock()

	deduper.Sweep(now)

	// 过期项应被剔除，避免缓存无限增长。
	if _, exists := deduper.seen["evt-expired"]; exists {
		testingObject.Fatalf("expected expired entry removed")
	}
	// 未过期项必须保留，避免去重窗口被意外缩短。
	if _, exists := deduper.seen["evt-active"]; !exists {
		testingObject.Fatalf("expected active entry kept")
	}
}
