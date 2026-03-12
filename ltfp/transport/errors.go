package transport

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrClosed 表示资源已关闭。
	ErrClosed = errors.New("closed")
	// ErrNotReady 表示资源尚未就绪。
	ErrNotReady = errors.New("not ready")
	// ErrSessionClosed 表示 session 已关闭或不可用。
	ErrSessionClosed = errors.New("session closed")
	// ErrTunnelClosed 表示 tunnel 已关闭。
	ErrTunnelClosed = errors.New("tunnel closed")
	// ErrTunnelBroken 表示 tunnel 已损坏。
	ErrTunnelBroken = errors.New("tunnel broken")
	// ErrTimeout 表示操作超时。
	ErrTimeout = errors.New("timeout")
	// ErrUnsupported 表示能力不支持。
	ErrUnsupported = errors.New("unsupported capability")
	// ErrInvalidArgument 表示参数非法。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrStateTransition 表示状态迁移非法。
	ErrStateTransition = errors.New("invalid state transition")
	// ErrTunnelNotFound 表示未找到目标 tunnel。
	ErrTunnelNotFound = errors.New("tunnel not found")
	// ErrTunnelInUse 表示 tunnel 已在使用中。
	ErrTunnelInUse = errors.New("tunnel in use")
	// ErrPoolExhausted 表示池容量已达到上限。
	ErrPoolExhausted = errors.New("pool exhausted")
)

// ErrorKind 表示 transport 错误分类。
type ErrorKind string

const (
	// ErrorKindTransport 表示底层传输错误。
	ErrorKindTransport ErrorKind = "transport"
	// ErrorKindProtocol 表示协议顺序或格式错误。
	ErrorKindProtocol ErrorKind = "protocol"
	// ErrorKindReject 表示业务拒绝。
	ErrorKindReject ErrorKind = "reject"
	// ErrorKindTimeout 表示超时类错误。
	ErrorKindTimeout ErrorKind = "timeout"
	// ErrorKindClosed 表示资源关闭类错误。
	ErrorKindClosed ErrorKind = "closed"
	// ErrorKindInternal 表示实现内部错误。
	ErrorKindInternal ErrorKind = "internal"
)

// Error 定义 transport 统一错误结构。
type Error struct {
	Kind      ErrorKind
	Op        string
	Message   string
	Temporary bool
	Cause     error
}

// Error 返回错误字符串。
func (err *Error) Error() string {
	if err == nil {
		// 兼容 nil 接收者，避免调用方日志输出 panic。
		return "<nil>"
	}
	parts := make([]string, 0, 4)
	if err.Kind != "" {
		// 错误种类放在最前，方便日志聚类。
		parts = append(parts, string(err.Kind))
	}
	if trimmedOp := strings.TrimSpace(err.Op); trimmedOp != "" {
		// 操作名用于定位失败阶段。
		parts = append(parts, trimmedOp)
	}
	if trimmedMessage := strings.TrimSpace(err.Message); trimmedMessage != "" {
		// 业务可读 message 放在中间部分。
		parts = append(parts, trimmedMessage)
	}
	result := strings.Join(parts, ": ")
	if err.Cause != nil {
		// 统一把底层原因拼接在尾部，保留原始错误信息。
		return fmt.Sprintf("%s: %s", result, err.Cause.Error())
	}
	if result == "" {
		// 兜底场景返回 internal，避免空字符串错误。
		return string(ErrorKindInternal)
	}
	return result
}

// Unwrap 返回底层错误，支持 errors.Is / errors.As。
func (err *Error) Unwrap() error {
	if err == nil {
		// nil 接收者没有可展开的错误。
		return nil
	}
	return err.Cause
}

// WrapError 将底层错误包装为 transport Error。
func WrapError(kind ErrorKind, operation string, cause error, message string) *Error {
	return &Error{
		Kind:    kind,
		Op:      strings.TrimSpace(operation),
		Message: strings.TrimSpace(message),
		Cause:   cause,
	}
}

// Wrapf 按格式化方式包装错误并保留底层 cause。
func Wrapf(kind ErrorKind, operation string, cause error, format string, arguments ...any) *Error {
	return WrapError(kind, operation, cause, fmt.Sprintf(format, arguments...))
}

// IsErrorKind 判断 error 是否为指定 ErrorKind。
func IsErrorKind(err error, kind ErrorKind) bool {
	typedErr := &Error{}
	if !errors.As(err, &typedErr) {
		// 不是 transport Error 时直接返回 false。
		return false
	}
	return typedErr.Kind == kind
}
