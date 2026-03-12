package validate

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestValidateFullSyncRequest 验证 full-sync 请求校验。
func TestValidateFullSyncRequest(t *testing.T) {
	t.Parallel()

	request := pb.FullSyncRequest{
		RequestID:    "full-sync-001",
		ConnectorID:  "connector-dev-01",
		SessionID:    "session-001",
		SessionEpoch: 1,
	}
	if err := ValidateFullSyncRequest(request); err != nil {
		t.Fatalf("validate full sync request failed: %v", err)
	}
}

// TestValidateFullSyncRequestRejectMissingRequestID 验证缺失 requestId 会被拒绝。
func TestValidateFullSyncRequestRejectMissingRequestID(t *testing.T) {
	t.Parallel()

	request := pb.FullSyncRequest{
		ConnectorID:  "connector-dev-01",
		SessionID:    "session-001",
		SessionEpoch: 1,
	}
	err := ValidateFullSyncRequest(request)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 缺失字段应返回统一 missing required 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateFullSyncSnapshot 验证 full-sync 快照校验。
func TestValidateFullSyncSnapshot(t *testing.T) {
	t.Parallel()

	snapshot := testkit.GoldenFullSyncSnapshot()
	if err := ValidateFullSyncSnapshot(snapshot); err != nil {
		t.Fatalf("validate full sync snapshot failed: %v", err)
	}
}

// TestValidateFullSyncSnapshotRejectMissingRouteID 验证 routeId 缺失会被拒绝。
func TestValidateFullSyncSnapshotRejectMissingRouteID(t *testing.T) {
	t.Parallel()

	snapshot := testkit.GoldenFullSyncSnapshot()
	snapshot.Routes[0].RouteID = ""
	err := ValidateFullSyncSnapshot(snapshot)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 缺失 routeId 应返回 missing required 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}
