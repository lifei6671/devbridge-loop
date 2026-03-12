package errors

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
	// CodeHybridFallbackForbidden 表示 hybrid fallback 被策略禁止。
	CodeHybridFallbackForbidden = "HYBRID_FALLBACK_FORBIDDEN"
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

// 数据面相关错误码定义。
const (
	// CodeTrafficInvalidOneof 表示数据面 oneof 字段设置冲突。
	CodeTrafficInvalidOneof = "TRAFFIC_INVALID_ONEOF"
	// CodeTrafficOpenRejected 表示 TrafficOpen 被拒绝。
	CodeTrafficOpenRejected = "TRAFFIC_OPEN_REJECTED"
	// CodeTrafficInvalidServiceID 表示 TrafficOpen 缺少 service_id。
	CodeTrafficInvalidServiceID = "TRAFFIC_INVALID_SERVICE_ID"
)
