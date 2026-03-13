package obs

import (
	"fmt"
	"strings"
)

const (
	// LogFieldTraceID 表示统一链路追踪 ID 字段名。
	LogFieldTraceID = "trace_id"
	// LogFieldTrafficID 表示 traffic ID 字段名。
	LogFieldTrafficID = "traffic_id"
	// LogFieldRouteID 表示 route ID 字段名。
	LogFieldRouteID = "route_id"
	// LogFieldServiceID 表示 service ID 字段名。
	LogFieldServiceID = "service_id"
	// LogFieldActualEndpointID 表示实际 endpoint ID 字段名。
	LogFieldActualEndpointID = "actual_endpoint_id"
	// LogFieldActualEndpointAddr 表示实际 endpoint 地址字段名。
	LogFieldActualEndpointAddr = "actual_endpoint_addr"
	// LogFieldSessionID 表示 session ID 字段名。
	LogFieldSessionID = "session_id"
	// LogFieldSessionEpoch 表示 session epoch 字段名。
	LogFieldSessionEpoch = "session_epoch"
	// LogFieldConnectorID 表示 connector ID 字段名。
	LogFieldConnectorID = "connector_id"
	// LogFieldTunnelID 表示 tunnel ID 字段名。
	LogFieldTunnelID = "tunnel_id"
)

// LogFields 定义 Bridge 关键路径统一日志字段。
type LogFields struct {
	TraceID            string
	TrafficID          string
	RouteID            string
	ServiceID          string
	ActualEndpointID   string
	ActualEndpointAddr string
	SessionID          string
	SessionEpoch       uint64
	ConnectorID        string
	TunnelID           string
}

// Logger defines structured logging dependencies for the bridge runtime.
type Logger struct{}

// FormatLogFields 输出统一日志字段，便于关键路径拼接结构化日志。
func FormatLogFields(fields LogFields) string {
	normalizedFields := normalizeLogFields(fields)
	return fmt.Sprintf(
		"%s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%d %s=%s %s=%s",
		LogFieldTraceID, normalizedFields.TraceID,
		LogFieldTrafficID, normalizedFields.TrafficID,
		LogFieldRouteID, normalizedFields.RouteID,
		LogFieldServiceID, normalizedFields.ServiceID,
		LogFieldActualEndpointID, normalizedFields.ActualEndpointID,
		LogFieldActualEndpointAddr, normalizedFields.ActualEndpointAddr,
		LogFieldSessionID, normalizedFields.SessionID,
		LogFieldSessionEpoch, normalizedFields.SessionEpoch,
		LogFieldConnectorID, normalizedFields.ConnectorID,
		LogFieldTunnelID, normalizedFields.TunnelID,
	)
}

// normalizeLogFields 对统一日志字段做去空白归一化，确保日志格式稳定。
func normalizeLogFields(fields LogFields) LogFields {
	return LogFields{
		TraceID:            strings.TrimSpace(fields.TraceID),
		TrafficID:          strings.TrimSpace(fields.TrafficID),
		RouteID:            strings.TrimSpace(fields.RouteID),
		ServiceID:          strings.TrimSpace(fields.ServiceID),
		ActualEndpointID:   strings.TrimSpace(fields.ActualEndpointID),
		ActualEndpointAddr: strings.TrimSpace(fields.ActualEndpointAddr),
		SessionID:          strings.TrimSpace(fields.SessionID),
		SessionEpoch:       fields.SessionEpoch,
		ConnectorID:        strings.TrimSpace(fields.ConnectorID),
		TunnelID:           strings.TrimSpace(fields.TunnelID),
	}
}
