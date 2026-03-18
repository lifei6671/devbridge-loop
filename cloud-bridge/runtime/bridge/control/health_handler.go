package control

import (
	"log/slog"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// HealthHandlerOptions 定义健康上报处理器依赖。
type HealthHandlerOptions struct {
	SessionRegistry *registry.SessionRegistry
	ServiceRegistry *registry.ServiceRegistry
	Metrics         *obs.Metrics
	Now             func() time.Time
}

// HealthHandler 负责将 Agent 上报的健康状态写入服务注册表。
type HealthHandler struct {
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	metrics         *obs.Metrics
	now             func() time.Time
}

// NewHealthHandler 创建健康上报处理器。
func NewHealthHandler(options HealthHandlerOptions) *HealthHandler {
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	serviceRegistry := options.ServiceRegistry
	if serviceRegistry == nil {
		serviceRegistry = registry.NewServiceRegistry()
	}
	metrics := options.Metrics
	if metrics == nil {
		// 未显式注入指标容器时回落默认容器，保证健康维度指标连续。
		metrics = obs.DefaultMetrics
	}
	return &HealthHandler{
		sessionRegistry: options.SessionRegistry,
		serviceRegistry: serviceRegistry,
		metrics:         metrics,
		now:             nowFunc,
	}
}

// HandleReport 处理 ServiceHealthReport 并更新 registry 快照。
func (handler *HealthHandler) HandleReport(envelope pb.ControlEnvelope, report pb.ServiceHealthReport) {
	if handler == nil || handler.serviceRegistry == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(report.ServiceID)
	normalizedServiceKey := strings.TrimSpace(report.ServiceKey)
	normalizedConnectorID := strings.TrimSpace(envelope.ConnectorID)
	normalizedSessionID := strings.TrimSpace(envelope.SessionID)
	if !handler.validateSessionEpoch(envelope) {
		// 非 ACTIVE 会话或旧代际上报直接丢弃，避免覆盖新 session 真相源。
		slog.Info(
			"bridge ignore service health report: invalid session runtime",
			"connector_id", strings.TrimSpace(envelope.ConnectorID),
			"session_id", strings.TrimSpace(envelope.SessionID),
			"session_epoch", envelope.SessionEpoch,
			"service_id", normalizedServiceID,
			"service_key", normalizedServiceKey,
			"reported_health_status", report.ServiceHealthStatus,
		)
		return
	}
	serviceInstances := handler.lookupServiceInstances(normalizedServiceID, normalizedServiceKey)
	if len(serviceInstances) == 0 {
		// 健康上报先于发布到达时保持无副作用，等待 publish 建立主记录。
		slog.Info(
			"bridge ignore service health report: service not found",
			"connector_id", strings.TrimSpace(envelope.ConnectorID),
			"session_id", strings.TrimSpace(envelope.SessionID),
			"session_epoch", envelope.SessionEpoch,
			"service_id", normalizedServiceID,
			"service_key", normalizedServiceKey,
			"reported_health_status", report.ServiceHealthStatus,
		)
		return
	}
	updatedCount := 0
	updatedServiceIDs := make(map[string]struct{}, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		instanceConnectorID := strings.TrimSpace(serviceInstance.Service.ConnectorID)
		instanceSessionID := strings.TrimSpace(serviceInstance.SessionID)
		if normalizedConnectorID != "" && instanceConnectorID != normalizedConnectorID {
			// connector 不匹配的实例不接受本次健康更新。
			continue
		}
		if normalizedSessionID != "" && instanceSessionID != "" && instanceSessionID != normalizedSessionID {
			// 已记录 session_id 且与上报不一致时，避免跨会话污染实例健康状态。
			continue
		}
		previousHealthStatus := serviceInstance.Service.HealthStatus
		serviceSnapshot := serviceInstance.Service
		serviceSnapshot.HealthStatus = normalizeHealthStatus(report.ServiceHealthStatus)
		handler.serviceRegistry.UpsertWithRuntime(
			handler.now(),
			serviceSnapshot,
			instanceSessionID,
		)
		slog.Info(
			"bridge apply service health report",
			"connector_id", instanceConnectorID,
			"session_id", firstNonEmpty(instanceSessionID, normalizedSessionID),
			"session_epoch", envelope.SessionEpoch,
			"service_id", strings.TrimSpace(serviceSnapshot.ServiceID),
			"service_key", strings.TrimSpace(serviceSnapshot.ServiceKey),
			"service_instance_id", strings.TrimSpace(serviceInstance.ServiceInstanceID),
			"service_status", serviceSnapshot.Status,
			"health_status_before", previousHealthStatus,
			"health_status_after", serviceSnapshot.HealthStatus,
			"resource_version", serviceSnapshot.ResourceVersion,
		)
		updatedServiceIDs[strings.TrimSpace(serviceSnapshot.ServiceID)] = struct{}{}
		updatedCount++
	}
	if updatedCount == 0 {
		// 所有实例都因 connector/session 过滤未命中时，不执行写入。
		slog.Info(
			"bridge ignore service health report: no matching service instance",
			"connector_id", normalizedConnectorID,
			"session_id", normalizedSessionID,
			"session_epoch", envelope.SessionEpoch,
			"service_id", normalizedServiceID,
			"service_key", normalizedServiceKey,
			"reported_health_status", report.ServiceHealthStatus,
		)
		return
	}
	slog.Info(
		"bridge apply service health report summary",
		"connector_id", normalizedConnectorID,
		"session_id", normalizedSessionID,
		"session_epoch", envelope.SessionEpoch,
		"service_id", normalizedServiceID,
		"service_key", normalizedServiceKey,
		"updated_instances", updatedCount,
	)
	// 健康更新完成后，按受影响 service_id 批量刷新可用实例指标快照。
	affectedServiceIDs := make([]string, 0, len(updatedServiceIDs))
	for serviceID := range updatedServiceIDs {
		if strings.TrimSpace(serviceID) == "" {
			continue
		}
		affectedServiceIDs = append(affectedServiceIDs, serviceID)
	}
	RefreshServiceAvailabilityMetricsByServiceIDs(handler.metrics, handler.serviceRegistry, affectedServiceIDs)
}

// lookupServiceInstances 按 service_id/service_key 查找服务池内实例快照。
func (handler *HealthHandler) lookupServiceInstances(serviceID string, serviceKey string) []registry.ServiceInstanceSnapshot {
	if strings.TrimSpace(serviceID) != "" {
		if serviceSnapshots := handler.serviceRegistry.ListInstancesByServiceID(serviceID); len(serviceSnapshots) > 0 {
			return serviceSnapshots
		}
	}
	if strings.TrimSpace(serviceKey) != "" {
		if serviceSnapshots := handler.serviceRegistry.ListInstancesByServiceKey(serviceKey); len(serviceSnapshots) > 0 {
			return serviceSnapshots
		}
	}
	return nil
}

// validateSessionEpoch 校验上报是否来自当前有效会话代际。
func (handler *HealthHandler) validateSessionEpoch(envelope pb.ControlEnvelope) bool {
	if handler.sessionRegistry == nil {
		// 未注入 session 视图时保持向后兼容。
		return true
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	if sessionID == "" || envelope.SessionEpoch == 0 {
		return false
	}
	sessionRuntime, exists := handler.sessionRegistry.GetBySession(sessionID)
	if !exists {
		return false
	}
	// 仅 ACTIVE 会话允许写入健康状态，防止旧会话在 draining/stale/failed 阶段继续污染状态。
	return sessionRuntime.Epoch == envelope.SessionEpoch && sessionRuntime.State == registry.SessionActive
}

// normalizeHealthStatus 将非法健康状态回退为 UNKNOWN。
func normalizeHealthStatus(status pb.HealthStatus) pb.HealthStatus {
	switch status {
	case pb.HealthStatusHealthy, pb.HealthStatusUnhealthy, pb.HealthStatusUnknown:
		return status
	default:
		return pb.HealthStatusUnknown
	}
}

// firstNonEmpty 返回首个非空字符串，便于按实例 session_id 回退 envelope.session_id。
func firstNonEmpty(candidates ...string) string {
	for _, candidate := range candidates {
		normalized := strings.TrimSpace(candidate)
		if normalized != "" {
			return normalized
		}
	}
	return ""
}
