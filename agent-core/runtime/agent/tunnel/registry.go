package tunnel

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// State 表示 tunnel 在池中的生命周期状态。
type State string

const (
	StateOpening  State = "opening"
	StateIdle     State = "idle"
	StateReserved State = "reserved"
	StateActive   State = "active"
	StateClosing  State = "closing"
	StateClosed   State = "closed"
	StateBroken   State = "broken"
)

var (
	// ErrInvalidStateTransition 表示状态迁移不合法。
	ErrInvalidStateTransition = errors.New("invalid tunnel state transition")
	// ErrTunnelNotFound 表示 tunnel 不存在。
	ErrTunnelNotFound = errors.New("tunnel not found")
	// ErrDuplicateTunnelID 表示 tunnel ID 已存在。
	ErrDuplicateTunnelID = errors.New("duplicate tunnel id")
)

// RuntimeTunnel 定义 manager 需要的最小 tunnel 能力。
type RuntimeTunnel interface {
	// ID 返回 tunnel 唯一标识。
	ID() string
	// Close 关闭底层 tunnel 资源。
	Close() error
}

// Record 描述单条 tunnel 的运行态记录。
type Record struct {
	Tunnel         RuntimeTunnel
	TunnelID       string
	State          State
	CreatedAt      time.Time
	UpdatedAt      time.Time
	IdleSince      time.Time
	LastError      string
	StateChangeSeq uint64
}

// Snapshot 描述当前池状态快照。
type Snapshot struct {
	OpeningCount  int
	IdleCount     int
	ReservedCount int
	ActiveCount   int
	ClosingCount  int
	ClosedCount   int
	BrokenCount   int
	TotalCount    int
	UpdatedAt     time.Time
}

// Registry 保存 tunnel 运行态与状态索引。
type Registry struct {
	mu             sync.RWMutex
	records        map[string]*Record
	idleOrder      []string
	updatedAt      time.Time
	stateChangeSeq uint64
}

// NewRegistry 创建 tunnel registry。
func NewRegistry() *Registry {
	return &Registry{
		records:   make(map[string]*Record),
		idleOrder: make([]string, 0),
	}
}

// TryAddOpenedAsIdle 将新建 tunnel 注册为 opening 并立即切换到 idle。
func (registry *Registry) TryAddOpenedAsIdle(now time.Time, tunnel RuntimeTunnel, maxIdle int) (bool, error) {
	if tunnel == nil {
		// nil tunnel 无法注册。
		return false, ErrTunnelNotFound
	}
	tunnelID := strings.TrimSpace(tunnel.ID())
	if tunnelID == "" {
		// 空 tunnelID 无法建立索引。
		return false, ErrTunnelNotFound
	}
	if maxIdle <= 0 {
		// maxIdle 非法时视为不允许入池。
		return false, nil
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.records[tunnelID]; exists {
		// 同 ID tunnel 重复注册属于逻辑错误。
		return false, ErrDuplicateTunnelID
	}
	if registry.countByStateLocked(StateIdle) >= maxIdle {
		// idle 已到上限时拒绝入池。
		return false, nil
	}

	record := &Record{
		Tunnel:    tunnel,
		TunnelID:  tunnelID,
		State:     StateOpening,
		CreatedAt: now,
		UpdatedAt: now,
	}
	registry.stateChangeSeq++
	record.StateChangeSeq = registry.stateChangeSeq
	registry.records[tunnelID] = record

	// opening -> idle 是创建成功后的标准路径。
	record.State = StateIdle
	record.UpdatedAt = now
	record.IdleSince = now
	registry.stateChangeSeq++
	record.StateChangeSeq = registry.stateChangeSeq
	registry.idleOrder = append(registry.idleOrder, tunnelID)
	registry.updatedAt = now
	return true, nil
}

// AcquireIdle 按 FIFO 获取一条 idle tunnel 并切换到 reserved。
func (registry *Registry) AcquireIdle(now time.Time) (*Record, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for len(registry.idleOrder) > 0 {
		tunnelID := registry.idleOrder[0]
		registry.idleOrder = registry.idleOrder[1:]
		record, exists := registry.records[tunnelID]
		if !exists {
			// 记录已删除时继续取下一条。
			continue
		}
		if record.State != StateIdle {
			// 非 idle 记录属于脏顺序索引，跳过。
			continue
		}
		// idle -> reserved，避免被重复分配。
		record.State = StateReserved
		record.UpdatedAt = now
		record.IdleSince = time.Time{}
		registry.stateChangeSeq++
		record.StateChangeSeq = registry.stateChangeSeq
		registry.updatedAt = now
		return cloneRecord(record), true
	}
	return nil, false
}

// MarkActive 将 reserved tunnel 切换为 active。
func (registry *Registry) MarkActive(now time.Time, tunnelID string) error {
	return registry.transition(now, strings.TrimSpace(tunnelID), StateActive, "")
}

// MarkClosing 将 idle/reserved/active tunnel 切换为 closing。
func (registry *Registry) MarkClosing(now time.Time, tunnelID string) error {
	return registry.transition(now, strings.TrimSpace(tunnelID), StateClosing, "")
}

// MarkClosed 将 closing tunnel 切换为 closed。
func (registry *Registry) MarkClosed(now time.Time, tunnelID string) error {
	return registry.transition(now, strings.TrimSpace(tunnelID), StateClosed, "")
}

// MarkBroken 将 tunnel 标记为 broken，并记录错误描述。
func (registry *Registry) MarkBroken(now time.Time, tunnelID string, message string) error {
	return registry.transition(now, strings.TrimSpace(tunnelID), StateBroken, message)
}

// RemoveTerminal 删除终态（closed/broken）tunnel 记录。
func (registry *Registry) RemoveTerminal(tunnelID string) (*Record, error) {
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		// 空 tunnelID 直接返回 not found。
		return nil, ErrTunnelNotFound
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, exists := registry.records[normalizedTunnelID]
	if !exists {
		return nil, ErrTunnelNotFound
	}
	if record.State != StateClosed && record.State != StateBroken {
		// 仅允许删除终态记录，防止运行中 tunnel 被误删。
		return nil, ErrInvalidStateTransition
	}
	delete(registry.records, normalizedTunnelID)
	registry.removeIdleOrderLocked(normalizedTunnelID)
	registry.updatedAt = time.Now().UTC()
	return cloneRecord(record), nil
}

// ExpiredIdleIDs 返回超过 TTL 的 idle tunnel 列表。
func (registry *Registry) ExpiredIdleIDs(now time.Time, ttl time.Duration) []string {
	if ttl <= 0 {
		// 未配置 TTL 时不做回收。
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]string, 0)
	for tunnelID, record := range registry.records {
		if record.State != StateIdle {
			continue
		}
		if record.IdleSince.IsZero() {
			continue
		}
		if now.Sub(record.IdleSince) >= ttl {
			// 超过 TTL 的 idle tunnel 进入回收候选。
			result = append(result, tunnelID)
		}
	}
	return result
}

// Get 返回指定 tunnel 的记录快照。
func (registry *Registry) Get(tunnelID string) (*Record, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := registry.records[strings.TrimSpace(tunnelID)]
	if !exists {
		return nil, false
	}
	return cloneRecord(record), true
}

// Snapshot 返回池状态统计快照。
func (registry *Registry) Snapshot() Snapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	snapshot := Snapshot{
		UpdatedAt:  registry.updatedAt,
		TotalCount: len(registry.records),
	}
	for _, record := range registry.records {
		switch record.State {
		case StateOpening:
			snapshot.OpeningCount++
		case StateIdle:
			snapshot.IdleCount++
		case StateReserved:
			snapshot.ReservedCount++
		case StateActive:
			snapshot.ActiveCount++
		case StateClosing:
			snapshot.ClosingCount++
		case StateClosed:
			snapshot.ClosedCount++
		case StateBroken:
			snapshot.BrokenCount++
		}
	}
	return snapshot
}

// IdleIDs 返回按 FIFO 顺序排列的 idle tunnel ID 列表。
func (registry *Registry) IdleIDs(limit int) []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if limit <= 0 || len(registry.idleOrder) == 0 {
		// 未请求数量或当前无 idle 时返回空切片。
		return nil
	}
	result := make([]string, 0, limit)
	for _, tunnelID := range registry.idleOrder {
		record, exists := registry.records[tunnelID]
		if !exists {
			// 记录已删除时忽略该索引项。
			continue
		}
		if record.State != StateIdle {
			// 仅返回真实 idle 状态，跳过脏索引项。
			continue
		}
		result = append(result, tunnelID)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func (registry *Registry) transition(now time.Time, tunnelID string, targetState State, message string) error {
	if tunnelID == "" {
		// 空 tunnelID 无法执行状态迁移。
		return ErrTunnelNotFound
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, exists := registry.records[tunnelID]
	if !exists {
		return ErrTunnelNotFound
	}
	if !isValidTransition(record.State, targetState) {
		// 非法迁移直接拒绝，避免状态机漂移。
		return ErrInvalidStateTransition
	}
	if record.State == StateIdle {
		// 离开 idle 前清理 idle 顺序索引。
		registry.removeIdleOrderLocked(tunnelID)
	}
	record.State = targetState
	record.UpdatedAt = now
	if targetState == StateIdle {
		record.IdleSince = now
		registry.idleOrder = append(registry.idleOrder, tunnelID)
	} else {
		record.IdleSince = time.Time{}
	}
	if targetState == StateBroken {
		// broken 态下记录最后错误，便于诊断。
		record.LastError = strings.TrimSpace(message)
	}
	registry.stateChangeSeq++
	record.StateChangeSeq = registry.stateChangeSeq
	registry.updatedAt = now
	return nil
}

func (registry *Registry) countByStateLocked(state State) int {
	count := 0
	for _, record := range registry.records {
		if record.State == state {
			count++
		}
	}
	return count
}

func (registry *Registry) removeIdleOrderLocked(tunnelID string) {
	if len(registry.idleOrder) == 0 {
		return
	}
	filtered := registry.idleOrder[:0]
	for _, orderedTunnelID := range registry.idleOrder {
		if orderedTunnelID == tunnelID {
			continue
		}
		filtered = append(filtered, orderedTunnelID)
	}
	registry.idleOrder = filtered
}

func isValidTransition(current State, target State) bool {
	switch current {
	case StateOpening:
		return target == StateIdle || target == StateBroken
	case StateIdle:
		return target == StateReserved || target == StateClosing || target == StateBroken
	case StateReserved:
		return target == StateActive || target == StateClosing || target == StateBroken
	case StateActive:
		return target == StateClosing || target == StateBroken
	case StateClosing:
		return target == StateClosed
	case StateClosed, StateBroken:
		return false
	default:
		return false
	}
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}
