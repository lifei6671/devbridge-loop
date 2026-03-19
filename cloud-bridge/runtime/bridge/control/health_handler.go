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
	normalizedInstanceID := strings.TrimSpace(report.InstanceID)
	normalizedLogicalServiceID := strings.TrimSpace(report.LogicalServiceID)
	normalizedConnectorID := strings.TrimSpace(envelope.ConnectorID)
	normalizedSessionID := strings.TrimSpace(envelope.SessionID)
	if !handler.validateSessionEpoch(envelope) {
		slog.Info(
			"bridge ignore service health report: invalid session runtime",
			"connector_id", normalizedConnectorID,
			"session_id", normalizedSessionID,
			"session_epoch", envelope.SessionEpoch,
			"logical_service_id", normalizedLogicalServiceID,
			"instance_id", normalizedInstanceID,
			"reported_health_status", report.ServiceHealthStatus,
		)
		return
	}
	instanceSnapshot, exists := handler.serviceRegistry.GetInstanceByID(normalizedInstanceID)
	if !exists {
		slog.Info(
			"bridge ignore service health report: instance not found",
			"connector_id", normalizedConnectorID,
			"session_id", normalizedSessionID,
			"session_epoch", envelope.SessionEpoch,
			"logical_service_id", normalizedLogicalServiceID,
			"instance_id", normalizedInstanceID,
			"reported_health_status", report.ServiceHealthStatus,
		)
		return
	}
	if normalizedConnectorID != "" && strings.TrimSpace(instanceSnapshot.Instance.ConnectorID) != normalizedConnectorID {
		slog.Info(
			"bridge ignore service health report: connector mismatch",
			"connector_id", normalizedConnectorID,
			"instance_connector_id", strings.TrimSpace(instanceSnapshot.Instance.ConnectorID),
			"instance_id", normalizedInstanceID,
		)
		return
	}
	if normalizedSessionID != "" && strings.TrimSpace(instanceSnapshot.Instance.SessionID) != "" &&
		strings.TrimSpace(instanceSnapshot.Instance.SessionID) != normalizedSessionID {
		slog.Info(
			"bridge ignore service health report: session mismatch",
			"session_id", normalizedSessionID,
			"instance_session_id", strings.TrimSpace(instanceSnapshot.Instance.SessionID),
			"instance_id", normalizedInstanceID,
		)
		return
	}

	previousHealthStatus := instanceSnapshot.Instance.HealthStatus
	instanceSnapshot.Instance.HealthStatus = normalizeHealthStatus(report.ServiceHealthStatus)
	instanceSnapshot.UpdatedAt = handler.now()
	handler.serviceRegistry.Upsert(instanceSnapshot.UpdatedAt, instanceSnapshot.LogicalService, instanceSnapshot.Instance)
	slog.Info(
		"bridge apply service health report",
		"connector_id", strings.TrimSpace(instanceSnapshot.Instance.ConnectorID),
		"session_id", firstNonEmpty(instanceSnapshot.Instance.SessionID, normalizedSessionID),
		"session_epoch", envelope.SessionEpoch,
		"logical_service_id", strings.TrimSpace(instanceSnapshot.Instance.LogicalServiceID),
		"instance_id", strings.TrimSpace(instanceSnapshot.Instance.InstanceID),
		"instance_status", instanceSnapshot.Instance.InstanceStatus,
		"health_status_before", previousHealthStatus,
		"health_status_after", instanceSnapshot.Instance.HealthStatus,
		"resource_version", instanceSnapshot.Instance.ResourceVersion,
	)
	RefreshServiceAvailabilityMetrics(handler.metrics, handler.serviceRegistry, instanceSnapshot.Instance.LogicalServiceID)
}

// validateSessionEpoch 校验上报是否来自当前有效会话代际。
func (handler *HealthHandler) validateSessionEpoch(envelope pb.ControlEnvelope) bool {
	if handler.sessionRegistry == nil {
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
