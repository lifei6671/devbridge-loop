package pb

import "testing"

// TestIsKnownForwardMode 验证 ForwardMode 白名单判断。
func TestIsKnownForwardMode(t *testing.T) {
	t.Parallel()

	// 已知模式应返回 true。
	if !IsKnownForwardMode(ForwardModePortMapping) {
		t.Fatalf("expected known forward mode")
	}
	// 非法模式应返回 false。
	if IsKnownForwardMode(ForwardMode("unknown")) {
		t.Fatalf("expected unknown forward mode")
	}
}

// TestIsKnownForwardSource 验证 ForwardSource 白名单判断。
func TestIsKnownForwardSource(t *testing.T) {
	t.Parallel()

	// 已知来源应返回 true。
	if !IsKnownForwardSource(ForwardSourceTunnel) {
		t.Fatalf("expected known forward source")
	}
	// 非法来源应返回 false。
	if IsKnownForwardSource(ForwardSource("unknown")) {
		t.Fatalf("expected unknown forward source")
	}
}
