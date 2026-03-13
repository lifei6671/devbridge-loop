package transport

import "strings"

// SessionDiagnosticFields 描述 session 维度的结构化日志字段。
type SessionDiagnosticFields struct {
	SessionID     string
	SessionEpoch  uint64
	Binding       string
	State         SessionState
	ProtocolState ProtocolState
	LastError     string
}

// BuildSessionDiagnosticFields 构造 session 维度统一诊断字段。
func BuildSessionDiagnosticFields(
	meta SessionMeta,
	bindingInfo BindingInfo,
	state SessionState,
	protocolState ProtocolState,
	lastError error,
) SessionDiagnosticFields {
	fields := SessionDiagnosticFields{
		SessionID:     strings.TrimSpace(meta.SessionID),
		SessionEpoch:  meta.SessionEpoch,
		Binding:       strings.TrimSpace(bindingInfo.Type.String()),
		State:         state,
		ProtocolState: protocolState,
	}
	if lastError != nil {
		// 实时错误优先，便于定位当前故障。
		fields.LastError = strings.TrimSpace(lastError.Error())
	} else {
		fields.LastError = strings.TrimSpace(meta.LastError)
	}
	return fields
}

// BuildSessionDiagnosticFieldsForSession 从 Session 直接提取诊断字段。
func BuildSessionDiagnosticFieldsForSession(session Session) SessionDiagnosticFields {
	if session == nil {
		return SessionDiagnosticFields{}
	}
	return BuildSessionDiagnosticFields(
		session.Meta(),
		session.BindingInfo(),
		session.State(),
		session.ProtocolState(),
		session.Err(),
	)
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields SessionDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"session_id":     fields.SessionID,
		"session_epoch":  fields.SessionEpoch,
		"binding":        fields.Binding,
		"session_state":  fields.State,
		"protocol_state": fields.ProtocolState,
		"last_error":     fields.LastError,
	}
}

// TunnelDiagnosticFields 描述 tunnel 维度的结构化日志字段。
type TunnelDiagnosticFields struct {
	SessionID    string
	SessionEpoch uint64
	Binding      string
	TunnelID     string
	TunnelState  TunnelState
	LastError    string
}

// BuildTunnelDiagnosticFields 构造 tunnel 维度统一诊断字段。
func BuildTunnelDiagnosticFields(
	meta TunnelMeta,
	bindingInfo BindingInfo,
	state TunnelState,
	lastError error,
) TunnelDiagnosticFields {
	fields := TunnelDiagnosticFields{
		SessionID:    strings.TrimSpace(meta.SessionID),
		SessionEpoch: meta.SessionEpoch,
		Binding:      strings.TrimSpace(bindingInfo.Type.String()),
		TunnelID:     strings.TrimSpace(meta.TunnelID),
		TunnelState:  state,
	}
	if lastError != nil {
		fields.LastError = strings.TrimSpace(lastError.Error())
	}
	return fields
}

// BuildTunnelDiagnosticFieldsForTunnel 从 Tunnel 直接提取诊断字段。
func BuildTunnelDiagnosticFieldsForTunnel(tunnel Tunnel) TunnelDiagnosticFields {
	if tunnel == nil {
		return TunnelDiagnosticFields{}
	}
	return BuildTunnelDiagnosticFields(
		tunnel.Meta(),
		tunnel.BindingInfo(),
		tunnel.State(),
		tunnel.Err(),
	)
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields TunnelDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"session_id":    fields.SessionID,
		"session_epoch": fields.SessionEpoch,
		"binding":       fields.Binding,
		"tunnel_id":     fields.TunnelID,
		"tunnel_state":  fields.TunnelState,
		"last_error":    fields.LastError,
	}
}

// PoolDiagnosticFields 描述 tunnel pool 维度的结构化日志字段。
type PoolDiagnosticFields struct {
	SessionID       string
	SessionEpoch    uint64
	Binding         string
	IdleCount       int
	InUseCount      int
	TargetIdleCount int
	LastError       string
}

// BuildPoolDiagnosticFields 构造 pool report 维度统一诊断字段。
func BuildPoolDiagnosticFields(report TunnelPoolReport, bindingInfo BindingInfo, lastError error) PoolDiagnosticFields {
	fields := PoolDiagnosticFields{
		SessionID:       strings.TrimSpace(report.SessionID),
		SessionEpoch:    report.SessionEpoch,
		Binding:         strings.TrimSpace(bindingInfo.Type.String()),
		IdleCount:       report.IdleCount,
		InUseCount:      report.InUseCount,
		TargetIdleCount: report.TargetIdleCount,
	}
	if lastError != nil {
		fields.LastError = strings.TrimSpace(lastError.Error())
	}
	return fields
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields PoolDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"session_id":        fields.SessionID,
		"session_epoch":     fields.SessionEpoch,
		"binding":           fields.Binding,
		"idle_count":        fields.IdleCount,
		"in_use_count":      fields.InUseCount,
		"target_idle_count": fields.TargetIdleCount,
		"last_error":        fields.LastError,
	}
}

// PoolRefillDiagnosticFields 描述补池动作维度的结构化日志字段。
type PoolRefillDiagnosticFields struct {
	SessionID          string
	SessionEpoch       uint64
	Binding            string
	RequestID          string
	Reason             TunnelRefillReason
	RequestedIdleDelta int
	EffectiveTarget    int
	OpenedCount        int
	FailedCount        int
	AfterIdleCount     int
	LastError          string
}

// BuildPoolRefillDiagnosticFields 构造补池动作统一诊断字段。
func BuildPoolRefillDiagnosticFields(
	request TunnelRefillRequest,
	result RefillResult,
	bindingInfo BindingInfo,
	lastError error,
) PoolRefillDiagnosticFields {
	fields := PoolRefillDiagnosticFields{
		SessionID:          strings.TrimSpace(request.SessionID),
		SessionEpoch:       request.SessionEpoch,
		Binding:            strings.TrimSpace(bindingInfo.Type.String()),
		RequestID:          strings.TrimSpace(request.RequestID),
		Reason:             request.Reason,
		RequestedIdleDelta: request.RequestedIdleDelta,
		EffectiveTarget:    result.EffectiveTargetIdle,
		OpenedCount:        result.OpenedCount,
		FailedCount:        result.FailedCount,
		AfterIdleCount:     result.AfterIdleCount,
	}
	if lastError != nil {
		fields.LastError = strings.TrimSpace(lastError.Error())
	}
	return fields
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields PoolRefillDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"session_id":            fields.SessionID,
		"session_epoch":         fields.SessionEpoch,
		"binding":               fields.Binding,
		"request_id":            fields.RequestID,
		"reason":                fields.Reason,
		"requested_idle_delta":  fields.RequestedIdleDelta,
		"effective_target_idle": fields.EffectiveTarget,
		"opened_count":          fields.OpenedCount,
		"failed_count":          fields.FailedCount,
		"after_idle_count":      fields.AfterIdleCount,
		"last_error":            fields.LastError,
	}
}
