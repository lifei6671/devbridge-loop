package negotiation

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// TestEvaluateSuccess 验证协商成功路径。
func TestEvaluateSuccess(t *testing.T) {
	t.Parallel()

	local := Profile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
		OptionalFeatures: []string{"port_mapping_forward", "hybrid_pre_open"},
	}
	remote := Profile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
		OptionalFeatures: []string{"hybrid_pre_open"},
	}

	result, err := Evaluate(local, remote)
	if err != nil {
		t.Fatalf("evaluate negotiation failed: %v", err)
	}
	// 成功场景必须返回 accepted=true。
	if !result.Accepted {
		t.Fatalf("expected accepted=true, got false")
	}
	// 交集至少应包含双方都支持的 required feature。
	if !HasFeature(result.NegotiatedFeatures, "session_epoch") {
		t.Fatalf("missing session_epoch in negotiated features")
	}
}

// TestEvaluateRejectVersionMismatch 验证主版本不一致会拒绝协商。
func TestEvaluateRejectVersionMismatch(t *testing.T) {
	t.Parallel()

	local := Profile{VersionMajor: 2, VersionMinor: 1}
	remote := Profile{VersionMajor: 3, VersionMinor: 0}
	_, err := Evaluate(local, remote)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 主版本不一致应返回版本不兼容错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeNegotiationUnsupportedVersion) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEvaluateRejectMissingRequiredFeature 验证缺少 required feature 会拒绝协商。
func TestEvaluateRejectMissingRequiredFeature(t *testing.T) {
	t.Parallel()

	local := Profile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
		OptionalFeatures: []string{"port_mapping_forward"},
	}
	remote := Profile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch", "client_auto_negotiation"},
	}
	_, err := Evaluate(local, remote)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 缺失 required feature 应返回 unsupported feature 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeNegotiationUnsupportedFeature) {
		t.Fatalf("unexpected error: %v", err)
	}
}
