package control

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// PublishHandlerOptions 定义 PublishHandler 构造参数。
type PublishHandlerOptions struct {
	Guard              *consistency.ResourceEventGuard
	SessionRegistry    *registry.SessionRegistry
	ServiceRegistry    *registry.ServiceRegistry
	Metrics            *obs.Metrics
	Now                func() time.Time
	ServiceIDGenerator func(now time.Time, serviceKey string) string
}

// PublishHandler 处理服务发布与下线事件。
type PublishHandler struct {
	guard              *consistency.ResourceEventGuard
	sessionRegistry    *registry.SessionRegistry
	serviceRegistry    *registry.ServiceRegistry
	metrics            *obs.Metrics
	now                func() time.Time
	serviceIDGenerator func(now time.Time, serviceKey string) string
}

var publishServiceIDSequence uint64

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
	serviceIDGenerator := options.ServiceIDGenerator
	if serviceIDGenerator == nil {
		serviceIDGenerator = defaultPublishServiceIDGenerator
	}
	metrics := options.Metrics
	if metrics == nil {
		// 指标未注入时复用默认容器，保持观测链路不丢数。
		metrics = obs.DefaultMetrics
	}
	return &PublishHandler{
		guard:              guard,
		sessionRegistry:    options.SessionRegistry,
		serviceRegistry:    serviceRegistry,
		metrics:            metrics,
		now:                nowFunc,
		serviceIDGenerator: serviceIDGenerator,
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
	normalizedMessage := handler.normalizePublishMessageIdentity(message)
	resolvedConnectorID := handler.resolveConnectorID(envelope)
	resolvedServiceID := handler.resolveServiceID(normalizedMessage.ServiceID, normalizedMessage.ServiceKey)
	resolvedServiceKey := strings.TrimSpace(normalizedMessage.ServiceKey)

	resourceID := handler.resolveServiceInstanceResourceID(
		normalizedMessage.ServiceID,
		normalizedMessage.ServiceKey,
		resolvedConnectorID,
		envelope.SessionID,
	)
	publishedServiceInstanceID := ""
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
			normalizedMessage,
			envelope.ResourceVersion,
			resolvedConnectorID,
		)
		publishedServiceInstanceID = handler.serviceRegistry.UpsertWithRuntime(handler.now(), snapshot, envelope.SessionID)
		// 发布事件只在 accepted 路径记一次，避免 duplicate 计数膨胀。
		handler.metrics.ObserveBridgeServicePublish(resolvedServiceID, publishedServiceInstanceID)
		// 发布后立即刷新可用实例快照，确保实例级可用数可观测。
		RefreshServiceAvailabilityMetrics(handler.metrics, handler.serviceRegistry, resolvedServiceID)
	}
	if publishedServiceInstanceID == "" {
		// 未触发写入时回查实例，保证审计字段尽量完整。
		publishedServiceInstanceID = handler.lookupServiceInstanceID(
			resolvedServiceID,
			resolvedServiceKey,
			resolvedConnectorID,
			envelope.SessionID,
		)
	}
	handler.emitServiceResourceAuditLog(
		"publish",
		decision,
		resolvedServiceID,
		resolvedServiceKey,
		publishedServiceInstanceID,
		resolvedConnectorID,
		envelope.SessionID,
		envelope.SessionEpoch,
		envelope.ResourceVersion,
	)

	return consistency.BuildPublishServiceAck(
		decision.Status,
		resolvedServiceID,
		resolvedServiceKey,
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
	resolvedConnectorID := handler.resolveConnectorID(envelope)
	resolvedServiceID := handler.resolveServiceID(message.ServiceID, message.ServiceKey)
	resolvedServiceKey := strings.TrimSpace(message.ServiceKey)
	serviceInstanceIDForAudit := handler.lookupServiceInstanceID(
		resolvedServiceID,
		resolvedServiceKey,
		resolvedConnectorID,
		envelope.SessionID,
	)

	resourceID := handler.resolveServiceInstanceResourceID(
		message.ServiceID,
		message.ServiceKey,
		resolvedConnectorID,
		envelope.SessionID,
	)
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
			removed := handler.serviceRegistry.RemoveInstanceByServiceIDAndRuntime(
				message.ServiceID,
				resolvedConnectorID,
				envelope.SessionID,
			)
			if !removed {
				// 兼容旧路径：实例维度未命中时回退整池删除。
				handler.serviceRegistry.RemoveByServiceID(message.ServiceID)
			}
		} else {
			removed := handler.serviceRegistry.RemoveInstanceByServiceKeyAndRuntime(
				message.ServiceKey,
				resolvedConnectorID,
				envelope.SessionID,
			)
			if !removed {
				// 兼容旧路径：实例维度未命中时回退整池删除。
				handler.serviceRegistry.RemoveByServiceKey(message.ServiceKey)
			}
		}
		// 下线后回刷可用实例快照，确保服务池可用数及时收敛。
		RefreshServiceAvailabilityMetrics(handler.metrics, handler.serviceRegistry, resolvedServiceID)
	}
	handler.emitServiceResourceAuditLog(
		"unpublish",
		decision,
		resolvedServiceID,
		resolvedServiceKey,
		serviceInstanceIDForAudit,
		resolvedConnectorID,
		envelope.SessionID,
		envelope.SessionEpoch,
		envelope.ResourceVersion,
	)

	return consistency.BuildUnpublishServiceAck(
		decision.Status,
		resolvedServiceID,
		resolvedServiceKey,
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
	versionSnapshot := make(map[string]uint64, len(snapshot.Services)*3)
	for _, service := range snapshot.Services {
		baseResourceID := resolveServiceResourceID("", service.ServiceID, service.ServiceKey)
		if baseResourceID == "" {
			// 兜底保护：跳过无法索引的服务条目。
			continue
		}
		// 兼容历史池级键，保证旧链路可继续命中版本视图。
		versionSnapshot["service:"+baseResourceID] = service.ResourceVersion
		normalizedConnectorID := strings.TrimSpace(service.ConnectorID)
		if normalizedConnectorID == "" {
			continue
		}
		// full-sync 至少回填 connector 维度版本，避免后续实例键首次写入绕过版本回滚保护。
		connectorScopedResourceID := buildServiceInstanceResourceID(baseResourceID, normalizedConnectorID, "")
		if connectorScopedResourceID != "" {
			versionSnapshot["service:"+connectorScopedResourceID] = service.ResourceVersion
		}
		// 若当前 connector 已有活跃会话，再补齐 session 维度实例键，和 publish/unpublish 判重口径保持一致。
		resolvedSessionID := handler.resolveCurrentSessionIDByConnector(normalizedConnectorID)
		if resolvedSessionID == "" {
			continue
		}
		sessionScopedResourceID := buildServiceInstanceResourceID(baseResourceID, normalizedConnectorID, resolvedSessionID)
		if sessionScopedResourceID != "" {
			versionSnapshot["service:"+sessionScopedResourceID] = service.ResourceVersion
		}
	}
	// full-sync 后用权威版本刷新本地视图。
	handler.guard.ReplaceAllVersions(versionSnapshot)
}

// resolveCurrentSessionIDByConnector 返回 connector 当前会话 ID（不存在时返回空串）。
func (handler *PublishHandler) resolveCurrentSessionIDByConnector(connectorID string) string {
	if handler == nil || handler.sessionRegistry == nil {
		return ""
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return ""
	}
	sessionRuntime, exists := handler.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !exists {
		return ""
	}
	return strings.TrimSpace(sessionRuntime.SessionID)
}

// rejectPublishAck 构造发布拒绝 ACK。
func (handler *PublishHandler) rejectPublishAck(message pb.PublishService, err error) pb.PublishServiceAck {
	currentVersion := handler.serviceRegistry.CurrentVersion(message.ServiceID, message.ServiceKey)
	return consistency.BuildPublishServiceAck(
		pb.EventStatusRejected,
		handler.resolveServiceID(message.ServiceID, message.ServiceKey),
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
		handler.resolveServiceID(message.ServiceID, message.ServiceKey),
		message.ServiceKey,
		currentVersion,
		currentVersion,
		extractErrorCode(err),
		err.Error(),
	)
}

// buildServiceSnapshot 将发布消息转为注册表快照。
func buildServiceSnapshot(message pb.PublishService, resourceVersion uint64, connectorID string) pb.Service {
	serviceID := strings.TrimSpace(message.ServiceID)
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

// normalizePublishMessageIdentity 统一发布消息中的协议、service_key 与 service_id 口径。
func (handler *PublishHandler) normalizePublishMessageIdentity(message pb.PublishService) pb.PublishService {
	normalizedMessage := message
	normalizedMessage.ServiceID = strings.TrimSpace(normalizedMessage.ServiceID)
	normalizedMessage.ServiceKey = strings.TrimSpace(normalizedMessage.ServiceKey)
	normalizedMessage.ServiceName = strings.TrimSpace(normalizedMessage.ServiceName)

	normalizedProtocol := ""
	for index := range normalizedMessage.Endpoints {
		normalizedEndpointProtocol := strings.ToLower(strings.TrimSpace(normalizedMessage.Endpoints[index].Protocol))
		normalizedMessage.Endpoints[index].Protocol = normalizedEndpointProtocol
		if normalizedProtocol == "" {
			normalizedProtocol = normalizedEndpointProtocol
		}
	}
	if normalizedMessage.ServiceKey == "" &&
		normalizedMessage.ServiceName != "" &&
		normalizedProtocol != "" {
		normalizedMessage.ServiceKey = normalizedMessage.ServiceName + "/" + normalizedProtocol
	}
	if normalizedMessage.ServiceID == "" {
		normalizedMessage.ServiceID = handler.resolvePublishServiceIDByKey(normalizedMessage.ServiceKey)
	}
	return normalizedMessage
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
func (handler *PublishHandler) resolveServiceID(serviceID string, serviceKey string) string {
	normalizedServiceID := resolveServiceID(serviceID, serviceKey)
	if normalizedServiceID != strings.TrimSpace(serviceKey) {
		return normalizedServiceID
	}
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey == "" {
		return normalizedServiceID
	}
	if handler != nil && handler.serviceRegistry != nil {
		if serviceSnapshot, exists := handler.serviceRegistry.GetByServiceKey(normalizedServiceKey); exists {
			if mappedServiceID := strings.TrimSpace(serviceSnapshot.ServiceID); mappedServiceID != "" {
				return mappedServiceID
			}
		}
	}
	return normalizedServiceID
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
func (handler *PublishHandler) resolveServiceResourceID(resourceID string, serviceID string, serviceKey string) string {
	normalizedResourceID := strings.TrimSpace(resourceID)
	if normalizedResourceID != "" {
		// envelope.resourceId 优先级最高。
		return normalizedResourceID
	}
	return handler.resolveServiceID(serviceID, serviceKey)
}

// resolveServiceInstanceResourceID 解析实例维度资源键，避免多 connector 共享 service_id 时版本串扰。
func (handler *PublishHandler) resolveServiceInstanceResourceID(
	serviceID string,
	serviceKey string,
	connectorID string,
	sessionID string,
) string {
	baseResourceID := handler.resolveServiceID(serviceID, serviceKey)
	return buildServiceInstanceResourceID(baseResourceID, connectorID, sessionID)
}

// resolveServiceResourceID 解析资源版本比较所用的资源键。
func resolveServiceResourceID(resourceID string, serviceID string, serviceKey string) string {
	normalizedResourceID := strings.TrimSpace(resourceID)
	if normalizedResourceID != "" {
		return normalizedResourceID
	}
	return resolveServiceID(serviceID, serviceKey)
}

// buildServiceInstanceResourceID 构造实例维度资源键（service_id/service_key + connector_id + session_id）。
func buildServiceInstanceResourceID(baseResourceID string, connectorID string, sessionID string) string {
	normalizedBaseResourceID := strings.TrimSpace(baseResourceID)
	if normalizedBaseResourceID == "" {
		return ""
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedConnectorID == "" && normalizedSessionID == "" {
		// 未带 runtime 维度时退化为池级资源键，保持向后兼容。
		return normalizedBaseResourceID
	}
	return normalizedBaseResourceID + "|" + normalizedConnectorID + "|" + normalizedSessionID
}

// resolvePublishServiceIDByKey 解析发布路径的 service_id：优先复用，缺失时生成新的 opaque id。
func (handler *PublishHandler) resolvePublishServiceIDByKey(serviceKey string) string {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey != "" && handler != nil && handler.serviceRegistry != nil {
		if serviceSnapshot, exists := handler.serviceRegistry.GetByServiceKey(normalizedServiceKey); exists {
			if mappedServiceID := strings.TrimSpace(serviceSnapshot.ServiceID); mappedServiceID != "" {
				return mappedServiceID
			}
		}
	}
	if handler == nil || handler.serviceIDGenerator == nil {
		return defaultPublishServiceIDGenerator(time.Now().UTC(), normalizedServiceKey)
	}
	return handler.serviceIDGenerator(handler.now(), normalizedServiceKey)
}

// defaultPublishServiceIDGenerator 生成发布路径默认 service_id。
func defaultPublishServiceIDGenerator(now time.Time, _ string) string {
	normalizedNow := now.UTC()
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	sequence := atomic.AddUint64(&publishServiceIDSequence, 1)
	return fmt.Sprintf("svc-%d-%d", normalizedNow.UnixNano(), sequence)
}

// lookupServiceInstanceID 按 service/runtime 维度回查 service_instance_id，用于审计字段补全。
func (handler *PublishHandler) lookupServiceInstanceID(
	serviceID string,
	serviceKey string,
	connectorID string,
	sessionID string,
) string {
	if handler == nil || handler.serviceRegistry == nil {
		return ""
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	var instances []registry.ServiceInstanceSnapshot
	if normalizedServiceID != "" {
		instances = handler.serviceRegistry.ListInstancesByServiceID(normalizedServiceID)
	} else if normalizedServiceKey != "" {
		instances = handler.serviceRegistry.ListInstancesByServiceKey(normalizedServiceKey)
	}
	for _, instance := range instances {
		if strings.TrimSpace(instance.Service.ConnectorID) != normalizedConnectorID {
			continue
		}
		if strings.TrimSpace(instance.SessionID) != normalizedSessionID {
			continue
		}
		return strings.TrimSpace(instance.ServiceInstanceID)
	}
	return ""
}

// emitServiceResourceAuditLog 输出 service 资源变更审计日志，统一透出服务身份字段。
func (handler *PublishHandler) emitServiceResourceAuditLog(
	action string,
	decision consistency.ResourceEventDecision,
	serviceID string,
	serviceKey string,
	serviceInstanceID string,
	connectorID string,
	sessionID string,
	sessionEpoch uint64,
	resourceVersion uint64,
) {
	slog.Info(
		"service resource audit",
		"action", strings.TrimSpace(action),
		"event_status", decision.Status,
		"error_code", strings.TrimSpace(decision.ErrorCode),
		"service_id", strings.TrimSpace(serviceID),
		"service_key", strings.TrimSpace(serviceKey),
		"service_instance_id", strings.TrimSpace(serviceInstanceID),
		"connector_id", strings.TrimSpace(connectorID),
		"session_id", strings.TrimSpace(sessionID),
		"session_epoch", sessionEpoch,
		"resource_version", resourceVersion,
		"accepted_resource_version", decision.AcceptedResourceVersion,
		"current_resource_version", decision.CurrentResourceVersion,
	)
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
	if sessionRuntime.State != registry.SessionActive {
		// 只有 ACTIVE 会话允许写入资源，draining/stale/failed/closed 一律冻结写路径。
		return ltfperrors.New(ltfperrors.CodeInvalidStateTransition, "session state does not allow resource mutation")
	}
	return nil
}
