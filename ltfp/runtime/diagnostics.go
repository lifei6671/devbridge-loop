package runtime

import (
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TrafficDiagnosticFields 描述 traffic 维度的结构化日志字段。
type TrafficDiagnosticFields struct {
	TrafficID    string
	ServiceID    string
	SessionID    string
	SessionEpoch uint64
	TunnelID     string
	Binding      string
	FrameType    TrafficFrameType
	LastError    string
}

// BuildTrafficDiagnosticFields 构造 traffic 维度统一诊断字段。
func BuildTrafficDiagnosticFields(
	meta TrafficMeta,
	tunnel transport.Tunnel,
	frameType TrafficFrameType,
	lastError error,
) TrafficDiagnosticFields {
	fields := TrafficDiagnosticFields{
		TrafficID:    strings.TrimSpace(meta.TrafficID),
		ServiceID:    strings.TrimSpace(meta.ServiceID),
		SessionID:    strings.TrimSpace(meta.SessionID),
		SessionEpoch: meta.SessionEpoch,
		FrameType:    frameType,
	}
	if tunnel != nil {
		tunnelMeta := tunnel.Meta()
		fields.TunnelID = strings.TrimSpace(tunnelMeta.TunnelID)
		fields.Binding = strings.TrimSpace(tunnel.BindingInfo().Type.String())
		if fields.SessionID == "" {
			fields.SessionID = strings.TrimSpace(tunnelMeta.SessionID)
		}
		if fields.SessionEpoch == 0 {
			fields.SessionEpoch = tunnelMeta.SessionEpoch
		}
	}
	if lastError != nil {
		fields.LastError = strings.TrimSpace(lastError.Error())
	}
	return fields
}

// Fields 返回适合结构化日志输出的字段映射。
func (fields TrafficDiagnosticFields) Fields() map[string]any {
	return map[string]any{
		"traffic_id":    fields.TrafficID,
		"service_id":    fields.ServiceID,
		"session_id":    fields.SessionID,
		"session_epoch": fields.SessionEpoch,
		"tunnel_id":     fields.TunnelID,
		"binding":       fields.Binding,
		"frame_type":    fields.FrameType,
		"last_error":    fields.LastError,
	}
}
