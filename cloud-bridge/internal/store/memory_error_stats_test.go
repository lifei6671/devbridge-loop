package store

import "testing"

func TestListErrorStats(t *testing.T) {
	s := NewMemoryStore()

	ctx := map[string]string{"path": "/api/v1/ingress/http/ping"}
	s.AddError("ROUTE_NOT_FOUND", "route not found", ctx)
	// 修改原始上下文后，存储中的历史记录不应被污染。
	ctx["path"] = "/mutated"
	s.AddError("ROUTE_NOT_FOUND", "route not found again", nil)
	s.AddError("TUNNEL_OFFLINE", "backflow endpoint unavailable", map[string]string{"tunnelId": "tunnel-a"})

	stats := s.ListErrorStats()
	if stats.Total != 3 {
		t.Fatalf("expected total=3, got %d", stats.Total)
	}
	if stats.UniqueCode != 2 {
		t.Fatalf("expected uniqueCode=2, got %d", stats.UniqueCode)
	}
	if len(stats.ByCode) != 2 {
		t.Fatalf("expected 2 code stats, got %d", len(stats.ByCode))
	}

	// ROUTE_NOT_FOUND 次数最高，应排在第一位。
	if stats.ByCode[0].Code != "ROUTE_NOT_FOUND" || stats.ByCode[0].Count != 2 {
		t.Fatalf("unexpected first code stat: %+v", stats.ByCode[0])
	}
	if len(stats.Recent) != 3 {
		t.Fatalf("expected 3 recent errors, got %d", len(stats.Recent))
	}
	// Recent 按时间倒序返回，最后一次写入是 TUNNEL_OFFLINE。
	if stats.Recent[0].Code != "TUNNEL_OFFLINE" {
		t.Fatalf("unexpected recent[0]: %+v", stats.Recent[0])
	}
	// 校验上下文复制是否生效。
	if stats.Recent[2].Context["path"] != "/api/v1/ingress/http/ping" {
		t.Fatalf("expected immutable context copy, got %+v", stats.Recent[2].Context)
	}
}
