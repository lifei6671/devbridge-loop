package consistency

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestBuildDedupKey 验证去重键生成规则。
func TestBuildDedupKey(t *testing.T) {
	t.Parallel()

	key, err := BuildDedupKey("session-001", 7, "event-001")
	if err != nil {
		t.Fatalf("build dedup key failed: %v", err)
	}
	// 三元组应完整进入去重键。
	if key != "session-001:7:event-001" {
		t.Fatalf("unexpected key: %s", key)
	}
}

// TestBuildDedupKeyRejectInvalidInput 验证去重键入参校验。
func TestBuildDedupKeyRejectInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := BuildDedupKey("", 1, "event-001")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// sessionId 缺失时应返回缺失字段错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCompareResourceVersion 验证资源版本比较逻辑。
func TestCompareResourceVersion(t *testing.T) {
	t.Parallel()

	if CompareResourceVersion(3, 2) != VersionRelationOlder {
		t.Fatalf("expected older relation")
	}
	if CompareResourceVersion(3, 3) != VersionRelationEqual {
		t.Fatalf("expected equal relation")
	}
	if CompareResourceVersion(3, 4) != VersionRelationNewer {
		t.Fatalf("expected newer relation")
	}
}

// TestValidateResourceVersionAdvanceRejectRollback 验证版本回退拒绝。
func TestValidateResourceVersionAdvanceRejectRollback(t *testing.T) {
	t.Parallel()

	err := ValidateResourceVersionAdvance(5, 4)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 版本回退应返回 VERSION_ROLLBACK 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeVersionRollback) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildAckHelpers 验证 ACK 构造器的 accepted 语义。
func TestBuildAckHelpers(t *testing.T) {
	t.Parallel()

	ack := BuildPublishServiceAck(pb.EventStatusDuplicate, "svc-001", "dev/alice/order", 3, 3, "", "")
	// duplicate 应按幂等成功视为 accepted=true。
	if !ack.Accepted {
		t.Fatalf("expected accepted=true for duplicate status")
	}
	routeAck := BuildRouteAssignAck(pb.EventStatusRejected, "route-001", 0, 5, "INVALID_SCOPE", "scope mismatch")
	// rejected 应返回 accepted=false。
	if routeAck.Accepted {
		t.Fatalf("expected accepted=false for rejected status")
	}
}
