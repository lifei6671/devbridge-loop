package errors

import "strings"

// 协议通用错误码定义。
const (
	// CodeInvalidPayload 表示消息体结构不合法或字段类型不匹配。
	CodeInvalidPayload = "INVALID_PAYLOAD"
	// CodeMissingRequiredField 表示缺少必填字段。
	CodeMissingRequiredField = "MISSING_REQUIRED_FIELD"
	// CodeUnsupportedValue 表示字段值不在支持范围内。
	CodeUnsupportedValue = "UNSUPPORTED_VALUE"
	// CodeInvalidSessionEpoch 表示会话代际非法。
	CodeInvalidSessionEpoch = "INVALID_SESSION_EPOCH"
	// CodeInvalidEventID 表示事件 ID 非法。
	CodeInvalidEventID = "INVALID_EVENT_ID"
	// CodeInvalidResourceVersion 表示资源版本非法。
	CodeInvalidResourceVersion = "INVALID_RESOURCE_VERSION"
	// CodeInvalidScope 表示 scope 校验失败。
	CodeInvalidScope = "INVALID_SCOPE"
	// CodeUnknownMessageType 表示消息类型未知。
	CodeUnknownMessageType = "UNKNOWN_MESSAGE_TYPE"
	// CodeStaleEpochEvent 表示收到旧会话 epoch 的事件。
	CodeStaleEpochEvent = "STALE_EPOCH_EVENT"
	// CodeDuplicateEvent 表示事件重复提交。
	CodeDuplicateEvent = "DUPLICATE_EVENT"
	// CodeVersionRollback 表示资源版本回退。
	CodeVersionRollback = "VERSION_ROLLBACK"
	// CodeMissingDependency 表示事件依赖的资源不存在。
	CodeMissingDependency = "MISSING_DEPENDENCY"
	// CodeInvalidStateTransition 表示会话状态流转非法。
	CodeInvalidStateTransition = "INVALID_STATE_TRANSITION"
	// CodeDiscoveryProviderNotAllowed 表示 provider 不在允许列表。
	CodeDiscoveryProviderNotAllowed = "DISCOVERY_PROVIDER_NOT_ALLOWED"
	// CodeDiscoveryNamespaceNotAllowed 表示 namespace 不在允许列表。
	CodeDiscoveryNamespaceNotAllowed = "DISCOVERY_NAMESPACE_NOT_ALLOWED"
	// CodeDiscoveryServiceNotAllowed 表示 serviceName 不在允许列表。
	CodeDiscoveryServiceNotAllowed = "DISCOVERY_SERVICE_NOT_ALLOWED"
	// CodeDiscoveryProviderUnavailable 表示 provider 查询失败且无可用缓存。
	CodeDiscoveryProviderUnavailable = "DISCOVERY_PROVIDER_UNAVAILABLE"
	// CodeDiscoveryNoEndpoint 表示查询后无可用 endpoint。
	CodeDiscoveryNoEndpoint = "DISCOVERY_NO_ENDPOINT"
	// CodeDiscoveryRefreshFailed 表示 stale 刷新尝试失败。
	CodeDiscoveryRefreshFailed = "DISCOVERY_REFRESH_FAILED"
	// CodeDiscoveryEndpointDenied 表示 endpoint 被安全策略拒绝。
	CodeDiscoveryEndpointDenied = "DISCOVERY_ENDPOINT_DENIED"
	// CodeResolveServiceNotFound 表示 route 解析时未找到目标服务。
	CodeResolveServiceNotFound = "RESOLVE_SERVICE_NOT_FOUND"
	// CodeResolveServiceUnavailable 表示 route 解析时目标服务不可用。
	CodeResolveServiceUnavailable = "RESOLVE_SERVICE_UNAVAILABLE"
	// CodeResolveSessionNotActive 表示 route 解析时会话非 ACTIVE。
	CodeResolveSessionNotActive = "RESOLVE_SESSION_NOT_ACTIVE"
	// CodeExportNotEligible 表示 export 条件不满足。
	CodeExportNotEligible = "EXPORT_NOT_ELIGIBLE"
	// CodeDirectProxyTimeout 表示 direct proxy 拨号超时。
	CodeDirectProxyTimeout = "DIRECT_PROXY_TIMEOUT"
	// CodeDirectProxyConcurrencyLimit 表示 direct proxy 并发达到上限。
	CodeDirectProxyConcurrencyLimit = "DIRECT_PROXY_CONCURRENCY_LIMIT"
	// CodeDirectProxyDialFailed 表示 direct proxy 拨号失败。
	CodeDirectProxyDialFailed = "DIRECT_PROXY_DIAL_FAILED"
	// CodeIngressPortConflict 表示 dedicated port 出现端口冲突。
	CodeIngressPortConflict = "INGRESS_PORT_CONFLICT"
	// CodeIngressRouteMismatch 表示 ingress 路由匹配条件冲突。
	CodeIngressRouteMismatch = "INGRESS_ROUTE_MISMATCH"
	// CodeUnsupportedLegacyProtocol 表示请求仍携带已废弃的旧协议字段。
	CodeUnsupportedLegacyProtocol = "UNSUPPORTED_LEGACY_PROTOCOL"
	// CodeInstanceOwnershipMismatch 表示 instance_id 与当前 connector 归属不一致。
	CodeInstanceOwnershipMismatch = "INSTANCE_OWNERSHIP_MISMATCH"
	// CodeStaleSessionEpoch 表示收到旧 session epoch 的实例级请求。
	CodeStaleSessionEpoch = "STALE_SESSION_EPOCH"
	// CodeInstanceNotFound 表示目标实例不存在或不归属于当前 connector。
	CodeInstanceNotFound = "INSTANCE_NOT_FOUND"
)

// 协商相关错误码定义。
const (
	// CodeNegotiationUnsupportedVersion 表示版本不兼容。
	CodeNegotiationUnsupportedVersion = "NEGOTIATION_UNSUPPORTED_VERSION"
	// CodeNegotiationUnsupportedFeature 表示缺少 required feature。
	CodeNegotiationUnsupportedFeature = "NEGOTIATION_UNSUPPORTED_FEATURE"
	// CodeNegotiationInvalidProfile 表示协商 profile 本身不合法。
	CodeNegotiationInvalidProfile = "NEGOTIATION_INVALID_PROFILE"
)

// 认证相关错误码定义。
const (
	// CodeAuthInvalidMethod 表示 auth_method 不在支持集合内。
	CodeAuthInvalidMethod = "auth_invalid_method"
	// CodeAuthInvalidToken 表示 token 格式错误、缺失或校验失败。
	CodeAuthInvalidToken = "auth_invalid_token"
	// CodeAuthTokenExpired 表示 token 已过期。
	CodeAuthTokenExpired = "auth_token_expired"
	// CodeAuthTokenRevoked 表示 token 已吊销。
	CodeAuthTokenRevoked = "auth_token_revoked"
	// CodeAuthConnectorMismatch 表示 token 与 connector_id 归属不一致。
	CodeAuthConnectorMismatch = "auth_connector_mismatch"
	// CodeAuthSessionSuperseded 表示候选 session_epoch 已被更高权威代际取代。
	CodeAuthSessionSuperseded = "auth_session_superseded"
	// CodeAuthRateLimited 表示认证抢占命中限流。
	CodeAuthRateLimited = "auth_rate_limited"
	// CodeAuthInternalError 表示认证链路内部错误。
	CodeAuthInternalError = "auth_internal_error"
)

// 数据面相关错误码定义。
const (
	// CodeTrafficInvalidOneof 表示数据面 oneof 字段设置冲突。
	CodeTrafficInvalidOneof = "TRAFFIC_INVALID_ONEOF"
	// CodeTrafficOpenRejected 表示 TrafficOpen 被拒绝。
	CodeTrafficOpenRejected = "TRAFFIC_OPEN_REJECTED"
	// CodeConnectorDialFailed 表示 connector path 在 Agent 侧 upstream dial 失败。
	CodeConnectorDialFailed = "CONNECTOR_DIAL_FAILED"
	// CodeDirectProxyRelayFailed 表示 direct proxy relay 过程失败。
	CodeDirectProxyRelayFailed = "DIRECT_PROXY_RELAY_FAILED"
	// CodeTrafficInvalidLogicalServiceID 表示 TrafficOpen 缺少 logical_service_id。
	CodeTrafficInvalidLogicalServiceID = "TRAFFIC_INVALID_LOGICAL_SERVICE_ID"
	// CodeTrafficInvalidInstanceID 表示 TrafficOpen 缺少 instance_id。
	CodeTrafficInvalidInstanceID = "TRAFFIC_INVALID_INSTANCE_ID"
)

// Tunnel recycle 握手相关错误码定义。
const (
	// CodeTunnelRecycleInvalidSeq 表示 recycle_seq 非法（非单调递增或缺失）。
	CodeTunnelRecycleInvalidSeq = "invalid_seq"
	// CodeTunnelRecycleCloseAckRequired 表示尚未观测到 TrafficCloseAck，不允许进入 recycle。
	CodeTunnelRecycleCloseAckRequired = "close_ack_required"
	// CodeTunnelRecycleTunnelUnhealthy 表示 tunnel 不满足回收健康条件。
	CodeTunnelRecycleTunnelUnhealthy = "tunnel_unhealthy"
	// CodeTunnelRecycleBufferDirty 表示 tunnel 仍有脏数据或 flush 失败。
	CodeTunnelRecycleBufferDirty = "buffer_dirty"
	// CodeTunnelRecycleTunnelMismatch 表示回收请求的 tunnel_id 与本地上下文不一致。
	CodeTunnelRecycleTunnelMismatch = "tunnel_mismatch"
	// CodeTunnelRecycleDeadlineHit 表示回收等待命中 deadline。
	CodeTunnelRecycleDeadlineHit = "deadline_hit"
)

// IsKnownTunnelRecycleCode 判断错误码是否属于协议约定的 recycle 错误集合。
func IsKnownTunnelRecycleCode(errorCode string) bool {
	switch strings.TrimSpace(errorCode) {
	case CodeTunnelRecycleInvalidSeq,
		CodeTunnelRecycleCloseAckRequired,
		CodeTunnelRecycleTunnelUnhealthy,
		CodeTunnelRecycleBufferDirty,
		CodeTunnelRecycleTunnelMismatch,
		CodeTunnelRecycleDeadlineHit:
		return true
	default:
		return false
	}
}

// NormalizeTunnelRecycleCode 归一化回收拒绝错误码，避免双端口径漂移。
func NormalizeTunnelRecycleCode(errorCode string) string {
	normalizedCode := strings.TrimSpace(errorCode)
	switch normalizedCode {
	case CodeTunnelRecycleInvalidSeq:
		return CodeTunnelRecycleInvalidSeq
	case CodeTunnelRecycleCloseAckRequired:
		return CodeTunnelRecycleCloseAckRequired
	case CodeTunnelRecycleTunnelUnhealthy:
		return CodeTunnelRecycleTunnelUnhealthy
	case CodeTunnelRecycleBufferDirty:
		return CodeTunnelRecycleBufferDirty
	case CodeTunnelRecycleTunnelMismatch:
		return CodeTunnelRecycleTunnelMismatch
	case CodeTunnelRecycleDeadlineHit:
		return CodeTunnelRecycleDeadlineHit
	default:
		return normalizedCode
	}
}

// NormalizeTunnelRecycleCodeOrDefault 归一化 recycle 错误码，未知时回退到指定默认值。
func NormalizeTunnelRecycleCodeOrDefault(errorCode string, fallbackCode string) string {
	normalizedCode := NormalizeTunnelRecycleCode(errorCode)
	if IsKnownTunnelRecycleCode(normalizedCode) {
		return normalizedCode
	}
	normalizedFallbackCode := NormalizeTunnelRecycleCode(fallbackCode)
	if IsKnownTunnelRecycleCode(normalizedFallbackCode) {
		return normalizedFallbackCode
	}
	// fallback 也非法时退回协议文档中的通用健康失败码，避免留下空错误码。
	return CodeTunnelRecycleTunnelUnhealthy
}
