package registry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TunnelState 定义 Bridge 侧 tunnel 生命周期状态。
type TunnelState string

const (
	TunnelStateIdle     TunnelState = "idle"
	TunnelStateReserved TunnelState = "reserved"
	TunnelStateActive   TunnelState = "active"
	TunnelStateClosed   TunnelState = "closed"
	TunnelStateBroken   TunnelState = "broken"
)

var (
	// ErrTunnelNotFound 表示 tunnel 不存在。
	ErrTunnelNotFound = errors.New("tunnel not found")
	// ErrDuplicateTunnelID 表示 tunnel ID 已存在。
	ErrDuplicateTunnelID = errors.New("duplicate tunnel id")
	// ErrInvalidTunnelStateTransition 表示 tunnel 状态迁移非法。
	ErrInvalidTunnelStateTransition = errors.New("invalid tunnel state transition")
	// ErrTunnelRegistryDependencyMissing 表示注册表关键依赖缺失。
	ErrTunnelRegistryDependencyMissing = errors.New("tunnel registry dependency missing")
)

// RuntimeTunnel 定义 Bridge connector path 需要的最小 tunnel 能力。
type RuntimeTunnel interface {
	ID() string
	ReadPayload(ctx context.Context) (pb.StreamPayload, error)
	WritePayload(ctx context.Context, payload pb.StreamPayload) error
	Close() error
}

// TunnelRuntime 描述 tunnel 在 Bridge 侧的运行态记录。
type TunnelRuntime struct {
	Tunnel      RuntimeTunnel
	TunnelID    string
	ConnectorID string
	SessionID   string
	TrafficID   string
	State       TunnelState
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TunnelSnapshot 描述当前 tunnel registry 的状态统计。
type TunnelSnapshot struct {
	IdleCount     int
	ReservedCount int
	ActiveCount   int
	ClosedCount   int
	BrokenCount   int
	TotalCount    int
	UpdatedAt     time.Time
}

// TunnelRegistry tracks bridge-side tunnel state.
type TunnelRegistry struct {
	mutex sync.RWMutex

	byTunnelID      map[string]*TunnelRuntime
	idleByConnector map[string][]string
	updatedAt       time.Time
}

// NewTunnelRegistry 创建 Bridge 侧 tunnel 运行态注册表。
func NewTunnelRegistry() *TunnelRegistry {
	return &TunnelRegistry{
		byTunnelID:      make(map[string]*TunnelRuntime),
		idleByConnector: make(map[string][]string),
	}
}

// UpsertIdle 注册一条新的 idle tunnel。
func (registry *TunnelRegistry) UpsertIdle(now time.Time, connectorID string, sessionID string, tunnel RuntimeTunnel) (TunnelRuntime, error) {
	if registry == nil {
		return TunnelRuntime{}, ErrTunnelRegistryDependencyMissing
	}
	if tunnel == nil {
		return TunnelRuntime{}, ErrTunnelRegistryDependencyMissing
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return TunnelRuntime{}, ErrTunnelRegistryDependencyMissing
	}
	normalizedTunnelID := strings.TrimSpace(tunnel.ID())
	if normalizedTunnelID == "" {
		return TunnelRuntime{}, ErrTunnelRegistryDependencyMissing
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.byTunnelID[normalizedTunnelID]; exists {
		return TunnelRuntime{}, ErrDuplicateTunnelID
	}
	runtime := &TunnelRuntime{
		Tunnel:      tunnel,
		TunnelID:    normalizedTunnelID,
		ConnectorID: normalizedConnectorID,
		SessionID:   strings.TrimSpace(sessionID),
		State:       TunnelStateIdle,
		CreatedAt:   normalizedNow,
		UpdatedAt:   normalizedNow,
	}
	registry.byTunnelID[normalizedTunnelID] = runtime
	registry.idleByConnector[normalizedConnectorID] = append(registry.idleByConnector[normalizedConnectorID], normalizedTunnelID)
	registry.updatedAt = normalizedNow
	return cloneTunnelRuntime(runtime), nil
}

// AcquireIdle 按 FIFO 获取 connector 对应的一条 idle tunnel，并切换为 reserved。
func (registry *TunnelRegistry) AcquireIdle(now time.Time, connectorID string) (TunnelRuntime, bool) {
	if registry == nil {
		return TunnelRuntime{}, false
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return TunnelRuntime{}, false
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	queue := registry.idleByConnector[normalizedConnectorID]
	for len(queue) > 0 {
		tunnelID := queue[0]
		queue = queue[1:]
		runtime, exists := registry.byTunnelID[tunnelID]
		if !exists {
			continue
		}
		if runtime.State != TunnelStateIdle {
			continue
		}
		runtime.State = TunnelStateReserved
		runtime.UpdatedAt = normalizedNow
		registry.idleByConnector[normalizedConnectorID] = queue
		registry.updatedAt = normalizedNow
		return cloneTunnelRuntime(runtime), true
	}
	registry.idleByConnector[normalizedConnectorID] = queue
	return TunnelRuntime{}, false
}

// MarkActive 把 reserved tunnel 切换为 active。
func (registry *TunnelRegistry) MarkActive(now time.Time, tunnelID string, trafficID string) error {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return ErrTunnelNotFound
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	runtime, exists := registry.byTunnelID[normalizedTunnelID]
	if !exists {
		return ErrTunnelNotFound
	}
	if runtime.State != TunnelStateReserved {
		return ErrInvalidTunnelStateTransition
	}
	runtime.State = TunnelStateActive
	runtime.TrafficID = strings.TrimSpace(trafficID)
	runtime.UpdatedAt = normalizedNow
	registry.updatedAt = normalizedNow
	return nil
}

// MarkClosed 把 reserved/active tunnel 切换为 closed。
func (registry *TunnelRegistry) MarkClosed(now time.Time, tunnelID string) error {
	return registry.transition(now, tunnelID, TunnelStateClosed, "")
}

// MarkBroken 把 idle/reserved/active tunnel 切换为 broken。
func (registry *TunnelRegistry) MarkBroken(now time.Time, tunnelID string, message string) error {
	return registry.transition(now, tunnelID, TunnelStateBroken, message)
}

// RemoveTerminal 删除 closed/broken 终态 tunnel 记录。
func (registry *TunnelRegistry) RemoveTerminal(tunnelID string) (TunnelRuntime, error) {
	if registry == nil {
		return TunnelRuntime{}, ErrTunnelRegistryDependencyMissing
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return TunnelRuntime{}, ErrTunnelNotFound
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	runtime, exists := registry.byTunnelID[normalizedTunnelID]
	if !exists {
		return TunnelRuntime{}, ErrTunnelNotFound
	}
	if runtime.State != TunnelStateClosed && runtime.State != TunnelStateBroken {
		return TunnelRuntime{}, ErrInvalidTunnelStateTransition
	}
	delete(registry.byTunnelID, normalizedTunnelID)
	registry.removeIdleLocked(runtime.ConnectorID, normalizedTunnelID)
	registry.updatedAt = time.Now().UTC()
	return cloneTunnelRuntime(runtime), nil
}

// Get 返回指定 tunnel 的运行态快照。
func (registry *TunnelRegistry) Get(tunnelID string) (TunnelRuntime, bool) {
	if registry == nil {
		return TunnelRuntime{}, false
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return TunnelRuntime{}, false
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	runtime, exists := registry.byTunnelID[normalizedTunnelID]
	if !exists {
		return TunnelRuntime{}, false
	}
	return cloneTunnelRuntime(runtime), true
}

// Snapshot 返回 tunnel registry 统计快照。
func (registry *TunnelRegistry) Snapshot() TunnelSnapshot {
	if registry == nil {
		return TunnelSnapshot{}
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	snapshot := TunnelSnapshot{
		TotalCount: len(registry.byTunnelID),
		UpdatedAt:  registry.updatedAt,
	}
	for _, runtime := range registry.byTunnelID {
		switch runtime.State {
		case TunnelStateIdle:
			snapshot.IdleCount++
		case TunnelStateReserved:
			snapshot.ReservedCount++
		case TunnelStateActive:
			snapshot.ActiveCount++
		case TunnelStateClosed:
			snapshot.ClosedCount++
		case TunnelStateBroken:
			snapshot.BrokenCount++
		}
	}
	return snapshot
}

// List 返回当前全部 tunnel 运行态快照。
func (registry *TunnelRegistry) List() []TunnelRuntime {
	if registry == nil {
		return []TunnelRuntime{}
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	result := make([]TunnelRuntime, 0, len(registry.byTunnelID))
	for _, runtime := range registry.byTunnelID {
		// 返回副本，避免调用方篡改注册表内部状态。
		result = append(result, cloneTunnelRuntime(runtime))
	}
	return result
}

// PurgeBySession 按 session 摘除 tunnel 记录并关闭底层连接。
func (registry *TunnelRegistry) PurgeBySession(now time.Time, sessionID string, reason string) []TunnelRuntime {
	if registry == nil {
		return nil
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" {
		return nil
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	normalizedReason := strings.TrimSpace(reason)

	registry.mutex.Lock()
	purged := make([]TunnelRuntime, 0)
	for tunnelID, runtime := range registry.byTunnelID {
		if strings.TrimSpace(runtime.SessionID) != normalizedSessionID {
			continue
		}
		// 先从 idle 索引和主索引移除，避免后续被再次分配。
		registry.removeIdleLocked(runtime.ConnectorID, tunnelID)
		if runtime.State != TunnelStateClosed && runtime.State != TunnelStateBroken {
			runtime.State = TunnelStateBroken
		}
		if normalizedReason != "" && strings.TrimSpace(runtime.LastError) == "" {
			runtime.LastError = normalizedReason
		}
		runtime.UpdatedAt = normalizedNow
		purged = append(purged, cloneTunnelRuntime(runtime))
		delete(registry.byTunnelID, tunnelID)
	}
	if len(purged) > 0 {
		registry.updatedAt = normalizedNow
	}
	registry.mutex.Unlock()

	// 在锁外关闭底层 tunnel，避免 IO 阻塞影响注册表写路径。
	for _, runtime := range purged {
		if runtime.Tunnel == nil {
			continue
		}
		_ = runtime.Tunnel.Close()
	}
	return purged
}

func (registry *TunnelRegistry) transition(now time.Time, tunnelID string, target TunnelState, message string) error {
	if registry == nil {
		return ErrTunnelRegistryDependencyMissing
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return ErrTunnelNotFound
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	runtime, exists := registry.byTunnelID[normalizedTunnelID]
	if !exists {
		return ErrTunnelNotFound
	}
	if !isValidTunnelTransition(runtime.State, target) {
		return ErrInvalidTunnelStateTransition
	}
	if runtime.State == TunnelStateIdle {
		registry.removeIdleLocked(runtime.ConnectorID, runtime.TunnelID)
	}
	runtime.State = target
	runtime.UpdatedAt = normalizedNow
	if target == TunnelStateBroken {
		runtime.LastError = strings.TrimSpace(message)
	}
	registry.updatedAt = normalizedNow
	return nil
}

func (registry *TunnelRegistry) removeIdleLocked(connectorID string, tunnelID string) {
	queue := registry.idleByConnector[connectorID]
	if len(queue) == 0 {
		return
	}
	filtered := queue[:0]
	for _, queuedTunnelID := range queue {
		if queuedTunnelID == tunnelID {
			continue
		}
		filtered = append(filtered, queuedTunnelID)
	}
	registry.idleByConnector[connectorID] = filtered
}

func isValidTunnelTransition(current TunnelState, target TunnelState) bool {
	switch current {
	case TunnelStateIdle:
		return target == TunnelStateReserved || target == TunnelStateBroken
	case TunnelStateReserved:
		return target == TunnelStateActive || target == TunnelStateClosed || target == TunnelStateBroken
	case TunnelStateActive:
		return target == TunnelStateClosed || target == TunnelStateBroken
	default:
		return false
	}
}

func cloneTunnelRuntime(runtime *TunnelRuntime) TunnelRuntime {
	if runtime == nil {
		return TunnelRuntime{}
	}
	return TunnelRuntime{
		Tunnel:      runtime.Tunnel,
		TunnelID:    runtime.TunnelID,
		ConnectorID: runtime.ConnectorID,
		SessionID:   runtime.SessionID,
		TrafficID:   runtime.TrafficID,
		State:       runtime.State,
		LastError:   runtime.LastError,
		CreatedAt:   runtime.CreatedAt,
		UpdatedAt:   runtime.UpdatedAt,
	}
}
