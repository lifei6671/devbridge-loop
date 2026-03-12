package fallback

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCanFallbackPreOpenAllowed 验证 pre-open 信号允许 fallback。
func TestCanFallbackPreOpenAllowed(t *testing.T) {
	t.Parallel()

	allowed, err := CanFallback(pb.FallbackPolicyPreOpenOnly, SignalOpenAckFail)
	if err != nil {
		t.Fatalf("can fallback failed: %v", err)
	}
	if !allowed {
		t.Fatalf("expected fallback allowed")
	}
}

// TestCanFallbackPostOpenForbidden 验证 post-open 信号禁止 fallback。
func TestCanFallbackPostOpenForbidden(t *testing.T) {
	t.Parallel()

	allowed, err := CanFallback(pb.FallbackPolicyPreOpenOnly, SignalOpenAckSuccess)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if allowed {
		t.Fatalf("expected fallback forbidden")
	}
	// post-open 阶段应返回 fallback forbidden 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeHybridFallbackForbidden) {
		t.Fatalf("unexpected error: %v", err)
	}
}
