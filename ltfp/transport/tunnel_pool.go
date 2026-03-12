package transport

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TunnelPool 定义 tunnel 池最小接口。
type TunnelPool interface {
	PutIdle(tunnel Tunnel) error
	Acquire(ctx context.Context) (Tunnel, error)
	Remove(tunnelID string) error

	IdleCount() int
	InUseCount() int
}

// TunnelPoolConfig 描述 tunnel 池容量和超时参数。
type TunnelPoolConfig struct {
	MinIdleTunnels int
	MaxIdleTunnels int
	IdleTunnelTTL  time.Duration
	AcquireTimeout time.Duration
}

// NormalizeAndValidate 归一化并校验配置。
func (config TunnelPoolConfig) NormalizeAndValidate() (TunnelPoolConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MinIdleTunnels < 0 {
		// 下限容量不能为负数，负值统一归零。
		normalizedConfig.MinIdleTunnels = 0
	}
	if normalizedConfig.MaxIdleTunnels <= 0 {
		// 未设置上限时采用默认上限，避免池无限膨胀。
		normalizedConfig.MaxIdleTunnels = 1024
	}
	if normalizedConfig.MinIdleTunnels > normalizedConfig.MaxIdleTunnels {
		// min 不允许超过 max，避免形成矛盾配置。
		return TunnelPoolConfig{}, fmt.Errorf(
			"normalize tunnel pool config: %w: min_idle=%d max_idle=%d",
			ErrInvalidArgument,
			normalizedConfig.MinIdleTunnels,
			normalizedConfig.MaxIdleTunnels,
		)
	}
	if normalizedConfig.IdleTunnelTTL < 0 {
		// TTL 不能为负数。
		return TunnelPoolConfig{}, fmt.Errorf(
			"normalize tunnel pool config: %w: idle_tunnel_ttl=%s",
			ErrInvalidArgument,
			normalizedConfig.IdleTunnelTTL,
		)
	}
	if normalizedConfig.AcquireTimeout < 0 {
		// acquire_timeout 不能为负数。
		return TunnelPoolConfig{}, fmt.Errorf(
			"normalize tunnel pool config: %w: acquire_timeout=%s",
			ErrInvalidArgument,
			normalizedConfig.AcquireTimeout,
		)
	}
	return normalizedConfig, nil
}

// DefaultTunnelPoolConfig 返回默认池配置。
func DefaultTunnelPoolConfig() TunnelPoolConfig {
	return TunnelPoolConfig{
		MinIdleTunnels: 0,
		MaxIdleTunnels: 1024,
		IdleTunnelTTL:  0,
		AcquireTimeout: 0,
	}
}

// InMemoryTunnelPool 是线程安全的内存实现。
type InMemoryTunnelPool struct {
	mutex sync.Mutex

	idleTunnelsByID  map[string]Tunnel
	inUseTunnelsByID map[string]Tunnel
	idleTunnelOrder  []string
	idleInsertedAt   map[string]time.Time
	config           TunnelPoolConfig

	notifyChannel chan struct{}
}

var _ TunnelPool = (*InMemoryTunnelPool)(nil)

// NewInMemoryTunnelPool 创建默认的内存 tunnel 池。
func NewInMemoryTunnelPool() *InMemoryTunnelPool {
	defaultConfig := DefaultTunnelPoolConfig()
	return NewInMemoryTunnelPoolWithConfig(defaultConfig)
}

// NewInMemoryTunnelPoolWithConfig 使用指定配置创建内存 tunnel 池。
func NewInMemoryTunnelPoolWithConfig(config TunnelPoolConfig) *InMemoryTunnelPool {
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		// 配置非法时回退默认值，避免构造阶段 panic。
		normalizedConfig = DefaultTunnelPoolConfig()
	}
	return &InMemoryTunnelPool{
		idleTunnelsByID:  make(map[string]Tunnel),
		inUseTunnelsByID: make(map[string]Tunnel),
		idleTunnelOrder:  make([]string, 0),
		idleInsertedAt:   make(map[string]time.Time),
		config:           normalizedConfig,
		notifyChannel:    make(chan struct{}),
	}
}

// Config 返回当前池配置快照。
func (pool *InMemoryTunnelPool) Config() TunnelPoolConfig {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	// 返回结构体副本，避免外部直接修改内部配置。
	return pool.config
}

// PutIdle 将 tunnel 放入空闲池。
func (pool *InMemoryTunnelPool) PutIdle(tunnel Tunnel) error {
	if tunnel == nil {
		// nil tunnel 无法入池，直接返回参数错误。
		return fmt.Errorf("put idle tunnel: %w", ErrInvalidArgument)
	}
	if tunnel.State() != TunnelStateIdle {
		// 只允许 idle 态入池，避免脏状态污染池子。
		return fmt.Errorf("put idle tunnel: %w: current_state=%s", ErrStateTransition, tunnel.State())
	}
	tunnelID := strings.TrimSpace(tunnel.ID())
	if tunnelID == "" {
		// tunnelId 为空时无法建立稳定索引。
		return fmt.Errorf("put idle tunnel: %w: empty tunnel id", ErrInvalidArgument)
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	if _, exists := pool.inUseTunnelsByID[tunnelID]; exists {
		// in-use tunnel 禁止重复回灌到 idle 池。
		return fmt.Errorf("put idle tunnel: %w: tunnel_id=%s", ErrTunnelInUse, tunnelID)
	}
	_, existsInIdlePool := pool.idleTunnelsByID[tunnelID]
	if !existsInIdlePool && len(pool.idleTunnelsByID) >= pool.config.MaxIdleTunnels {
		// 超过 max_idle_tunnels 时拒绝继续入池，避免池无限增长。
		return fmt.Errorf(
			"put idle tunnel: %w: idle_count=%d max_idle_tunnels=%d",
			ErrPoolExhausted,
			len(pool.idleTunnelsByID),
			pool.config.MaxIdleTunnels,
		)
	}
	if !existsInIdlePool {
		// 仅在确认可入池后才维护顺序队列，避免产生脏索引。
		pool.idleTunnelOrder = append(pool.idleTunnelOrder, tunnelID)
	}
	pool.idleTunnelsByID[tunnelID] = tunnel
	pool.idleInsertedAt[tunnelID] = time.Now().UTC()
	if !existsInIdlePool {
		// 仅在 idle 数量增加时广播可用事件，避免重复唤醒。
		pool.notifyLocked()
	}
	return nil
}

// Acquire 获取一条可用 idle tunnel，必要时阻塞等待。
func (pool *InMemoryTunnelPool) Acquire(ctx context.Context) (Tunnel, error) {
	if ctx == nil {
		// 兜底使用 Background，避免 nil context 导致 panic。
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && pool.config.AcquireTimeout > 0 {
		// 当调用方未指定 deadline 时，应用池级 acquire_timeout 默认值。
		timeoutContext, cancelTimeout := context.WithTimeout(ctx, pool.config.AcquireTimeout)
		defer cancelTimeout()
		ctx = timeoutContext
	}
	for {
		pool.mutex.Lock()
		tunnel, exists := pool.popIdleLocked()
		if exists {
			// 出队成功后转入 in-use 集合，避免重复分配。
			pool.inUseTunnelsByID[tunnel.ID()] = tunnel
			pool.mutex.Unlock()
			return tunnel, nil
		}
		notifyChannel := pool.notifyChannel
		pool.mutex.Unlock()

		select {
		case <-ctx.Done():
			// 等待被取消时返回上下文错误，让上层决定重试策略。
			return nil, fmt.Errorf("acquire tunnel: %w", ctx.Err())
		case <-notifyChannel:
			// 收到广播后重新检查 idle 池。
		}
	}
}

// Remove 从池中移除指定 tunnel。
func (pool *InMemoryTunnelPool) Remove(tunnelID string) error {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 ID 无法执行删除操作。
		return fmt.Errorf("remove tunnel: %w: empty tunnel id", ErrInvalidArgument)
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	removedFromIdle := false
	if _, exists := pool.idleTunnelsByID[normalizedTunnelID]; exists {
		// 先删 idle map，再清理顺序切片。
		delete(pool.idleTunnelsByID, normalizedTunnelID)
		delete(pool.idleInsertedAt, normalizedTunnelID)
		pool.removeIdleOrderLocked(normalizedTunnelID)
		removedFromIdle = true
	}
	removedFromInUse := false
	if _, exists := pool.inUseTunnelsByID[normalizedTunnelID]; exists {
		// in-use map 删除后该 tunnel 不可再被追踪。
		delete(pool.inUseTunnelsByID, normalizedTunnelID)
		removedFromInUse = true
	}
	if !removedFromIdle && !removedFromInUse {
		// 两个集合都未命中时返回 not found。
		return fmt.Errorf("remove tunnel: %w: tunnel_id=%s", ErrTunnelNotFound, normalizedTunnelID)
	}
	return nil
}

// EvictExpiredIdle 按 idle_tunnel_ttl 清理过期空闲 tunnel。
func (pool *InMemoryTunnelPool) EvictExpiredIdle(now time.Time) []string {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	if pool.config.IdleTunnelTTL <= 0 {
		// 未配置 TTL 时不执行清理。
		return nil
	}
	evictedTunnelIDs := make([]string, 0)
	for tunnelID := range pool.idleTunnelsByID {
		insertedAt := pool.idleInsertedAt[tunnelID]
		if insertedAt.IsZero() {
			// 无入池时间时按最保守策略清理，避免僵尸连接常驻。
			delete(pool.idleTunnelsByID, tunnelID)
			delete(pool.idleInsertedAt, tunnelID)
			pool.removeIdleOrderLocked(tunnelID)
			evictedTunnelIDs = append(evictedTunnelIDs, tunnelID)
			continue
		}
		if now.Sub(insertedAt) < pool.config.IdleTunnelTTL {
			// 尚未超过 TTL 时保留该 tunnel。
			continue
		}
		// 超过 TTL 的 idle tunnel 直接移除。
		delete(pool.idleTunnelsByID, tunnelID)
		delete(pool.idleInsertedAt, tunnelID)
		pool.removeIdleOrderLocked(tunnelID)
		evictedTunnelIDs = append(evictedTunnelIDs, tunnelID)
	}
	return evictedTunnelIDs
}

// IdleCount 返回当前 idle tunnel 数量。
func (pool *InMemoryTunnelPool) IdleCount() int {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	// idle map 长度即当前空闲容量。
	return len(pool.idleTunnelsByID)
}

// InUseCount 返回当前 in-use tunnel 数量。
func (pool *InMemoryTunnelPool) InUseCount() int {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	// in-use map 长度即当前占用容量。
	return len(pool.inUseTunnelsByID)
}

// popIdleLocked 从 idle 队列中弹出第一条仍然存在的 tunnel。
func (pool *InMemoryTunnelPool) popIdleLocked() (Tunnel, bool) {
	for len(pool.idleTunnelOrder) > 0 {
		tunnelID := pool.idleTunnelOrder[0]
		// 头删切片，维持 FIFO 出队顺序。
		pool.idleTunnelOrder = pool.idleTunnelOrder[1:]
		tunnel, exists := pool.idleTunnelsByID[tunnelID]
		if !exists {
			// 若 map 中已不存在，说明是历史脏索引，继续下一个。
			continue
		}
		delete(pool.idleTunnelsByID, tunnelID)
		delete(pool.idleInsertedAt, tunnelID)
		return tunnel, true
	}
	return nil, false
}

// removeIdleOrderLocked 清理 idle 顺序切片中的指定 tunnelId。
func (pool *InMemoryTunnelPool) removeIdleOrderLocked(tunnelID string) {
	for index := range pool.idleTunnelOrder {
		if pool.idleTunnelOrder[index] != tunnelID {
			continue
		}
		// 命中后做一次切片拼接并立即返回，避免重复删除。
		pool.idleTunnelOrder = append(pool.idleTunnelOrder[:index], pool.idleTunnelOrder[index+1:]...)
		return
	}
}

// notifyLocked 广播通知等待中的 Acquire 调用方重新检查 idle 池。
func (pool *InMemoryTunnelPool) notifyLocked() {
	close(pool.notifyChannel)
	pool.notifyChannel = make(chan struct{})
}
