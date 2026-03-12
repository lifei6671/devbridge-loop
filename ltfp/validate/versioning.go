package validate

import (
	"fmt"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// VersionSupport 描述当前节点支持的协议版本范围。
type VersionSupport struct {
	CurrentMajor uint32
	CurrentMinor uint32
	MinMajor     uint32
	MinMinor     uint32
}

// ValidateSchemaVersion 校验消息版本是否在支持范围内。
func ValidateSchemaVersion(messageMajor uint32, messageMinor uint32, support VersionSupport) error {
	// 支持范围配置必须合法，避免调用方传入零值导致误判。
	if support.CurrentMajor == 0 || support.MinMajor == 0 {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, "version support configuration is invalid")
	}
	// 主版本不一致直接拒绝，属于不兼容升级。
	if messageMajor != support.CurrentMajor {
		return ltfperrors.New(ltfperrors.CodeNegotiationUnsupportedVersion, fmt.Sprintf("unsupported major version: got=%d want=%d", messageMajor, support.CurrentMajor))
	}
	// 低于最小兼容 minor 时拒绝，防止老旧字段语义缺失。
	if messageMajor == support.MinMajor && messageMinor < support.MinMinor {
		return ltfperrors.New(ltfperrors.CodeNegotiationUnsupportedVersion, fmt.Sprintf("minor version is too old: got=%d min=%d", messageMinor, support.MinMinor))
	}
	// 高于当前 minor 时先拒绝，等待显式升级兼容策略。
	if messageMinor > support.CurrentMinor {
		return ltfperrors.New(ltfperrors.CodeNegotiationUnsupportedVersion, fmt.Sprintf("minor version is newer than current support: got=%d current=%d", messageMinor, support.CurrentMinor))
	}
	return nil
}
