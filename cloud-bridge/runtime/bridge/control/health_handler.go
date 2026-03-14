package control

import (
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// HealthHandlerOptions 定义健康上报处理器依赖。
type HealthHandlerOptions struct {
	SessionRegistry *registry.SessionRegistry
	ServiceRegistry *registry.ServiceRegistry
	Now             func() time.Time
}

// HealthHandler 负责将 Agent 上报的健康状态写入服务注册表。
type HealthHandler struct {
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
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
	return &HealthHandler{
		sessionRegistry: options.SessionRegistry,
		serviceRegistry: serviceRegistry,
		now:             nowFunc,
	}
}

// HandleReport 处理 ServiceHealthReport 并更新 registry 快照。
func (handler *HealthHandler) HandleReport(envelope pb.ControlEnvelope, report pb.ServiceHealthReport) {
	if handler == nil || handler.serviceRegistry == nil {
		return
	}
	if !handler.validateSessionEpoch(envelope) {
		// 旧会话上报直接丢弃，避免覆盖新 session 真相源。
		return
	}

	normalizedServiceID := strings.TrimSpace(report.ServiceID)
	normalizedServiceKey := strings.TrimSpace(report.ServiceKey)
	serviceSnapshot, exists := handler.lookupService(normalizedServiceID, normalizedServiceKey)
	if !exists {
		// 健康上报先于发布到达时保持无副作用，等待 publish 建立主记录。
		return
	}
	serviceSnapshot.HealthStatus = normalizeHealthStatus(report.ServiceHealthStatus)
	handler.serviceRegistry.Upsert(handler.now(), serviceSnapshot)
}

// lookupService 按 service_id/service_key 查找当前服务快照。
func (handler *HealthHandler) lookupService(serviceID string, serviceKey string) (pb.Service, bool) {
	if strings.TrimSpace(serviceID) != "" {
		if serviceSnapshot, exists := handler.serviceRegistry.GetByServiceID(serviceID); exists {
			return serviceSnapshot, true
		}
	}
	if strings.TrimSpace(serviceKey) != "" {
		if serviceSnapshot, exists := handler.serviceRegistry.GetByServiceKey(serviceKey); exists {
			return serviceSnapshot, true
		}
	}
	return pb.Service{}, false
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
	return sessionRuntime.Epoch == envelope.SessionEpoch
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
