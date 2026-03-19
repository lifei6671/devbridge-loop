package control

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/admission"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// RouteHandlerOptions 定义 RouteHandler 构造参数。
type RouteHandlerOptions struct {
	Guard           *consistency.ResourceEventGuard
	SessionRegistry *registry.SessionRegistry
	ServiceRegistry *registry.ServiceRegistry
	RouteRegistry   *registry.RouteRegistry
	Metrics         *obs.Metrics
	Admission       RouteAdmission
	HostDeriver     HostDeriver
	Now             func() time.Time
}

// RouteAdmission 定义路由注册前的 admission 校验接口。
type RouteAdmission interface {
	Admit(route pb.Route) ([]string, map[string]string, error)
}

// RouteHandler 处理 RouteAssign/RouteRevoke 资源事件。
type RouteHandler struct {
	guard           *consistency.ResourceEventGuard
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	routeRegistry   *registry.RouteRegistry
	metrics         *obs.Metrics
	admission       RouteAdmission
	hostDeriver     HostDeriver
	now             func() time.Time
}

// NewRouteHandler 创建路由处理器。
func NewRouteHandler(options RouteHandlerOptions) *RouteHandler {
	nowFunc := options.Now
	if nowFunc == nil {
		// 默认使用 UTC 时间。
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	guard := options.Guard
	if guard == nil {
		// 默认重放窗口容量设为 4096。
		guard = consistency.NewResourceEventGuard(4096)
	}
	routeRegistry := options.RouteRegistry
	if routeRegistry == nil {
		// 未注入时创建默认路由注册表。
		routeRegistry = registry.NewRouteRegistry()
	}
	metrics := options.Metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	routeAdmission := options.Admission
	if routeAdmission == nil {
		routeAdmission = admission.NewRouteConflictAdmission(routeRegistry, options.ServiceRegistry)
	}
	return &RouteHandler{
		guard:           guard,
		sessionRegistry: options.SessionRegistry,
		serviceRegistry: options.ServiceRegistry,
		routeRegistry:   routeRegistry,
		metrics:         metrics,
		admission:       routeAdmission,
		hostDeriver:     options.HostDeriver,
		now:             nowFunc,
	}
}

// HandleAssign 处理 RouteAssign 消息并返回 ACK。
func (handler *RouteHandler) HandleAssign(envelope pb.ControlEnvelope, message pb.RouteAssign) pb.RouteAssignAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		// 封装元信息不合法时直接拒绝。
		return handler.rejectAssignAck(message, err)
	}
	if strings.TrimSpace(message.RouteID) == "" {
		// routeId 缺失时无法建立版本索引。
		err := ltfperrors.New(ltfperrors.CodeMissingRequiredField, "routeId is required")
		return handler.rejectAssignAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		// 旧 epoch 事件禁止进入运行态。
		return handler.rejectAssignAck(message, err)
	}
	normalizedMessage, err := handler.normalizeAssignMessage(message)
	if err != nil {
		return handler.rejectAssignAck(message, err)
	}

	resourceID := resolveRouteResourceID(envelope.ResourceID, normalizedMessage.RouteID)
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "route",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	var warnings []string
	var ackMetadata map[string]string
	if decision.Status == pb.EventStatusAccepted {
		// accepted 才写入注册表。
		snapshot := pb.Route{
			RouteID:         normalizedMessage.RouteID,
			Scope:           normalizedMessage.Scope,
			ResourceVersion: envelope.ResourceVersion,
			Match:           normalizedMessage.Match,
			Target:          normalizedMessage.Target,
			PolicyJSON:      normalizedMessage.PolicyJSON,
			Priority:        normalizedMessage.Priority,
			Metadata:        normalizedMessage.Metadata,
		}
		admissionWarnings, admissionMetadata, admissionErr := handler.runAdmission(snapshot)
		if admissionErr != nil {
			return handler.rejectAssignAckWithMetadata(normalizedMessage, admissionErr, admissionMetadata)
		}
		warnings = admissionWarnings
		ackMetadata = admissionMetadata
		handler.routeRegistry.Upsert(handler.now(), snapshot)
	}

	assignAck := consistency.BuildRouteAssignAck(
		decision.Status,
		normalizedMessage.RouteID,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
	assignAck.Warnings = append(assignAck.Warnings, warnings...)
	assignAck.Metadata = cloneStringMap(ackMetadata)
	return assignAck
}

// HandleRevoke 处理 RouteRevoke 消息并返回 ACK。
func (handler *RouteHandler) HandleRevoke(envelope pb.ControlEnvelope, message pb.RouteRevoke) pb.RouteRevokeAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		// 封装元信息不合法时直接拒绝。
		return handler.rejectRevokeAck(message, err)
	}
	if strings.TrimSpace(message.RouteID) == "" {
		// routeId 缺失时无法建立版本索引。
		err := ltfperrors.New(ltfperrors.CodeMissingRequiredField, "routeId is required")
		return handler.rejectRevokeAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		// 旧 epoch 事件禁止进入运行态。
		return handler.rejectRevokeAck(message, err)
	}

	resourceID := resolveRouteResourceID(envelope.ResourceID, message.RouteID)
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "route",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	if decision.Status == pb.EventStatusAccepted {
		// accepted 时执行撤销。
		handler.routeRegistry.Remove(message.RouteID)
	}

	return consistency.BuildRouteRevokeAck(
		decision.Status,
		message.RouteID,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
}

// rejectAssignAck 构造 route assign 拒绝 ACK。
func (handler *RouteHandler) rejectAssignAck(message pb.RouteAssign, err error) pb.RouteAssignAck {
	return handler.rejectAssignAckWithMetadata(message, err, nil)
}

// rejectAssignAckWithMetadata 构造带 metadata 的 route assign 拒绝 ACK。
func (handler *RouteHandler) rejectAssignAckWithMetadata(message pb.RouteAssign, err error, metadata map[string]string) pb.RouteAssignAck {
	currentVersion := handler.routeRegistry.CurrentVersion(message.RouteID)
	assignAck := consistency.BuildRouteAssignAck(
		pb.EventStatusRejected,
		message.RouteID,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
	assignAck.Metadata = cloneStringMap(metadata)
	return assignAck
}

// rejectRevokeAck 构造 route revoke 拒绝 ACK。
func (handler *RouteHandler) rejectRevokeAck(message pb.RouteRevoke, err error) pb.RouteRevokeAck {
	currentVersion := handler.routeRegistry.CurrentVersion(message.RouteID)
	return consistency.BuildRouteRevokeAck(
		pb.EventStatusRejected,
		message.RouteID,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// validateSessionEpoch 校验路由事件是否来自当前有效 session。
func (handler *RouteHandler) validateSessionEpoch(envelope pb.ControlEnvelope) error {
	if handler.sessionRegistry == nil {
		// 未注入 session 视图时跳过校验。
		return nil
	}
	sessionRuntime, exists := handler.sessionRegistry.GetBySession(envelope.SessionID)
	if !exists {
		// 会话不存在视为旧事件。
		return ltfperrors.New(ltfperrors.CodeStaleEpochEvent, "session not found for route event")
	}
	if sessionRuntime.Epoch != envelope.SessionEpoch {
		// 代际不一致视为旧事件。
		return ltfperrors.New(ltfperrors.CodeStaleEpochEvent, "session epoch mismatch for route event")
	}
	if sessionRuntime.State != registry.SessionActive {
		// 只有 ACTIVE 会话允许写入路由，旧会话必须冻结控制面写入。
		return ltfperrors.New(ltfperrors.CodeInvalidStateTransition, "session state does not allow route mutation")
	}
	return nil
}

// resolveRouteResourceID 解析路由资源主键。
func resolveRouteResourceID(resourceID string, routeID string) string {
	normalizedResourceID := strings.TrimSpace(resourceID)
	if normalizedResourceID != "" {
		// envelope.resourceId 优先级最高。
		return normalizedResourceID
	}
	return strings.TrimSpace(routeID)
}

func (handler *RouteHandler) normalizeAssignMessage(message pb.RouteAssign) (pb.RouteAssign, error) {
	normalizedMessage := message
	normalizedMessage.RouteID = strings.TrimSpace(message.RouteID)
	normalizedMessage.Scope = normalizeScope(message.Scope)
	normalizedMessage.Target.Type = pb.RouteTargetType(strings.TrimSpace(string(message.Target.Type)))
	normalizedMessage.Match.Host = strings.ToLower(strings.TrimSpace(message.Match.Host))
	normalizedMessage.Match.Authority = strings.ToLower(strings.TrimSpace(message.Match.Authority))
	if err := validateRouteMatchRegex(normalizedMessage.Match); err != nil {
		return pb.RouteAssign{}, err
	}
	if err := validateRouteTarget(normalizedMessage.Target); err != nil {
		return pb.RouteAssign{}, err
	}
	if !shouldDeriveRouteHost(normalizedMessage) {
		return normalizedMessage, nil
	}
	if handler == nil || handler.hostDeriver == nil {
		return normalizedMessage, nil
	}
	serviceName, scope, ok := handler.resolveRouteServiceIdentity(normalizedMessage)
	if !ok {
		return normalizedMessage, nil
	}
	derivedHost, err := handler.hostDeriver.Derive(serviceName, scope)
	if err != nil {
		return pb.RouteAssign{}, err
	}
	normalizedMessage.Match.Host = derivedHost
	return normalizedMessage, nil
}

func (handler *RouteHandler) runAdmission(route pb.Route) ([]string, map[string]string, error) {
	if handler == nil || handler.admission == nil {
		return nil, nil, nil
	}
	warnings, metadata, err := handler.admission.Admit(route)
	if err != nil {
		if handler.metrics != nil {
			handler.metrics.IncBridgeRouteConflictRejectionTotal()
		}
		conflictRouteID := admission.ExtractConflictRouteID(err)
		if metadata == nil && conflictRouteID != "" {
			metadata = map[string]string{"conflict_route_id": conflictRouteID}
		}
		if !ltfperrors.IsCode(err, ltfperrors.CodeIngressRouteMismatch) {
			err = ltfperrors.New(ltfperrors.CodeIngressRouteMismatch, err.Error())
		}
		return nil, metadata, err
	}
	return warnings, metadata, nil
}

func validateRouteTarget(target pb.RouteTarget) error {
	switch target.Type {
	case pb.RouteTargetTypeConnectorService:
		if target.ConnectorService == nil {
			return ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				"route.target.connectorService is required for connector_service target",
			)
		}
		return nil
	case pb.RouteTargetTypeExternalService:
		if target.ExternalService == nil {
			return ltfperrors.New(
				ltfperrors.CodeMissingRequiredField,
				"route.target.externalService is required for external_service target",
			)
		}
		return nil
	default:
		return ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			"route.target.type must be connector_service or external_service",
		)
	}
}

func validateRouteMatchRegex(match pb.RouteMatch) error {
	for matcherIndex, matcher := range match.Headers {
		normalizedPattern := strings.TrimSpace(matcher.Regex)
		if normalizedPattern == "" {
			continue
		}
		if _, err := regexp.Compile(normalizedPattern); err != nil {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				fmt.Sprintf("route.match.headers[%d].regex is invalid: %v", matcherIndex, err),
			)
		}
	}
	for matcherIndex, matcher := range match.Queries {
		normalizedPattern := strings.TrimSpace(matcher.Regex)
		if normalizedPattern == "" {
			continue
		}
		if _, err := regexp.Compile(normalizedPattern); err != nil {
			return ltfperrors.New(
				ltfperrors.CodeUnsupportedValue,
				fmt.Sprintf("route.match.queries[%d].regex is invalid: %v", matcherIndex, err),
			)
		}
	}
	return nil
}

func shouldDeriveRouteHost(message pb.RouteAssign) bool {
	if strings.TrimSpace(message.Match.Host) != "" || strings.TrimSpace(message.Match.Authority) != "" {
		return false
	}
	if message.Target.Type != pb.RouteTargetTypeConnectorService || message.Target.ConnectorService == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(message.Match.Protocol)) {
	case "http", "grpc":
		return true
	default:
		return false
	}
}

func (handler *RouteHandler) resolveRouteServiceIdentity(message pb.RouteAssign) (string, pb.Scope, bool) {
	selector := message.Target.ConnectorService.Selector
	serviceName := strings.TrimSpace(selector.ServiceName)
	scope := normalizeScope(selector.Scope)
	if scope.Namespace == "" {
		scope.Namespace = strings.TrimSpace(message.Scope.Namespace)
	}
	if scope.Environment == "" {
		scope.Environment = strings.TrimSpace(message.Scope.Environment)
	}
	if serviceName != "" && scope.Namespace != "" && scope.Environment != "" {
		return serviceName, scope, true
	}
	if strings.TrimSpace(selector.LogicalServiceID) == "" || handler == nil || handler.serviceRegistry == nil {
		return "", pb.Scope{}, false
	}
	logicalService, exists := handler.serviceRegistry.GetLogicalServiceByID(selector.LogicalServiceID)
	if !exists {
		return "", pb.Scope{}, false
	}
	return strings.TrimSpace(logicalService.ServiceName), normalizeScope(logicalService.Scope), true
}
