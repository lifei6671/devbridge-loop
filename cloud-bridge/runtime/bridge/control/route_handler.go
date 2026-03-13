package control

import (
	"strings"
	"time"

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
	RouteRegistry   *registry.RouteRegistry
	Now             func() time.Time
}

// RouteHandler 处理 RouteAssign/RouteRevoke 资源事件。
type RouteHandler struct {
	guard           *consistency.ResourceEventGuard
	sessionRegistry *registry.SessionRegistry
	routeRegistry   *registry.RouteRegistry
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
	return &RouteHandler{
		guard:           guard,
		sessionRegistry: options.SessionRegistry,
		routeRegistry:   routeRegistry,
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
		// accepted 才写入注册表。
		snapshot := pb.Route{
			RouteID:         message.RouteID,
			Namespace:       message.Namespace,
			Environment:     message.Environment,
			ResourceVersion: envelope.ResourceVersion,
			Match:           message.Match,
			Target:          message.Target,
			PolicyJSON:      message.PolicyJSON,
			Priority:        message.Priority,
			Metadata:        message.Metadata,
		}
		handler.routeRegistry.Upsert(handler.now(), snapshot)
	}

	return consistency.BuildRouteAssignAck(
		decision.Status,
		message.RouteID,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
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
	currentVersion := handler.routeRegistry.CurrentVersion(message.RouteID)
	return consistency.BuildRouteAssignAck(
		pb.EventStatusRejected,
		message.RouteID,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
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
