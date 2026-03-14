package tunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
)

var (
	// ErrManagerDependencyMissing 表示 Manager 关键依赖缺失。
	ErrManagerDependencyMissing = errors.New("manager dependency missing")
	// ErrInvalidManagerConfig 表示 Manager 参数配置不合法。
	ErrInvalidManagerConfig = errors.New("invalid manager config")
)

const (
	// SessionStateActive 表示 session 已恢复可接流量。
	SessionStateActive = "ACTIVE"
	// SessionStateDraining 表示 session 进入排空状态。
	SessionStateDraining = "DRAINING"
	// SessionStateStale 表示 session 进入过期状态。
	SessionStateStale = "STALE"
)

// ManagerConfig 定义 TunnelManager 的池治理参数。
type ManagerConfig struct {
	MinIdle           int
	MaxIdle           int
	IdleTTL           time.Duration
	MaxInflightOpens  int
	TunnelOpenRate    float64
	TunnelOpenBurst   int
	ReconcileInterval time.Duration
}

// DefaultManagerConfig 返回文档建议的默认参数。
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		MinIdle:           8,
		MaxIdle:           32,
		IdleTTL:           90 * time.Second,
		MaxInflightOpens:  4,
		TunnelOpenRate:    10,
		TunnelOpenBurst:   20,
		ReconcileInterval: time.Second,
	}
}

// NormalizeAndValidate 归一化并校验治理参数。
func (config ManagerConfig) NormalizeAndValidate() (ManagerConfig, error) {
	normalizedConfig := config
	defaultConfig := DefaultManagerConfig()

	if normalizedConfig.MinIdle < 0 {
		// min_idle 负值统一归零。
		normalizedConfig.MinIdle = 0
	}
	if normalizedConfig.MaxIdle <= 0 {
		// max_idle 未设置时回落默认值。
		normalizedConfig.MaxIdle = defaultConfig.MaxIdle
	}
	if normalizedConfig.MinIdle > normalizedConfig.MaxIdle {
		// min 不得超过 max，避免目标容量矛盾。
		return ManagerConfig{}, fmt.Errorf(
			"normalize manager config: %w: min_idle=%d max_idle=%d",
			ErrInvalidManagerConfig,
			normalizedConfig.MinIdle,
			normalizedConfig.MaxIdle,
		)
	}
	if normalizedConfig.IdleTTL < 0 {
		// 负值 TTL 不合法。
		return ManagerConfig{}, fmt.Errorf(
			"normalize manager config: %w: idle_ttl=%v",
			ErrInvalidManagerConfig,
			normalizedConfig.IdleTTL,
		)
	}
	if normalizedConfig.MaxInflightOpens <= 0 {
		// 未配置 inflight 时回落默认值。
		normalizedConfig.MaxInflightOpens = defaultConfig.MaxInflightOpens
	}
	if normalizedConfig.TunnelOpenRate < 0 {
		// 速率不能为负值。
		return ManagerConfig{}, fmt.Errorf(
			"normalize manager config: %w: tunnel_open_rate=%v",
			ErrInvalidManagerConfig,
			normalizedConfig.TunnelOpenRate,
		)
	}
	if normalizedConfig.TunnelOpenRate == 0 {
		// 未配置速率时回落默认值。
		normalizedConfig.TunnelOpenRate = defaultConfig.TunnelOpenRate
	}
	if normalizedConfig.TunnelOpenBurst < 0 {
		// burst 不能为负数。
		return ManagerConfig{}, fmt.Errorf(
			"normalize manager config: %w: tunnel_open_burst=%d",
			ErrInvalidManagerConfig,
			normalizedConfig.TunnelOpenBurst,
		)
	}
	if normalizedConfig.TunnelOpenBurst == 0 {
		// 未配置 burst 时回落默认值。
		normalizedConfig.TunnelOpenBurst = defaultConfig.TunnelOpenBurst
	}
	if normalizedConfig.ReconcileInterval <= 0 {
		// 未配置纠偏间隔时回落默认值。
		normalizedConfig.ReconcileInterval = defaultConfig.ReconcileInterval
	}
	return normalizedConfig, nil
}

// PoolEventNotifier 接收池状态变更事件通知。
type PoolEventNotifier interface {
	// NotifyEvent 通知上层触发一次事件驱动上报。
	NotifyEvent(trigger string)
}

// ManagerOptions 定义 TunnelManager 依赖注入参数。
type ManagerOptions struct {
	Config      ManagerConfig
	Registry    *Registry
	Producer    *Producer
	Opener      TunnelOpener
	EventNotify PoolEventNotifier
	NowFn       func() time.Time
	Metrics     *obs.Metrics
}

// ReconcileResult 描述一次池纠偏结果。
type ReconcileResult struct {
	Trigger             string
	Before              Snapshot
	After               Snapshot
	RequestedTargetIdle int
	EffectiveTargetIdle int
	TrimmedIdleCount    int
	Produced            ProduceResult
	TTLSweep            TTLSweepResult
}

// Manager 负责 tunnel pool 全生命周期治理。
type Manager struct {
	config    ManagerConfig
	registry  *Registry
	producer  *Producer
	reaper    *Reaper
	ttlReaper *TTLReaper
	nowFn     func() time.Time
	metrics   *obs.Metrics

	eventNotify PoolEventNotifier

	refillSignalChannel chan struct{}
	reconcileMutex      sync.Mutex
	stateMutex          sync.Mutex
	pendingTargetIdle   int
	pendingReason       string
	suspendRefill       bool
}

// NewManager 创建 tunnel pool 管理器。
func NewManager(options ManagerOptions) (*Manager, error) {
	normalizedConfig, err := options.Config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}

	registry := options.Registry
	if registry == nil {
		// 未注入 registry 时默认创建新实例。
		registry = NewRegistry()
	}

	normalizedNowFn := options.NowFn
	if normalizedNowFn == nil {
		// 默认统一使用 UTC now。
		normalizedNowFn = func() time.Time { return time.Now().UTC() }
	}

	producer := options.Producer
	if producer == nil {
		if options.Opener == nil {
			// 既没有 producer 也没有 opener 时无法执行建连。
			return nil, fmt.Errorf("new manager: %w", ErrManagerDependencyMissing)
		}
		producer, err = NewProducer(options.Opener, ProducerConfig{
			MaxInflight: normalizedConfig.MaxInflightOpens,
			RateLimit:   normalizedConfig.TunnelOpenRate,
			Burst:       normalizedConfig.TunnelOpenBurst,
		})
		if err != nil {
			return nil, err
		}
	}

	reaper := NewReaper(registry, normalizedNowFn)
	ttlReaper := NewTTLReaper(registry, reaper, TTLReaperConfig{IdleTTL: normalizedConfig.IdleTTL}, normalizedNowFn)
	manager := &Manager{
		config:              normalizedConfig,
		registry:            registry,
		producer:            producer,
		reaper:              reaper,
		ttlReaper:           ttlReaper,
		nowFn:               normalizedNowFn,
		metrics:             obs.DefaultMetrics,
		eventNotify:         options.EventNotify,
		refillSignalChannel: make(chan struct{}, 1),
		pendingTargetIdle:   normalizedConfig.MinIdle,
		pendingReason:       "startup",
	}
	if options.Metrics != nil {
		manager.metrics = options.Metrics
	}
	// 启动前先把当前池快照写入指标，避免冷启动期间指标为空。
	manager.updatePoolMetrics(manager.registry.Snapshot())
	return manager, nil
}

// BindEventNotifier 绑定池事件通知器。
func (manager *Manager) BindEventNotifier(notifier PoolEventNotifier) {
	manager.stateMutex.Lock()
	defer manager.stateMutex.Unlock()
	// 运行时允许替换通知器，便于测试和热插拔。
	manager.eventNotify = notifier
}

// Config 返回当前治理参数快照。
func (manager *Manager) Config() ManagerConfig {
	return manager.config
}

// Snapshot 返回池状态快照。
func (manager *Manager) Snapshot() Snapshot {
	if manager == nil || manager.registry == nil {
		// 空 manager 时返回零值快照。
		return Snapshot{}
	}
	return manager.registry.Snapshot()
}

// Start 启动治理循环：启动预建、周期纠偏、TTL 扫描、refill 触发处理。
func (manager *Manager) Start(ctx context.Context) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// nil context 时兜底，防止 select 空指针。
		normalizedContext = context.Background()
	}
	if _, err := manager.ReconcileNow(normalizedContext, "startup"); err != nil && normalizedContext.Err() == nil {
		// 首次预建失败时继续运行，让后续纠偏重试。
		manager.emitEvent("startup_reconcile_failed")
	}

	ticker := time.NewTicker(manager.config.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-normalizedContext.Done():
			// 上层取消时停止治理循环。
			return normalizedContext.Err()
		case <-manager.refillSignalChannel:
			// 事件触发补池时立即执行一次纠偏。
			_, _ = manager.ReconcileNow(normalizedContext, "refill_signal")
		case <-ticker.C:
			// 周期纠偏用于状态对账与慢路径修复。
			_, _ = manager.ReconcileNow(normalizedContext, "periodic")
		}
	}
}

// ReconcileNow 立即执行一次池治理纠偏。
func (manager *Manager) ReconcileNow(ctx context.Context, trigger string) (ReconcileResult, error) {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 调用方未传 context 时使用 Background。
		normalizedContext = context.Background()
	}
	manager.reconcileMutex.Lock()
	defer manager.reconcileMutex.Unlock()

	result := ReconcileResult{Trigger: strings.TrimSpace(trigger)}
	result.Before = manager.registry.Snapshot()
	result.TTLSweep = manager.ttlReaper.Sweep()
	if result.TTLSweep.Closed > 0 {
		// TTL 回收后触发事件上报，保持 Bridge 侧视图收敛。
		manager.emitEvent("ttl_reaped")
	}

	trimmedIdleCount, trimErr := manager.trimExcessIdle()
	result.TrimmedIdleCount = trimmedIdleCount
	if trimErr != nil {
		result.After = manager.registry.Snapshot()
		return result, trimErr
	}

	requestedTargetIdle, effectiveTargetIdle := manager.computeTargetIdle()
	result.RequestedTargetIdle = requestedTargetIdle
	result.EffectiveTargetIdle = effectiveTargetIdle

	currentSnapshot := manager.registry.Snapshot()
	if currentSnapshot.IdleCount >= effectiveTargetIdle {
		// 当前 idle 已满足目标时仅更新 pending 状态。
		manager.clearPendingIfSatisfied(currentSnapshot.IdleCount)
		result.After = currentSnapshot
		manager.updatePoolMetrics(result.After)
		if result.TTLSweep.FirstError != nil {
			return result, result.TTLSweep.FirstError
		}
		return result, nil
	}

	toOpen := effectiveTargetIdle - currentSnapshot.IdleCount
	result.Produced = manager.producer.OpenBatch(normalizedContext, toOpen, func(tunnel RuntimeTunnel) error {
		added, err := manager.registry.TryAddOpenedAsIdle(manager.nowFn(), tunnel, manager.config.MaxIdle)
		if err != nil {
			return err
		}
		if !added {
			// 入池失败说明已达上限，交由 cleanup 关闭资源。
			return ErrInvalidStateTransition
		}
		return nil
	})
	result.After = manager.registry.Snapshot()
	manager.updatePoolMetrics(result.After)
	manager.clearPendingIfSatisfied(result.After.IdleCount)

	if result.Produced.Opened > 0 || result.Produced.Failed > 0 || result.TTLSweep.Closed > 0 || result.TrimmedIdleCount > 0 {
		// 仅在状态有变化时触发事件上报，减少空转噪声。
		manager.emitEvent("pool_changed")
	}
	if result.Produced.FirstError != nil {
		return result, result.Produced.FirstError
	}
	if result.TTLSweep.FirstError != nil {
		return result, result.TTLSweep.FirstError
	}
	return result, nil
}

// RequestRefill 请求把 idle 平滑补充到目标值（按 max 合并）。
func (manager *Manager) RequestRefill(targetIdle int, reason string) bool {
	effectiveTargetIdle := manager.clampTargetIdle(targetIdle)
	if effectiveTargetIdle <= 0 {
		// 目标容量无效时忽略请求。
		return false
	}
	manager.stateMutex.Lock()
	if manager.suspendRefill {
		// session 非 ACTIVE 时禁止触发补池。
		manager.stateMutex.Unlock()
		return false
	}
	updated := false
	if effectiveTargetIdle > manager.pendingTargetIdle {
		// 多个请求取更高目标，避免并发叠加过冲。
		manager.pendingTargetIdle = effectiveTargetIdle
		manager.pendingReason = strings.TrimSpace(reason)
		updated = true
	}
	manager.stateMutex.Unlock()
	if !updated {
		return false
	}
	// 新目标生效后异步唤醒治理循环。
	select {
	case manager.refillSignalChannel <- struct{}{}:
	default:
		// 信号通道满时说明已有待处理任务，无需重复写入。
	}
	manager.emitEvent("refill_requested")
	return true
}

// AcquireIdle 获取一条 idle tunnel，并在消费后触发补池。
func (manager *Manager) AcquireIdle(at time.Time) (*Record, bool) {
	normalizedTime := at
	if normalizedTime.IsZero() {
		// 未传时间戳时使用当前时间。
		normalizedTime = manager.nowFn()
	}
	record, ok := manager.registry.AcquireIdle(normalizedTime)
	if !ok {
		return nil, false
	}
	// idle 被消费后立刻触发补池。
	manager.RequestRefill(manager.config.MinIdle, "consume")
	manager.updatePoolMetrics(manager.registry.Snapshot())
	manager.emitEvent("idle_acquired")
	return record, true
}

// ActivateIdle 按 tunnelID 将 idle tunnel 激活为 active，并触发补池。
func (manager *Manager) ActivateIdle(tunnelID string) error {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return ErrTunnelNotFound
	}
	if _, err := manager.registry.ActivateIdleByID(manager.nowFn(), normalizedTunnelID); err != nil {
		return err
	}
	// idle 被激活消费后立即触发补池，保持预建水位。
	manager.RequestRefill(manager.config.MinIdle, "consume")
	manager.updatePoolMetrics(manager.registry.Snapshot())
	manager.emitEvent("tunnel_active")
	return nil
}

// MarkActive 把 reserved tunnel 标记为 active。
func (manager *Manager) MarkActive(tunnelID string) error {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 tunnelID 直接返回 not found。
		return ErrTunnelNotFound
	}
	if err := manager.registry.MarkActive(manager.nowFn(), normalizedTunnelID); err != nil {
		return err
	}
	manager.updatePoolMetrics(manager.registry.Snapshot())
	manager.emitEvent("tunnel_active")
	return nil
}

// CloseAndRemove 把 tunnel 收敛到 closed 并摘除记录。
func (manager *Manager) CloseAndRemove(tunnelID string) error {
	return manager.closeAndRemove(tunnelID, true, "close")
}

func (manager *Manager) closeAndRemove(tunnelID string, refill bool, refillReason string) error {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 tunnelID 直接返回 not found。
		return ErrTunnelNotFound
	}
	record, exists := manager.registry.Get(normalizedTunnelID)
	if !exists {
		return ErrTunnelNotFound
	}
	if err := manager.registry.MarkClosing(manager.nowFn(), normalizedTunnelID); err != nil {
		return err
	}
	if record.Tunnel != nil {
		if err := record.Tunnel.Close(); err != nil {
			// 关闭失败时仍继续收敛到 closed，避免状态滞留。
			_ = manager.registry.MarkClosed(manager.nowFn(), normalizedTunnelID)
			if _, removeErr := manager.reaper.RemoveClosedOnly(normalizedTunnelID); removeErr != nil {
				return errors.Join(err, removeErr)
			}
			if refill {
				manager.RequestRefill(manager.config.MinIdle, refillReason)
			}
			manager.updatePoolMetrics(manager.registry.Snapshot())
			manager.emitEvent("tunnel_closed")
			return err
		}
	}
	if err := manager.registry.MarkClosed(manager.nowFn(), normalizedTunnelID); err != nil {
		return err
	}
	if _, err := manager.reaper.RemoveClosedOnly(normalizedTunnelID); err != nil {
		return err
	}
	if refill {
		manager.RequestRefill(manager.config.MinIdle, refillReason)
	}
	manager.updatePoolMetrics(manager.registry.Snapshot())
	manager.emitEvent("tunnel_closed")
	return nil
}

// MarkBrokenAndRemove 把异常 tunnel 标记 broken 并摘除。
func (manager *Manager) MarkBrokenAndRemove(tunnelID string, reason string) error {
	if _, err := manager.reaper.MarkBrokenAndRemove(strings.TrimSpace(tunnelID), strings.TrimSpace(reason)); err != nil {
		return err
	}
	// broken 摘除后触发补池，保持 idle 水位。
	manager.RequestRefill(manager.config.MinIdle, "broken")
	manager.updatePoolMetrics(manager.registry.Snapshot())
	manager.emitEvent("tunnel_broken")
	return nil
}

// trimExcessIdle 把超出 maxIdle 的 idle tunnel 回收到上限。
func (manager *Manager) trimExcessIdle() (int, error) {
	snapshot := manager.registry.Snapshot()
	excessIdleCount := snapshot.IdleCount - manager.config.MaxIdle
	if excessIdleCount <= 0 {
		// 未超过上限时无需回收。
		return 0, nil
	}
	idleTunnelIDs := manager.registry.IdleIDs(excessIdleCount)
	trimmedIdleCount := 0
	for _, tunnelID := range idleTunnelIDs {
		// 超上限回收不触发 refill，避免边回收边补建。
		if err := manager.closeAndRemove(tunnelID, false, "trim"); err != nil {
			// 单条回收失败立即返回，避免继续扩大异常面。
			return trimmedIdleCount, err
		}
		trimmedIdleCount++
	}
	return trimmedIdleCount, nil
}

// computeTargetIdle 计算本轮纠偏目标容量。
func (manager *Manager) computeTargetIdle() (int, int) {
	manager.stateMutex.Lock()
	defer manager.stateMutex.Unlock()
	if manager.suspendRefill {
		// session DRAINING/STALE 时暂停补池。
		return 0, 0
	}
	requestedTargetIdle := manager.pendingTargetIdle
	if requestedTargetIdle < manager.config.MinIdle {
		// 未显式请求时至少维持 min_idle。
		requestedTargetIdle = manager.config.MinIdle
	}
	effectiveTargetIdle := manager.clampTargetIdle(requestedTargetIdle)
	return requestedTargetIdle, effectiveTargetIdle
}

// clearPendingIfSatisfied 当 idle 达到 pending 目标后清空挂起请求。
func (manager *Manager) clearPendingIfSatisfied(currentIdleCount int) {
	manager.stateMutex.Lock()
	defer manager.stateMutex.Unlock()
	if manager.pendingTargetIdle <= 0 {
		return
	}
	if currentIdleCount >= manager.pendingTargetIdle {
		// 已达目标后清空 pending，下一轮按 min_idle 维持。
		manager.pendingTargetIdle = 0
		manager.pendingReason = ""
	}
}

// clampTargetIdle 将目标容量限制在 [min_idle, max_idle]。
func (manager *Manager) clampTargetIdle(targetIdle int) int {
	effectiveTargetIdle := targetIdle
	if effectiveTargetIdle < manager.config.MinIdle {
		// 小于下限时提升到 min_idle。
		effectiveTargetIdle = manager.config.MinIdle
	}
	if effectiveTargetIdle > manager.config.MaxIdle {
		// 大于上限时截断到 max_idle。
		effectiveTargetIdle = manager.config.MaxIdle
	}
	return effectiveTargetIdle
}

// HandleSessionState 处理 session 状态变化触发的 idle 回收和补池开关。
func (manager *Manager) HandleSessionState(state string) (int, error) {
	if manager == nil {
		return 0, nil
	}
	normalizedState := strings.ToUpper(strings.TrimSpace(state))
	switch normalizedState {
	case SessionStateDraining, SessionStateStale:
		// 与 ReconcileNow 串行，避免 DRAINING/STALE 期间并发补池重新引入 idle。
		manager.reconcileMutex.Lock()
		defer manager.reconcileMutex.Unlock()

		manager.stateMutex.Lock()
		// 进入 DRAINING/STALE 后暂停补池，防止回收后立即补建。
		manager.suspendRefill = true
		manager.pendingTargetIdle = 0
		manager.pendingReason = strings.ToLower(normalizedState)
		manager.stateMutex.Unlock()

		recycledIdleCount, recycleErr := manager.recycleAllIdleWithoutRefill()
		manager.updatePoolMetrics(manager.registry.Snapshot())
		manager.emitEvent("session_" + strings.ToLower(normalizedState))
		return recycledIdleCount, recycleErr
	case SessionStateActive:
		// 与 ReconcileNow 串行，确保恢复补池与上一轮 drain 行为顺序一致。
		manager.reconcileMutex.Lock()
		manager.stateMutex.Lock()
		wasSuspended := manager.suspendRefill
		manager.suspendRefill = false
		manager.stateMutex.Unlock()
		manager.reconcileMutex.Unlock()
		if wasSuspended {
			// 恢复 ACTIVE 后按 min_idle 重新补池。
			manager.RequestRefill(manager.config.MinIdle, "session_active")
		}
		manager.emitEvent("session_active")
		return 0, nil
	default:
		return 0, nil
	}
}

func (manager *Manager) recycleAllIdleWithoutRefill() (int, error) {
	snapshot := manager.registry.Snapshot()
	if snapshot.IdleCount <= 0 {
		return 0, nil
	}
	idleTunnelIDs := manager.registry.IdleIDs(snapshot.IdleCount)
	recycledIdleCount := 0
	for _, tunnelID := range idleTunnelIDs {
		if err := manager.closeAndRemove(tunnelID, false, "session_state"); err != nil {
			return recycledIdleCount, err
		}
		recycledIdleCount++
	}
	return recycledIdleCount, nil
}

// emitEvent 向上层通知一次池状态变化事件。
func (manager *Manager) emitEvent(trigger string) {
	manager.stateMutex.Lock()
	notifier := manager.eventNotify
	manager.stateMutex.Unlock()
	if notifier == nil {
		// 未配置通知器时静默跳过。
		return
	}
	normalizedTrigger := strings.TrimSpace(trigger)
	if normalizedTrigger == "" {
		normalizedTrigger = "pool_event"
	}
	notifier.NotifyEvent(normalizedTrigger)
}

// updatePoolMetrics 把当前 pool 快照同步到观测指标。
func (manager *Manager) updatePoolMetrics(snapshot Snapshot) {
	if manager == nil || manager.metrics == nil {
		return
	}
	// tunnel pool 观测当前只关心 idle/active 两个核心水位。
	manager.metrics.SetAgentTunnelPoolCounts(snapshot.IdleCount, snapshot.ActiveCount)
}
