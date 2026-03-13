package validate

import (
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// ValidateControlEnvelope 校验控制面统一封装的基础合法性。
func ValidateControlEnvelope(envelope pb.ControlEnvelope) error {
	// 控制面消息类型必须属于协议定义集合。
	if !pb.IsKnownControlMessageType(envelope.MessageType) {
		return ltfperrors.New(ltfperrors.CodeUnknownMessageType, fmt.Sprintf("unknown control message type: %s", envelope.MessageType))
	}
	// 主版本号用于不兼容升级判定，必须大于 0。
	if envelope.VersionMajor == 0 {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, "versionMajor must be greater than 0")
	}
	// 资源级消息强制校验幂等元信息，普通消息不做硬限制。
	if requiresResourceMeta(envelope.MessageType) {
		if err := ValidateResourceMeta(envelope.SessionID, envelope.SessionEpoch, envelope.EventID, envelope.ResourceVersion); err != nil {
			return err
		}
	}
	return nil
}

// ValidateResourceMeta 校验资源级消息的幂等字段。
func ValidateResourceMeta(sessionID string, sessionEpoch uint64, eventID string, resourceVersion uint64) error {
	// sessionID 是会话隔离主键，缺失会导致跨会话事件串扰。
	if strings.TrimSpace(sessionID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "sessionID is required")
	}
	// sessionEpoch 是代际语义核心字段，必须为正数。
	if sessionEpoch == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "sessionEpoch must be greater than 0")
	}
	// eventID 用于幂等去重，空值会破坏去重语义。
	if strings.TrimSpace(eventID) == "" {
		return ltfperrors.New(ltfperrors.CodeInvalidEventID, "eventID is required")
	}
	// resourceVersion 用于资源代际比较，必须为正数。
	if resourceVersion == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidResourceVersion, "resourceVersion must be greater than 0")
	}
	return nil
}

// ValidateConnectorHello 校验 ConnectorHello 消息字段。
func ValidateConnectorHello(message pb.ConnectorHello) error {
	// connectorId 是会话归属关键字段，缺失会导致状态串线。
	if strings.TrimSpace(message.ConnectorID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connectorId is required")
	}
	// namespace 是最小隔离边界字段之一，必须显式填写。
	if strings.TrimSpace(message.Namespace) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "namespace is required")
	}
	// environment 是最小隔离边界字段之一，必须显式填写。
	if strings.TrimSpace(message.Environment) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "environment is required")
	}
	return nil
}

// ValidatePublishService 校验 PublishService 消息字段。
func ValidatePublishService(message pb.PublishService) error {
	// serviceKey 是 lookup key，必须存在。
	if strings.TrimSpace(message.ServiceKey) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "serviceKey is required")
	}
	// namespace 和 environment 是 scope 约束核心字段。
	if strings.TrimSpace(message.Namespace) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "namespace is required")
	}
	if strings.TrimSpace(message.Environment) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "environment is required")
	}
	// serviceName 为空会导致无法建立 canonical identity 映射。
	if strings.TrimSpace(message.ServiceName) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "serviceName is required")
	}
	// endpoints 至少需要一个，避免发布不可路由服务。
	if len(message.Endpoints) == 0 {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "at least one endpoint is required")
	}

	for index, endpoint := range message.Endpoints {
		// endpoint 协议不能为空，否则无法匹配上游拨号逻辑。
		if strings.TrimSpace(endpoint.Protocol) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, fmt.Sprintf("endpoint[%d].protocol is required", index))
		}
		// endpoint host 不能为空，否则无法建立连接。
		if strings.TrimSpace(endpoint.Host) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, fmt.Sprintf("endpoint[%d].host is required", index))
		}
		// endpoint port 必须为正数。
		if endpoint.Port == 0 {
			return ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("endpoint[%d].port must be greater than 0", index))
		}
	}
	return nil
}

// ValidateTrafficOpen 校验 TrafficOpen 消息字段。
func ValidateTrafficOpen(message pb.TrafficOpen) error {
	// trafficID 是流量生命周期主键，不能为空。
	if strings.TrimSpace(message.TrafficID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "trafficId is required")
	}
	// serviceID 是数据面路由定位的关键字段，不能为空。
	if strings.TrimSpace(message.ServiceID) == "" {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidServiceID, "serviceId is required")
	}
	// endpointSelectionHint 允许为空，存在时仅做非权威提示。
	return nil
}

// ValidateStreamPayload 校验数据面 oneof 结构合法性。
func ValidateStreamPayload(payload pb.StreamPayload) error {
	// 数据面 oneof 必须严格只有一个字段生效。
	if payload.ActivePayloadCount() != 1 {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidOneof, "stream payload must contain exactly one active field")
	}
	// OpenReq 场景下继续复用 TrafficOpen 规则。
	if payload.OpenReq != nil {
		return ValidateTrafficOpen(*payload.OpenReq)
	}
	return nil
}

// ValidateRouteScope 校验 route 与 target 的 scope 一致性。
func ValidateRouteScope(routeNamespace, routeEnvironment, targetNamespace, targetEnvironment string) error {
	// 首版协议不允许跨 scope 引用，因此 namespace 必须一致。
	if strings.TrimSpace(routeNamespace) != strings.TrimSpace(targetNamespace) {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route namespace must equal target namespace")
	}
	// 首版协议不允许跨 environment fallback，因此 environment 必须一致。
	if strings.TrimSpace(routeEnvironment) != strings.TrimSpace(targetEnvironment) {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route environment must equal target environment")
	}
	return nil
}

// ValidateControlError 校验控制面错误消息语义。
func ValidateControlError(message pb.ControlError) error {
	// code 为空会导致调用方无法按错误类型分支处理。
	if strings.TrimSpace(message.Code) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "control error code is required")
	}
	// message 为空会导致错误排障信息丢失。
	if strings.TrimSpace(message.Message) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "control error message is required")
	}
	return nil
}

// requiresResourceMeta 判断消息类型是否需要强制幂等元信息。
func requiresResourceMeta(messageType pb.ControlMessageType) bool {
	// 资源级变更消息必须携带 sessionEpoch/eventID/resourceVersion。
	switch messageType {
	case pb.ControlMessagePublishService, pb.ControlMessageUnpublishService, pb.ControlMessageRouteAssign, pb.ControlMessageRouteRevoke:
		return true
	default:
		return false
	}
}
