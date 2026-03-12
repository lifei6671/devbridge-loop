package validate

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// TestValidatePolicyJSONEmpty 验证空 policy_json 允许通过且无告警。
func TestValidatePolicyJSONEmpty(t *testing.T) {
	t.Parallel()

	warnings, err := ValidatePolicyJSON("")
	if err != nil {
		t.Fatalf("validate policy json failed: %v", err)
	}
	// 空策略不应产生告警。
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

// TestValidatePolicyJSONWithWarning 验证合法 policy_json 返回过渡告警。
func TestValidatePolicyJSONWithWarning(t *testing.T) {
	t.Parallel()

	warnings, err := ValidatePolicyJSON(`{"retry":{"maxAttempts":1}}`)
	if err != nil {
		t.Fatalf("validate policy json failed: %v", err)
	}
	// 非空策略应返回实验字段告警。
	if len(warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(warnings))
	}
	if warnings[0].Code != PolicyWarningExperimental {
		t.Fatalf("unexpected warning code: %s", warnings[0].Code)
	}
}

// TestValidatePolicyJSONRejectInvalid 验证非法 json 会被拒绝。
func TestValidatePolicyJSONRejectInvalid(t *testing.T) {
	t.Parallel()

	_, err := ValidatePolicyJSON(`{"retry":`)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 非法 JSON 应返回 INVALID_PAYLOAD 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidPayload) {
		t.Fatalf("unexpected error: %v", err)
	}
}
