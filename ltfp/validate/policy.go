package validate

import (
	"encoding/json"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// PolicyWarning 描述 policy_json 过渡期告警信息。
type PolicyWarning struct {
	Code    string
	Message string
}

const (
	// PolicyWarningExperimental 表示 policy_json 仅用于实验字段。
	PolicyWarningExperimental = "POLICY_JSON_EXPERIMENTAL"
)

// ValidatePolicyJSON 校验并返回 policy_json 过渡期告警。
func ValidatePolicyJSON(policyJSON string) ([]PolicyWarning, error) {
	normalized := strings.TrimSpace(policyJSON)
	// 空策略视为未配置，不返回告警也不返回错误。
	if normalized == "" {
		return nil, nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(normalized), &parsed); err != nil {
		// policy_json 非法时直接拒绝，避免携带脏配置进入系统。
		return nil, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "policy_json is not valid json object", err)
	}
	// 顶层必须是对象，数组或标量都不允许。
	if parsed == nil {
		return nil, ltfperrors.New(ltfperrors.CodeInvalidPayload, "policy_json must be a json object")
	}

	warnings := []PolicyWarning{
		{
			Code:    PolicyWarningExperimental,
			Message: "policy_json is temporary and should not be used as long-term extension channel",
		},
	}
	return warnings, nil
}
