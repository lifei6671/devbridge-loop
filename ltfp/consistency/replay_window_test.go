package consistency

import "testing"

// TestReplayWindowSeenOrAdd 验证去重窗口重复事件识别能力。
func TestReplayWindowSeenOrAdd(t *testing.T) {
	t.Parallel()

	window := NewReplayWindow(2)
	// 首次写入应返回 false。
	if hit := window.SeenOrAdd("k1"); hit {
		t.Fatalf("expected first insert hit=false")
	}
	// 第二次写入同 key 应返回 true。
	if hit := window.SeenOrAdd("k1"); !hit {
		t.Fatalf("expected duplicate hit=true")
	}
}

// TestReplayWindowEviction 验证超容量时会淘汰最旧事件键。
func TestReplayWindowEviction(t *testing.T) {
	t.Parallel()

	window := NewReplayWindow(2)
	window.SeenOrAdd("k1")
	window.SeenOrAdd("k2")
	window.SeenOrAdd("k3")
	// k1 应被淘汰，再次写入应视为新 key。
	if hit := window.SeenOrAdd("k1"); hit {
		t.Fatalf("expected evicted key to be treated as new")
	}
}
