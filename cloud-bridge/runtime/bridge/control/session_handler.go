package control

import (
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// SessionHandlerOptions 定义 SessionHandler 的依赖注入。
type SessionHandlerOptions struct {
	SessionRegistry *registry.SessionRegistry
	ServiceRegistry *registry.ServiceRegistry
	RouteRegistry   *registry.RouteRegistry
	Guard           *consistency.ResourceEventGuard
	Now             func() time.Time
}

// SessionHandler 管理会话生命周期与重连对账流程。
type SessionHandler struct {
	mu               sync.Mutex
	sessionRegistry  *registry.SessionRegistry
	serviceRegistry  *registry.ServiceRegistry
	routeRegistry    *registry.RouteRegistry
	guard            *consistency.ResourceEventGuard
	now              func() time.Time
	lastSessionID    string
	lastSessionEpoch uint64
	lastResourceVer  uint64
}

// NewSessionHandler 创建会话处理器。
func NewSessionHandler(options SessionHandlerOptions) *SessionHandler {
	nowFunc := options.Now
	if nowFunc == nil {
		// 默认统一使用 UTC 时间，减少跨时区歧义。
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	sessionRegistry := options.SessionRegistry
	if sessionRegistry == nil {
		// 未注入时创建默认会话注册表。
		sessionRegistry = registry.NewSessionRegistry()
	}
	serviceRegistry := options.ServiceRegistry
	if serviceRegistry == nil {
		// 未注入时创建默认服务注册表。
		serviceRegistry = registry.NewServiceRegistry()
	}
	routeRegistry := options.RouteRegistry
	if routeRegistry == nil {
		// 未注入时创建默认路由注册表。
		routeRegistry = registry.NewRouteRegistry()
	}
	guard := options.Guard
	if guard == nil {
		// 默认共享一个事件守卫实例。
		guard = consistency.NewResourceEventGuard(4096)
	}
	return &SessionHandler{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
		routeRegistry:   routeRegistry,
		guard:           guard,
		now:             nowFunc,
	}
}

// UpsertSession 写入或更新会话运行态。
func (handler *SessionHandler) UpsertSession(runtime registry.SessionRuntime) {
	// session 事件进入后立即刷新会话视图。
	handler.sessionRegistry.Upsert(handler.now(), runtime)
}

// BuildReconnectSyncPlan 生成重连后的同步策略。
func (handler *SessionHandler) BuildReconnectSyncPlan(sessionID string, sessionEpoch uint64) consistency.ReconnectSyncPlan {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	// 依据上次已确认基线，计算 full-sync 或增量同步策略。
	return consistency.BuildReconnectSyncPlan(
		handler.lastSessionID,
		handler.lastSessionEpoch,
		sessionID,
		sessionEpoch,
		handler.lastResourceVer,
	)
}

// MarkReconnectBaseline 更新重连基线（用于后续对账决策）。
func (handler *SessionHandler) MarkReconnectBaseline(sessionID string, sessionEpoch uint64, resourceVersion uint64) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	// 仅记录最新基线，作为下次重连比较依据。
	handler.lastSessionID = sessionID
	handler.lastSessionEpoch = sessionEpoch
	handler.lastResourceVer = resourceVersion
}

// ApplyFullSyncSnapshot 应用 full-sync 快照并重建本地状态。
func (handler *SessionHandler) ApplyFullSyncSnapshot(snapshot pb.FullSyncSnapshot) {
	if !snapshot.Completed {
		// 快照未完成时不执行覆盖，避免中间态污染。
		return
	}
	now := handler.now()
	// 先覆盖服务与路由视图，确保运行态与快照一致。
	handler.serviceRegistry.ReplaceAll(now, snapshot.Services)
	handler.routeRegistry.ReplaceAll(now, snapshot.Routes)

	versionSnapshot := make(map[string]uint64, len(snapshot.Services)+len(snapshot.Routes))
	for _, service := range snapshot.Services {
		resourceID := resolveServiceResourceID("", service.ServiceID, service.ServiceKey)
		if resourceID == "" {
			// 服务索引键不合法时跳过。
			continue
		}
		versionSnapshot["service:"+resourceID] = service.ResourceVersion
	}
	for _, route := range snapshot.Routes {
		if route.RouteID == "" {
			// routeId 为空时无法建立版本键。
			continue
		}
		versionSnapshot["route:"+route.RouteID] = route.ResourceVersion
	}
	// 覆盖事件守卫版本视图，确保增量事件比较基线正确。
	handler.guard.ReplaceAllVersions(versionSnapshot)

	handler.mu.Lock()
	defer handler.mu.Unlock()
	// full-sync 完成后刷新会话与资源版本基线。
	handler.lastResourceVer = snapshot.SnapshotVersion
	if snapshot.SessionEpoch > handler.lastSessionEpoch {
		// 会话代际提升时更新为新的权威 epoch。
		handler.lastSessionEpoch = snapshot.SessionEpoch
	}
}

// CurrentBaseline 返回当前记录的重连对账基线。
func (handler *SessionHandler) CurrentBaseline() (string, uint64, uint64) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	// 返回 sessionID、sessionEpoch、resourceVersion 三元组。
	return handler.lastSessionID, handler.lastSessionEpoch, handler.lastResourceVer
}
