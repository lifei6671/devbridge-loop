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
