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

// PublishHandlerOptions 定义 PublishHandler 构造参数。
type PublishHandlerOptions struct {
	Guard           *consistency.ResourceEventGuard
	SessionRegistry *registry.SessionRegistry
	ServiceRegistry *registry.ServiceRegistry
	Now             func() time.Time
}

// PublishHandler 处理服务发布与下线事件。
type PublishHandler struct {
	guard           *consistency.ResourceEventGuard
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	now             func() time.Time
}

// NewPublishHandler 创建发布处理器。
func NewPublishHandler(options PublishHandlerOptions) *PublishHandler {
	nowFunc := options.Now
	if nowFunc == nil {
		// 未注入时使用 UTC 当前时间，避免时区歧义。
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	guard := options.Guard
	if guard == nil {
		// 默认创建一个容量为 4096 的重放窗口。
		guard = consistency.NewResourceEventGuard(4096)
	}
	serviceRegistry := options.ServiceRegistry
	if serviceRegistry == nil {
		// 默认创建内存服务注册表。
		serviceRegistry = registry.NewServiceRegistry()
	}
	return &PublishHandler{
		guard:           guard,
		sessionRegistry: options.SessionRegistry,
		serviceRegistry: serviceRegistry,
		now:             nowFunc,
	}
}

// HandlePublish 处理 PublishService 控制消息。
func (handler *PublishHandler) HandlePublish(envelope pb.ControlEnvelope, message pb.PublishService) pb.PublishServiceAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		// 封装元信息不合法时直接拒绝。
		return handler.rejectPublishAck(message, err)
	}
	if err := validate.ValidatePublishService(message); err != nil {
		// 业务字段不合法时拒绝并回传错误码。
		return handler.rejectPublishAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		// 旧 epoch 事件必须拒绝，避免污染新 session。
		return handler.rejectPublishAck(message, err)
	}

	resourceID := resolveServiceResourceID(envelope.ResourceID, message.ServiceID, message.ServiceKey)
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "service",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	if decision.Status == pb.EventStatusAccepted {
		// accepted 才会写入注册表，duplicate/rejected 都无副作用。
		snapshot := buildServiceSnapshot(
			message,
			envelope.ResourceVersion,
			handler.resolveConnectorID(envelope),
		)
		handler.serviceRegistry.Upsert(handler.now(), snapshot)
	}

	return consistency.BuildPublishServiceAck(
		decision.Status,
		resolveServiceID(message.ServiceID, message.ServiceKey),
		message.ServiceKey,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
}

// HandleUnpublish 处理 UnpublishService 控制消息。
func (handler *PublishHandler) HandleUnpublish(envelope pb.ControlEnvelope, message pb.UnpublishService) pb.UnpublishServiceAck {
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		// 封装元信息不合法时直接拒绝。
		return handler.rejectUnpublishAck(message, err)
	}
	if strings.TrimSpace(message.ServiceID) == "" && strings.TrimSpace(message.ServiceKey) == "" {
		// 下线消息必须至少指定 serviceId 或 serviceKey。
		err := ltfperrors.New(ltfperrors.CodeMissingRequiredField, "serviceID or serviceKey is required")
		return handler.rejectUnpublishAck(message, err)
	}
	if err := handler.validateSessionEpoch(envelope); err != nil {
		// 旧 epoch 事件必须拒绝，避免污染新 session。
		return handler.rejectUnpublishAck(message, err)
	}

	resourceID := resolveServiceResourceID(envelope.ResourceID, message.ServiceID, message.ServiceKey)
	decision := handler.guard.Evaluate(consistency.ResourceEventMeta{
		SessionID:       envelope.SessionID,
		SessionEpoch:    envelope.SessionEpoch,
		EventID:         envelope.EventID,
		ResourceType:    "service",
		ResourceID:      resourceID,
		ResourceVersion: envelope.ResourceVersion,
	})
	if decision.Status == pb.EventStatusAccepted {
		// accepted 时执行实际下线动作。
		if strings.TrimSpace(message.ServiceID) != "" {
			handler.serviceRegistry.RemoveByServiceID(message.ServiceID)
		} else {
			handler.serviceRegistry.RemoveByServiceKey(message.ServiceKey)
		}
	}

	return consistency.BuildUnpublishServiceAck(
		decision.Status,
		resolveServiceID(message.ServiceID, message.ServiceKey),
		message.ServiceKey,
		decision.AcceptedResourceVersion,
		decision.CurrentResourceVersion,
		decision.ErrorCode,
		decision.ErrorMessage,
	)
}

// ReconcileFromFullSync 使用 full-sync 快照重建服务视图。
func (handler *PublishHandler) ReconcileFromFullSync(snapshot pb.FullSyncSnapshot) {
	if !snapshot.Completed {
		// 分片快照未完成时不触发覆盖，避免中间态污染。
		return
	}
	handler.serviceRegistry.ReplaceAll(handler.now(), snapshot.Services)
	versionSnapshot := make(map[string]uint64, len(snapshot.Services))
	for _, service := range snapshot.Services {
		resourceID := resolveServiceResourceID("", service.ServiceID, service.ServiceKey)
		if resourceID == "" {
			// 兜底保护：跳过无法索引的服务条目。
			continue
		}
		resourceKey := "service:" + resourceID
		versionSnapshot[resourceKey] = service.ResourceVersion
	}
	// full-sync 后用权威版本刷新本地视图。
	handler.guard.ReplaceAllVersions(versionSnapshot)
}

// rejectPublishAck 构造发布拒绝 ACK。
func (handler *PublishHandler) rejectPublishAck(message pb.PublishService, err error) pb.PublishServiceAck {
	currentVersion := handler.serviceRegistry.CurrentVersion(message.ServiceID, message.ServiceKey)
	return consistency.BuildPublishServiceAck(
		pb.EventStatusRejected,
		resolveServiceID(message.ServiceID, message.ServiceKey),
		message.ServiceKey,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// rejectUnpublishAck 构造下线拒绝 ACK。
func (handler *PublishHandler) rejectUnpublishAck(message pb.UnpublishService, err error) pb.UnpublishServiceAck {
	currentVersion := handler.serviceRegistry.CurrentVersion(message.ServiceID, message.ServiceKey)
	return consistency.BuildUnpublishServiceAck(
		pb.EventStatusRejected,
		resolveServiceID(message.ServiceID, message.ServiceKey),
		message.ServiceKey,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// buildServiceSnapshot 将发布消息转为注册表快照。
func buildServiceSnapshot(message pb.PublishService, resourceVersion uint64, connectorID string) pb.Service {
	serviceID := resolveServiceID(message.ServiceID, message.ServiceKey)
	return pb.Service{
		ServiceID:       serviceID,
		ServiceKey:      message.ServiceKey,
		Namespace:       message.Namespace,
		Environment:     message.Environment,
		ConnectorID:     strings.TrimSpace(connectorID),
		ServiceName:     message.ServiceName,
		ServiceType:     message.ServiceType,
		Status:          pb.ServiceStatusActive,
		ResourceVersion: resourceVersion,
		Endpoints:       message.Endpoints,
		Exposure:        message.Exposure,
		HealthCheck:     message.HealthCheck,
		HealthStatus:    pb.HealthStatusUnknown,
		DiscoveryPolicy: message.DiscoveryPolicy,
		Labels:          message.Labels,
		Metadata:        message.Metadata,
	}
}

// resolveConnectorID 提取发布事件归属的 connector_id。
func (handler *PublishHandler) resolveConnectorID(envelope pb.ControlEnvelope) string {
	normalizedConnectorID := strings.TrimSpace(envelope.ConnectorID)
	if normalizedConnectorID != "" {
		// payload 已携带 connector_id 时直接采用。
		return normalizedConnectorID
	}
	if handler.sessionRegistry == nil {
		return ""
	}
	sessionSnapshot, exists := handler.sessionRegistry.GetBySession(envelope.SessionID)
	if !exists {
		return ""
	}
	// 回落到 session 视图，兼容旧端未显式透传 connector_id 的场景。
	return strings.TrimSpace(sessionSnapshot.ConnectorID)
}

// resolveServiceID 解析服务主键。
func resolveServiceID(serviceID string, serviceKey string) string {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID != "" {
		// 优先使用明确的 serviceId。
		return normalizedServiceID
	}
	// serviceId 缺失时回退为 serviceKey。
	return strings.TrimSpace(serviceKey)
}

// resolveServiceResourceID 解析资源版本比较所用的资源键。
func resolveServiceResourceID(resourceID string, serviceID string, serviceKey string) string {
	normalizedResourceID := strings.TrimSpace(resourceID)
	if normalizedResourceID != "" {
		// envelope.resourceId 优先级最高。
		return normalizedResourceID
	}
	return resolveServiceID(serviceID, serviceKey)
}

// extractErrorCode 提取协议错误码，不存在时回退到 INVALID_PAYLOAD。
func extractErrorCode(err error) string {
	code := ltfperrors.ExtractCode(err)
	if code != "" {
		return code
	}
	// 非协议错误统一映射为 payload 非法。
	return ltfperrors.CodeInvalidPayload
}

// validateSessionEpoch 校验资源事件是否来自当前有效 session 代际。
func (handler *PublishHandler) validateSessionEpoch(envelope pb.ControlEnvelope) error {
	if handler.sessionRegistry == nil {
		// 未注入 session 视图时跳过校验，保留向后兼容。
		return nil
	}
	sessionRuntime, exists := handler.sessionRegistry.GetBySession(envelope.SessionID)
	if !exists {
		// 未注册会话视为旧事件或脏事件。
		return ltfperrors.New(ltfperrors.CodeStaleEpochEvent, "session not found for resource event")
	}
	if sessionRuntime.Epoch != envelope.SessionEpoch {
		// epoch 不一致时必须拒绝，避免旧会话污染。
		return ltfperrors.New(ltfperrors.CodeStaleEpochEvent, "session epoch mismatch for resource event")
	}
	return nil
}
