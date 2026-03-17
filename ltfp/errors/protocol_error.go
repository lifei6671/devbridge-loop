package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
)

// ProtocolError 描述带错误码和可选原因链的协议错误。
type ProtocolError struct {
	Code    string
	Message string
	Cause   error
}

// Error 返回协议错误的可读文本。
func (err *ProtocolError) Error() string {
	// 无底层原因时直接返回 code+message，避免冗余包装文本。
	if err.Cause == nil {
		return fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	// 有底层原因时附加 cause 便于上层定位问题。
	return fmt.Sprintf("%s: %s: %v", err.Code, err.Message, err.Cause)
}

// Unwrap 返回底层错误，支持 errors.Is/errors.As。
func (err *ProtocolError) Unwrap() error {
	// Unwrap 只暴露 cause，不改动当前错误码语义。
	return err.Cause
}

// New 创建一个不带底层原因的协议错误。
func New(code, message string) *ProtocolError {
	// 构造函数统一收敛 code+message，减少重复样板代码。
	return &ProtocolError{
		Code:    code,
		Message: message,
	}
}

// Wrap 创建一个带底层原因链的协议错误。
func Wrap(code, message string, cause error) *ProtocolError {
	// Wrap 不吞掉原始错误，保留错误链供上层判断。
	return &ProtocolError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// IsCode 判断任意错误链中是否包含指定协议错误码。
func IsCode(err error, code string) bool {
	var protocolErr *ProtocolError
	// 先尝试提取协议错误，避免误判非协议错误。
	if !stderrors.As(err, &protocolErr) {
		return false
	}
	// 比较错误码时保持精确匹配，不做模糊比较。
	return protocolErr.Code == code
}

// ExtractCode 返回错误链中的协议错误码，不存在时返回空字符串。
func ExtractCode(err error) string {
	var protocolErr *ProtocolError
	// 提取失败时返回空字符串，调用方可按未知错误处理。
	if !stderrors.As(err, &protocolErr) {
		return ""
	}
	return protocolErr.Code
}

// NewTunnelRecycleError 创建带统一 recycle 错误码的协议错误。
func NewTunnelRecycleError(code string, message string) *ProtocolError {
	return New(
		NormalizeTunnelRecycleCodeOrDefault(code, CodeTunnelRecycleTunnelUnhealthy),
		strings.TrimSpace(message),
	)
}

// WrapTunnelRecycleError 包装 recycle 相关错误，并优先保留错误链里已有的 recycle 错误码。
func WrapTunnelRecycleError(code string, message string, cause error) *ProtocolError {
	recycleCode := ExtractTunnelRecycleCode(cause)
	if !IsKnownTunnelRecycleCode(recycleCode) {
		recycleCode = NormalizeTunnelRecycleCodeOrDefault(code, CodeTunnelRecycleTunnelUnhealthy)
	}
	return Wrap(recycleCode, strings.TrimSpace(message), cause)
}

// IsTunnelRecycleCode 判断错误链中是否包含指定 recycle 错误码。
func IsTunnelRecycleCode(err error, code string) bool {
	normalizedCode := NormalizeTunnelRecycleCode(code)
	if !IsKnownTunnelRecycleCode(normalizedCode) {
		return false
	}
	return IsCode(err, normalizedCode)
}

// ExtractTunnelRecycleCode 提取错误链中的 recycle 错误码；普通 deadline 超时会映射为 deadline_hit。
func ExtractTunnelRecycleCode(err error) string {
	extractedCode := NormalizeTunnelRecycleCode(ExtractCode(err))
	if IsKnownTunnelRecycleCode(extractedCode) {
		return extractedCode
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return CodeTunnelRecycleDeadlineHit
	}
	return ""
}

// ExtractTunnelRecycleMessage 提取 recycle 错误的人类可读消息。
func ExtractTunnelRecycleMessage(err error) string {
	if err == nil {
		return ""
	}
	var protocolErr *ProtocolError
	if stderrors.As(err, &protocolErr) && IsKnownTunnelRecycleCode(protocolErr.Code) {
		return strings.TrimSpace(protocolErr.Message)
	}
	return strings.TrimSpace(err.Error())
}
