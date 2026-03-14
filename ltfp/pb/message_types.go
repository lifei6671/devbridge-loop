package pb

import "strings"

// ControlMessageType 定义控制面消息类型。
type ControlMessageType string

const (
	// ControlMessageConnectorHello 表示连接器发起握手。
	ControlMessageConnectorHello ControlMessageType = "ConnectorHello"
	// ControlMessageConnectorWelcome 表示服务端返回握手参数。
	ControlMessageConnectorWelcome ControlMessageType = "ConnectorWelcome"
	// ControlMessageConnectorAuth 表示连接器提交认证信息。
	ControlMessageConnectorAuth ControlMessageType = "ConnectorAuth"
	// ControlMessageConnectorAuthAck 表示服务端返回认证结果。
	ControlMessageConnectorAuthAck ControlMessageType = "ConnectorAuthAck"
	// ControlMessageHeartbeat 表示长连接心跳。
	ControlMessageHeartbeat ControlMessageType = "Heartbeat"
	// ControlMessagePublishService 表示发布服务。
	ControlMessagePublishService ControlMessageType = "PublishService"
	// ControlMessagePublishServiceAck 表示发布服务 ACK。
	ControlMessagePublishServiceAck ControlMessageType = "PublishServiceAck"
	// ControlMessageUnpublishService 表示下线服务。
	ControlMessageUnpublishService ControlMessageType = "UnpublishService"
	// ControlMessageUnpublishServiceAck 表示下线服务 ACK。
	ControlMessageUnpublishServiceAck ControlMessageType = "UnpublishServiceAck"
	// ControlMessageServiceHealthReport 表示上报健康状态。
	ControlMessageServiceHealthReport ControlMessageType = "ServiceHealthReport"
	// ControlMessageTunnelPoolReport 表示 Agent 上报 tunnel 池状态。
	ControlMessageTunnelPoolReport ControlMessageType = "TunnelPoolReport"
	// ControlMessageTunnelRefillRequest 表示 Bridge 请求 Agent 补池。
	ControlMessageTunnelRefillRequest ControlMessageType = "TunnelRefillRequest"
	// ControlMessageRouteAssign 表示下发 route。
	ControlMessageRouteAssign ControlMessageType = "RouteAssign"
	// ControlMessageRouteAssignAck 表示 route 下发 ACK。
	ControlMessageRouteAssignAck ControlMessageType = "RouteAssignAck"
	// ControlMessageRouteRevoke 表示撤销 route。
	ControlMessageRouteRevoke ControlMessageType = "RouteRevoke"
	// ControlMessageRouteRevokeAck 表示撤销 route ACK。
	ControlMessageRouteRevokeAck ControlMessageType = "RouteRevokeAck"
	// ControlMessageRouteStatusReport 表示 route 状态上报。
	ControlMessageRouteStatusReport ControlMessageType = "RouteStatusReport"
	// ControlMessageControlError 表示控制面错误。
	ControlMessageControlError ControlMessageType = "ControlError"
)

// SessionState 定义连接会话状态。
type SessionState string

const (
	// SessionStateConnecting 表示底层连接建立中。
	SessionStateConnecting SessionState = "CONNECTING"
	// SessionStateAuthenticating 表示握手与认证中。
	SessionStateAuthenticating SessionState = "AUTHENTICATING"
	// SessionStateActive 表示会话已可承载新流量。
	SessionStateActive SessionState = "ACTIVE"
	// SessionStateDraining 表示会话仅允许收尾不允许新流量。
	SessionStateDraining SessionState = "DRAINING"
	// SessionStateStale 表示会话已失活。
	SessionStateStale SessionState = "STALE"
	// SessionStateClosed 表示会话已关闭。
	SessionStateClosed SessionState = "CLOSED"
)

// ServiceStatus 定义服务生命周期状态。
type ServiceStatus string

const (
	// ServiceStatusActive 表示服务可参与路由解析。
	ServiceStatusActive ServiceStatus = "ACTIVE"
	// ServiceStatusInactive 表示服务不可参与路由解析。
	ServiceStatusInactive ServiceStatus = "INACTIVE"
	// ServiceStatusStale 表示服务记录仅用于审计。
	ServiceStatusStale ServiceStatus = "STALE"
)

// HealthStatus 定义健康状态。
type HealthStatus string

const (
	// HealthStatusHealthy 表示健康。
	HealthStatusHealthy HealthStatus = "HEALTHY"
	// HealthStatusUnhealthy 表示不健康。
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
	// HealthStatusUnknown 表示未知健康状态。
	HealthStatusUnknown HealthStatus = "UNKNOWN"
)

// EventStatus 定义资源级事件 ACK 结果。
type EventStatus string

const (
	// EventStatusAccepted 表示事件被接收并应用。
	EventStatusAccepted EventStatus = "accepted"
	// EventStatusDuplicate 表示事件重复并按幂等忽略。
	EventStatusDuplicate EventStatus = "duplicate"
	// EventStatusRejected 表示事件被拒绝。
	EventStatusRejected EventStatus = "rejected"
)

// RouteTargetType 定义 route 目标类型。
type RouteTargetType string

const (
	// RouteTargetTypeConnectorService 表示目标为 connector 发布服务。
	RouteTargetTypeConnectorService RouteTargetType = "connector_service"
	// RouteTargetTypeExternalService 表示目标为外部发现服务。
	RouteTargetTypeExternalService RouteTargetType = "external_service"
	// RouteTargetTypeHybridGroup 表示目标为混合组。
	RouteTargetTypeHybridGroup RouteTargetType = "hybrid_group"
)

// IngressMode 定义入口暴露模式。
type IngressMode string

const (
	// IngressModeL7Shared 表示 L7 共享入口。
	IngressModeL7Shared IngressMode = "l7_shared"
	// IngressModeTLSSNIShared 表示 TLS SNI 共享入口。
	IngressModeTLSSNIShared IngressMode = "tls_sni_shared"
	// IngressModeL4DedicatedPort 表示 L4 专属端口入口。
	IngressModeL4DedicatedPort IngressMode = "l4_dedicated_port"
)

// FallbackPolicy 定义 hybrid fallback 策略。
type FallbackPolicy string

const (
	// FallbackPolicyPreOpenOnly 表示只允许 pre-open 阶段 fallback。
	FallbackPolicyPreOpenOnly FallbackPolicy = "pre_open_only"
)

// IsKnownControlMessageType 判断控制面消息类型是否在协议定义内。
func IsKnownControlMessageType(messageType ControlMessageType) bool {
	normalized := strings.TrimSpace(string(messageType))
	// 通过 switch 常量集合确保新类型新增时编译期可见。
	switch ControlMessageType(normalized) {
	case ControlMessageConnectorHello, ControlMessageConnectorWelcome, ControlMessageConnectorAuth, ControlMessageConnectorAuthAck:
		return true
	case ControlMessageHeartbeat, ControlMessagePublishService, ControlMessagePublishServiceAck:
		return true
	case ControlMessageUnpublishService, ControlMessageUnpublishServiceAck, ControlMessageServiceHealthReport:
		return true
	case ControlMessageTunnelPoolReport, ControlMessageTunnelRefillRequest:
		return true
	case ControlMessageRouteAssign, ControlMessageRouteAssignAck, ControlMessageRouteRevoke, ControlMessageRouteRevokeAck:
		return true
	case ControlMessageRouteStatusReport, ControlMessageControlError:
		return true
	default:
		return false
	}
}
