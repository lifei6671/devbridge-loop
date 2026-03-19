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
	// LogFieldLogicalServiceID 表示逻辑服务 ID 字段名。
	LogFieldLogicalServiceID = "logical_service_id"
	// LogFieldServiceName 表示服务名字段名。
	LogFieldServiceName = "service_name"
	// LogFieldInstanceID 表示实例 ID 字段名。
	LogFieldInstanceID = "instance_id"
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
	// LogFieldTokenID 表示脱敏后的 token ID 字段名。
	LogFieldTokenID = "token_id"
	// LogFieldSourceIP 表示远端源 IP 字段名。
	LogFieldSourceIP = "source_ip"
	// LogFieldErrorCode 表示标准错误码字段名。
	LogFieldErrorCode = "error_code"
	// LogFieldTunnelID 表示 tunnel ID 字段名。
	LogFieldTunnelID = "tunnel_id"
	// LogFieldRequestScope 表示请求原始 scope 字段名。
	LogFieldRequestScope = "request_scope"
	// LogFieldMatchedScope 表示最终命中的 scope 字段名。
	LogFieldMatchedScope = "matched_scope"
	// LogFieldIsExternalFallback 表示是否通过 external fallback 命中。
	LogFieldIsExternalFallback = "is_external_fallback"
)

// LogFields 定义 Bridge 关键路径统一日志字段。
type LogFields struct {
	TraceID            string
	TrafficID          string
	RouteID            string
	LogicalServiceID   string
	ServiceName        string
	InstanceID         string
	ActualEndpointID   string
	ActualEndpointAddr string
	SessionID          string
	SessionEpoch       uint64
	ConnectorID        string
	TunnelID           string
	RequestScope       string
	MatchedScope       string
	IsExternalFallback bool
}

// Logger defines structured logging dependencies for the bridge runtime.
type Logger struct{}

// FormatLogFields 输出统一日志字段，便于关键路径拼接结构化日志。
func FormatLogFields(fields LogFields) string {
	normalizedFields := normalizeLogFields(fields)
	return fmt.Sprintf(
		"%s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%s %s=%d %s=%s %s=%s %s=%s %s=%s %s=%t",
		LogFieldTraceID, normalizedFields.TraceID,
		LogFieldTrafficID, normalizedFields.TrafficID,
		LogFieldRouteID, normalizedFields.RouteID,
		LogFieldLogicalServiceID, normalizedFields.LogicalServiceID,
		LogFieldServiceName, normalizedFields.ServiceName,
		LogFieldInstanceID, normalizedFields.InstanceID,
		LogFieldActualEndpointID, normalizedFields.ActualEndpointID,
		LogFieldActualEndpointAddr, normalizedFields.ActualEndpointAddr,
		LogFieldSessionID, normalizedFields.SessionID,
		LogFieldSessionEpoch, normalizedFields.SessionEpoch,
		LogFieldConnectorID, normalizedFields.ConnectorID,
		LogFieldTunnelID, normalizedFields.TunnelID,
		LogFieldRequestScope, normalizedFields.RequestScope,
		LogFieldMatchedScope, normalizedFields.MatchedScope,
		LogFieldIsExternalFallback, normalizedFields.IsExternalFallback,
	)
}

// normalizeLogFields 对统一日志字段做去空白归一化，确保日志格式稳定。
func normalizeLogFields(fields LogFields) LogFields {
	return LogFields{
		TraceID:            strings.TrimSpace(fields.TraceID),
		TrafficID:          strings.TrimSpace(fields.TrafficID),
		RouteID:            strings.TrimSpace(fields.RouteID),
		LogicalServiceID:   strings.TrimSpace(fields.LogicalServiceID),
		ServiceName:        strings.TrimSpace(fields.ServiceName),
		InstanceID:         strings.TrimSpace(fields.InstanceID),
		ActualEndpointID:   strings.TrimSpace(fields.ActualEndpointID),
		ActualEndpointAddr: strings.TrimSpace(fields.ActualEndpointAddr),
		SessionID:          strings.TrimSpace(fields.SessionID),
		SessionEpoch:       fields.SessionEpoch,
		ConnectorID:        strings.TrimSpace(fields.ConnectorID),
		TunnelID:           strings.TrimSpace(fields.TunnelID),
		RequestScope:       strings.TrimSpace(fields.RequestScope),
		MatchedScope:       strings.TrimSpace(fields.MatchedScope),
		IsExternalFallback: fields.IsExternalFallback,
	}
}
