package negotiation

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestEvaluateProfilesSuccess 验证 pb 协商适配器成功路径。
func TestEvaluateProfilesSuccess(t *testing.T) {
	t.Parallel()

	local := pb.NegotiationProfile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
		OptionalFeatures: []string{"port_mapping_forward"},
	}
	remote := pb.NegotiationProfile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
		OptionalFeatures: []string{"client_auto_negotiation"},
	}
	result, err := EvaluateProfiles(local, remote)
	if err != nil {
		t.Fatalf("evaluate profiles failed: %v", err)
	}
	// 协商成功时 accepted 应为 true。
	if !result.Accepted {
		t.Fatalf("expected accepted=true")
	}
}

// TestEvaluateProfilesRejectMissingRequired 验证缺失 required feature 时返回错误码。
func TestEvaluateProfilesRejectMissingRequired(t *testing.T) {
	t.Parallel()

	local := pb.NegotiationProfile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch"},
	}
	remote := pb.NegotiationProfile{
		VersionMajor:     2,
		VersionMinor:     1,
		RequiredFeatures: []string{"session_epoch", "client_auto_negotiation"},
	}
	result, err := EvaluateProfiles(local, remote)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 返回错误中应携带协商错误码。
	if result.ErrorCode == "" {
		t.Fatalf("expected result.errorCode")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeNegotiationUnsupportedFeature) {
		t.Fatalf("unexpected error: %v", err)
	}
}
