package validate

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestValidateForwardIntentPortMapping 验证端口映射模式的转发意图校验。
func TestValidateForwardIntentPortMapping(t *testing.T) {
	t.Parallel()

	intent := testkit.GoldenForwardIntentPortMapping()
	if err := ValidateForwardIntent(intent); err != nil {
		t.Fatalf("validate forward intent failed: %v", err)
	}
}

// TestValidateForwardIntentAutoNegotiation 验证自动协商模式的转发意图校验。
func TestValidateForwardIntentAutoNegotiation(t *testing.T) {
	t.Parallel()

	intent := testkit.GoldenForwardIntentAutoNegotiation()
	if err := ValidateForwardIntent(intent); err != nil {
		t.Fatalf("validate auto negotiation intent failed: %v", err)
	}
}

// TestValidateForwardIntentRejectUnknownSource 验证未知来源会被拒绝。
func TestValidateForwardIntentRejectUnknownSource(t *testing.T) {
	t.Parallel()

	intent := testkit.GoldenForwardIntentPortMapping()
	intent.SourceOrder = []pb.ForwardSource{pb.ForwardSource("unknown")}
	err := ValidateForwardIntent(intent)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 来源值非法应返回 unsupported value 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedValue) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateForwardDecisionAccepted 验证成功决策校验。
func TestValidateForwardDecisionAccepted(t *testing.T) {
	t.Parallel()

	decision := testkit.GoldenForwardDecisionAccepted()
	if err := ValidateForwardDecision(decision); err != nil {
		t.Fatalf("validate forward decision failed: %v", err)
	}
}

// TestValidateForwardDecisionRejectMissingErrorCode 验证拒绝决策必须携带错误码。
func TestValidateForwardDecisionRejectMissingErrorCode(t *testing.T) {
	t.Parallel()

	decision := pb.ForwardDecision{
		Accepted:  false,
		RequestID: "forward-req-003",
		Mode:      pb.ForwardModeAutoNegotiation,
		NegotiationResult: pb.NegotiationResult{
			Accepted:     false,
			ErrorCode:    "NEGOTIATION_UNSUPPORTED_FEATURE",
			ErrorMessage: "required feature missing",
		},
	}
	err := ValidateForwardDecision(decision)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 拒绝决策缺 errorCode 时应返回缺失字段错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}
