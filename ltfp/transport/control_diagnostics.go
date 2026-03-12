package transport

import "strings"

// ControlDiagnosticFields 描述控制面错误与重试日志的统一字段。
type ControlDiagnosticFields struct {
	SessionID    string
	SessionEpoch uint64
	Binding      string
	LastError    string
	Retryable    bool
}

// BuildControlDiagnosticFields 从会话元信息构造统一诊断字段。
func BuildControlDiagnosticFields(
	meta SessionMeta,
	bindingInfo BindingInfo,
	lastError error,
	retryable bool,
) ControlDiagnosticFields {
	fields := ControlDiagnosticFields{
		SessionID:    strings.TrimSpace(meta.SessionID),
		SessionEpoch: meta.SessionEpoch,
		Binding:      strings.TrimSpace(bindingInfo.Type.String()),
		Retryable:    retryable,
	}
	if lastError != nil {
		// 显式传入的错误优先级最高，便于记录当前失败原因。
		fields.LastError = strings.TrimSpace(lastError.Error())
	} else {
		// 未传错误时回退到 session meta 中的 last_error 快照。
		fields.LastError = strings.TrimSpace(meta.LastError)
	}
	return fields
}

// BuildControlDiagnosticFieldsForSession 从 transport.Session 直接提取统一诊断字段。
func BuildControlDiagnosticFieldsForSession(session Session, retryable bool) ControlDiagnosticFields {
	if session == nil {
		// nil session 时只保留 retryable，避免调用方再做额外判空。
		return ControlDiagnosticFields{Retryable: retryable}
	}
	return BuildControlDiagnosticFields(session.Meta(), session.BindingInfo(), session.Err(), retryable)
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields ControlDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"session_id":    fields.SessionID,
		"session_epoch": fields.SessionEpoch,
		"binding":       fields.Binding,
		"last_error":    fields.LastError,
		"retryable":     fields.Retryable,
	}
}
