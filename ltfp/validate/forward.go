package validate

import (
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// ValidateNegotiationProfile 校验请求级协商输入配置。
func ValidateNegotiationProfile(profile pb.NegotiationProfile) error {
	// 版本主号必须大于 0，否则无法进行兼容性判定。
	if profile.VersionMajor == 0 {
		return ltfperrors.New(ltfperrors.CodeNegotiationInvalidProfile, "versionMajor must be greater than 0")
	}
	for index, feature := range profile.RequiredFeatures {
		// required feature 不能为空字符串，否则会污染协商过程。
		if strings.TrimSpace(feature) == "" {
			return ltfperrors.New(ltfperrors.CodeNegotiationInvalidProfile, fmt.Sprintf("requiredFeatures[%d] is empty", index))
		}
	}
	for index, feature := range profile.OptionalFeatures {
		// optional feature 不能为空字符串，否则会污染协商过程。
		if strings.TrimSpace(feature) == "" {
			return ltfperrors.New(ltfperrors.CodeNegotiationInvalidProfile, fmt.Sprintf("optionalFeatures[%d] is empty", index))
		}
	}
	return nil
}

// ValidateNegotiationResult 校验请求级协商结果。
func ValidateNegotiationResult(result pb.NegotiationResult) error {
	if result.Accepted {
		// 协商成功时不应携带错误码，避免语义冲突。
		if strings.TrimSpace(result.ErrorCode) != "" {
			return ltfperrors.New(ltfperrors.CodeInvalidPayload, "errorCode must be empty when negotiation accepted")
		}
		return nil
	}
	// 协商失败时至少应携带错误码或 missingRequired 之一，便于上层诊断。
	if strings.TrimSpace(result.ErrorCode) == "" && len(result.MissingRequired) == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidPayload, "negotiation reject must include errorCode or missingRequired")
	}
	return nil
}

// ValidateForwardIntent 校验请求级转发意图。
func ValidateForwardIntent(intent pb.ForwardIntent) error {
	// requestId 是请求级幂等和追踪关键字段，不能为空。
	if strings.TrimSpace(intent.RequestID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardIntent.requestId is required")
	}
	// mode 必须属于协议定义集合。
	if !pb.IsKnownForwardMode(intent.Mode) {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported forward mode: %s", intent.Mode))
	}

	for index, source := range intent.SourceOrder {
		// sourceOrder 中每个来源都必须是已知值。
		if !pb.IsKnownForwardSource(source) {
			return ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported sourceOrder[%d]: %s", index, source))
		}
	}

	// 端口映射模式要求绑定固定 scope 与 serviceName，防止被请求方覆盖。
	if intent.Mode == pb.ForwardModePortMapping {
		if strings.TrimSpace(intent.Namespace) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardIntent.namespace is required for port_mapping mode")
		}
		if strings.TrimSpace(intent.Environment) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardIntent.environment is required for port_mapping mode")
		}
		if strings.TrimSpace(intent.ServiceName) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardIntent.serviceName is required for port_mapping mode")
		}
	}

	// 自动协商模式要求携带可校验的协商 profile。
	if intent.Mode == pb.ForwardModeAutoNegotiation {
		if err := ValidateNegotiationProfile(intent.NegotiationProfile); err != nil {
			return err
		}
	}
	return nil
}

// ValidateForwardDecision 校验请求级转发决策。
func ValidateForwardDecision(decision pb.ForwardDecision) error {
	// requestId 必须与 intent 对齐，不能为空。
	if strings.TrimSpace(decision.RequestID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardDecision.requestId is required")
	}
	// mode 必须属于协议定义集合。
	if !pb.IsKnownForwardMode(decision.Mode) {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported forward mode: %s", decision.Mode))
	}
	if decision.Accepted {
		// 接受决策时 selectedSource 必须有效。
		if !pb.IsKnownForwardSource(decision.SelectedSource) {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardDecision.selectedSource is required when accepted")
		}
		// 接受决策时不应携带错误码，避免语义冲突。
		if strings.TrimSpace(decision.ErrorCode) != "" {
			return ltfperrors.New(ltfperrors.CodeInvalidPayload, "forwardDecision.errorCode must be empty when accepted")
		}
	} else {
		// 拒绝决策时建议返回 errorCode 供上层分类处理。
		if strings.TrimSpace(decision.ErrorCode) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "forwardDecision.errorCode is required when rejected")
		}
	}
	// 决策内协商结果若存在则必须满足格式约束。
	if err := ValidateNegotiationResult(decision.NegotiationResult); err != nil {
		return err
	}
	return nil
}
