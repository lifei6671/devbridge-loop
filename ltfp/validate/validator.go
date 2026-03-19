package validate

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	standardScopeNamespaceHeader   = "x-bridge-namespace"
	standardScopeEnvironmentHeader = "x-bridge-environment"
)

// IsReservedScopeHeader 判断 header 名称是否属于 Bridge 入口保留 scope header。
func IsReservedScopeHeader(headerName string) bool {
	switch strings.ToLower(strings.TrimSpace(headerName)) {
	case standardScopeNamespaceHeader, standardScopeEnvironmentHeader:
		return true
	default:
		return false
	}
}

// ValidateControlEnvelope 校验控制面统一封装的基础合法性。
func ValidateControlEnvelope(envelope pb.ControlEnvelope) error {
	if !pb.IsKnownControlMessageType(envelope.MessageType) {
		return ltfperrors.New(ltfperrors.CodeUnknownMessageType, fmt.Sprintf("unknown control message type: %s", envelope.MessageType))
	}
	if envelope.VersionMajor == 0 {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, "versionMajor must be greater than 0")
	}
	if requiresResourceMeta(envelope.MessageType) {
		if err := ValidateResourceMeta(envelope.SessionID, envelope.SessionEpoch, envelope.EventID, envelope.ResourceVersion); err != nil {
			return err
		}
	}
	return nil
}

// ValidateResourceMeta 校验资源级消息的幂等字段。
func ValidateResourceMeta(sessionID string, sessionEpoch uint64, eventID string, resourceVersion uint64) error {
	if strings.TrimSpace(sessionID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "sessionID is required")
	}
	if sessionEpoch == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "sessionEpoch must be greater than 0")
	}
	if strings.TrimSpace(eventID) == "" {
		return ltfperrors.New(ltfperrors.CodeInvalidEventID, "eventID is required")
	}
	if resourceVersion == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidResourceVersion, "resourceVersion must be greater than 0")
	}
	return nil
}

// ValidateConnectorHello 校验 ConnectorHello 消息字段。
func ValidateConnectorHello(message pb.ConnectorHello) error {
	if strings.TrimSpace(message.ConnectorID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connectorId is required")
	}
	return nil
}

// ValidateNoLegacyFields 校验原始负载中不包含已废弃字段。
func ValidateNoLegacyFields(payload json.RawMessage, fieldNames ...string) error {
	if len(payload) == 0 || len(fieldNames) == 0 {
		return nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	for _, fieldName := range fieldNames {
		if _, exists := body[fieldName]; exists {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedLegacyProtocol,
				fmt.Sprintf("legacy field %q is not supported", fieldName),
			)
		}
	}
	return nil
}

// ValidatePublishService 校验 PublishService 消息字段。
func ValidatePublishService(message pb.PublishService) error {
	normalizedServiceName := strings.TrimSpace(message.ServiceName)
	if normalizedServiceName == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "serviceName is required")
	}
	if strings.Contains(normalizedServiceName, "/") {
		return ltfperrors.New(ltfperrors.CodeUnsupportedValue, "serviceName must not contain '/'")
	}
	if strings.TrimSpace(message.Scope.Namespace) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "scope.namespace is required")
	}
	if strings.TrimSpace(message.Scope.Environment) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "scope.environment is required")
	}
	if len(message.Endpoints) == 0 {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "at least one endpoint is required")
	}

	normalizedServiceProtocol := ""
	for index, endpoint := range message.Endpoints {
		normalizedEndpointProtocol := strings.ToLower(strings.TrimSpace(endpoint.Protocol))
		if normalizedEndpointProtocol == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, fmt.Sprintf("endpoint[%d].protocol is required", index))
		}
		if normalizedServiceProtocol == "" {
			normalizedServiceProtocol = normalizedEndpointProtocol
		} else if normalizedServiceProtocol != normalizedEndpointProtocol {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				"all endpoints in one publish service must use the same protocol",
			)
		}
		if strings.TrimSpace(endpoint.Host) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, fmt.Sprintf("endpoint[%d].host is required", index))
		}
		if endpoint.Port == 0 {
			return ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("endpoint[%d].port must be greater than 0", index))
		}
	}
	if err := ValidateRouteHint(message.RouteHint); err != nil {
		return err
	}
	return nil
}

// ValidateRouteHint 校验 RouteHint 的 matcher 结构合法性。
func ValidateRouteHint(routeHint pb.RouteHint) error {
	for matcherIndex, matcher := range routeHint.MatchHeaders {
		if strings.TrimSpace(matcher.Name) == "" {
			return ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				fmt.Sprintf("routeHint.matchHeaders[%d].name is required", matcherIndex),
			)
		}
		if IsReservedScopeHeader(matcher.Name) {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				fmt.Sprintf("routeHint.matchHeaders[%d].name uses reserved scope header", matcherIndex),
			)
		}
		normalizedPattern := strings.TrimSpace(matcher.Regex)
		if normalizedPattern == "" {
			continue
		}
		if _, err := regexp.Compile(normalizedPattern); err != nil {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				fmt.Sprintf("routeHint.matchHeaders[%d].regex is invalid: %v", matcherIndex, err),
			)
		}
	}
	for matcherIndex, matcher := range routeHint.MatchQueries {
		if strings.TrimSpace(matcher.Name) == "" {
			return ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				fmt.Sprintf("routeHint.matchQueries[%d].name is required", matcherIndex),
			)
		}
		normalizedPattern := strings.TrimSpace(matcher.Regex)
		if normalizedPattern == "" {
			continue
		}
		if _, err := regexp.Compile(normalizedPattern); err != nil {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				fmt.Sprintf("routeHint.matchQueries[%d].regex is invalid: %v", matcherIndex, err),
			)
		}
	}
	return nil
}

// ValidateUnpublishService 校验 UnpublishService 消息字段。
func ValidateUnpublishService(message pb.UnpublishService) error {
	if strings.TrimSpace(message.InstanceID) == "" && strings.TrimSpace(message.LogicalServiceID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "instanceId or logicalServiceId is required")
	}
	if strings.TrimSpace(message.InstanceID) == "" {
		if strings.TrimSpace(message.ServiceName) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "serviceName is required when instanceId is empty")
		}
		if strings.TrimSpace(message.Scope.Namespace) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "scope.namespace is required when instanceId is empty")
		}
		if strings.TrimSpace(message.Scope.Environment) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "scope.environment is required when instanceId is empty")
		}
	}
	return nil
}

// ValidateServiceHealthReport 校验服务健康上报字段。
func ValidateServiceHealthReport(message pb.ServiceHealthReport) error {
	if strings.TrimSpace(message.InstanceID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "instanceId is required")
	}
	return nil
}

// ValidateTrafficOpen 校验 TrafficOpen 消息字段。
func ValidateTrafficOpen(message pb.TrafficOpen) error {
	if strings.TrimSpace(message.TrafficID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "trafficId is required")
	}
	if strings.TrimSpace(message.LogicalServiceID) == "" {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidLogicalServiceID, "logicalServiceId is required")
	}
	if strings.TrimSpace(message.InstanceID) == "" {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidInstanceID, "instanceId is required")
	}
	return nil
}

// ValidateStreamPayload 校验数据面 oneof 结构合法性。
func ValidateStreamPayload(payload pb.StreamPayload) error {
	if payload.ActivePayloadCount() != 1 {
		return ltfperrors.New(ltfperrors.CodeTrafficInvalidOneof, "stream payload must contain exactly one active field")
	}
	if payload.OpenReq != nil {
		return ValidateTrafficOpen(*payload.OpenReq)
	}
	return nil
}

// ValidateRouteScope 校验 route 与 target 的 scope 一致性。
func ValidateRouteScope(routeScope pb.Scope, targetScope pb.Scope) error {
	normalizedRouteNamespace := strings.TrimSpace(routeScope.Namespace)
	normalizedTargetNamespace := strings.TrimSpace(targetScope.Namespace)
	if normalizedRouteNamespace != "" && normalizedTargetNamespace != "" && normalizedRouteNamespace != normalizedTargetNamespace {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route namespace must equal target namespace")
	}
	normalizedRouteEnvironment := strings.TrimSpace(routeScope.Environment)
	normalizedTargetEnvironment := strings.TrimSpace(targetScope.Environment)
	if normalizedRouteEnvironment != "" && normalizedTargetEnvironment != "" && normalizedRouteEnvironment != normalizedTargetEnvironment {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route environment must equal target environment")
	}
	return nil
}

// ValidateControlError 校验控制面错误消息语义。
func ValidateControlError(message pb.ControlError) error {
	if strings.TrimSpace(message.Code) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "control error code is required")
	}
	if strings.TrimSpace(message.Message) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "control error message is required")
	}
	return nil
}

// requiresResourceMeta 判断消息类型是否需要强制幂等元信息。
func requiresResourceMeta(messageType pb.ControlMessageType) bool {
	switch messageType {
	case pb.ControlMessagePublishService, pb.ControlMessageUnpublishService, pb.ControlMessageRouteAssign, pb.ControlMessageRouteRevoke:
		return true
	default:
		return false
	}
}
