package session

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
)

// State 定义 session 生命周期状态。
type State string

const (
	StateConnecting     State = "CONNECTING"
	StateAuthenticating State = "AUTHENTICATING"
	StateActive         State = "ACTIVE"
	StateDraining       State = "DRAINING"
	StateStale          State = "STALE"
	StateClosed         State = "CLOSED"
)

// Dialer 定义 session 连接建立能力。
type Dialer interface {
	// Dial 建立到底层传输的连接。
	Dial(ctx context.Context) error
}

// ReconnectPolicy 定义重连退避策略。
type ReconnectPolicy interface {
	// NextDelay 返回下一次重连等待时间。
	NextDelay(attempt int) time.Duration
}

// StateHandler 处理 session 状态变化引发的联动操作。
type StateHandler interface {
	// HandleSessionState 根据状态变化执行联动逻辑，例如收敛 tunnel pool。
	HandleSessionState(state string) (int, error)
}

// FixedBackoff 是固定间隔的重连策略。
type FixedBackoff struct {
	Delay time.Duration
}

// NextDelay 返回固定退避时间。
func (b FixedBackoff) NextDelay(attempt int) time.Duration {
	_ = attempt // 固定策略不依赖次数
	if b.Delay <= 0 {
		// 默认给一个保守延时，避免忙等。
		return time.Second
	}
	return b.Delay
}

// Options 定义 Manager 的依赖与策略。
type Options struct {
	Dialer          Dialer
	Authenticator   Authenticator
	Heartbeat       HeartbeatScheduler
	HeartbeatSender Sender
	ReconnectPolicy ReconnectPolicy
	Metrics         *obs.Metrics
	StateHandler    StateHandler
}

// Manager 管理 session 生命周期与 epoch。
type Manager struct {
	mu               sync.RWMutex
	state            State
	stateVersion     uint64
	epoch            uint64
	lastHeartbeatAt  time.Time
	lastStateAt      time.Time
	dialer           Dialer
	authenticator    Authenticator
	heartbeat        HeartbeatScheduler
	heartbeatSender  Sender
	reconnectPolicy  ReconnectPolicy
	reconnectAttempt int
	metrics          *obs.Metrics
	stateHandler     StateHandler
	stateHandlerMu   sync.Mutex
}

// NewManager 创建默认的 session 管理器。
func NewManager() *Manager {
	// 约定：初始状态为 CONNECTING，epoch 从 1 开始。
	now := time.Now()
	manager := &Manager{
		state:           StateConnecting,
		stateVersion:    1,
		epoch:           1,
		lastStateAt:     now,
		reconnectPolicy: FixedBackoff{Delay: time.Second},
		metrics:         obs.DefaultMetrics,
	}
	// 初始化时同步写入 session 状态指标。
	manager.metrics.SetAgentSessionState(string(StateConnecting))
	return manager
}

// NewManagerWithOptions 创建带依赖的 session 管理器。
func NewManagerWithOptions(opts Options) *Manager {
	mgr := NewManager()
	// 注入依赖与策略，未提供则使用默认值。
	if opts.Dialer != nil {
		mgr.dialer = opts.Dialer
	}
	if opts.Authenticator != nil {
		mgr.authenticator = opts.Authenticator
	}
	mgr.heartbeat = opts.Heartbeat
	mgr.heartbeatSender = opts.HeartbeatSender
	if opts.ReconnectPolicy != nil {
		mgr.reconnectPolicy = opts.ReconnectPolicy
	}
	if opts.Metrics != nil {
		mgr.metrics = opts.Metrics
	}
	if opts.StateHandler != nil {
		mgr.stateHandler = opts.StateHandler
	}
	// 注入自定义指标对象后，立即刷新一次当前状态。
	mgr.metrics.SetAgentSessionState(string(mgr.state))
	return mgr
}

// Run 启动 session 管理循环，包含重连逻辑。
func (m *Manager) Run(ctx context.Context) error {
	for {
		if err := m.connectOnce(ctx); err != nil {
			// 连接失败后推进 epoch，避免旧连接污染。
			m.AdvanceEpoch()
			if ctx.Err() != nil {
				// 上下文已取消，直接退出。
				return ctx.Err()
			}
			delay := m.reconnectPolicy.NextDelay(m.reconnectAttempt)
			m.reconnectAttempt++
			// 等待退避时间后重试。
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				continue
			}
		}

		// 已连接成功，等待上层取消或重连触发。
		select {
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Start 启动 session 状态机，进入 ACTIVE。
func (m *Manager) Start(ctx context.Context) error {
	_ = ctx // skeleton: 暂不使用 ctx，后续接入连接流程
	m.mu.Lock()
	// 进入 ACTIVE，标记状态切换时间。
	stateChanged, stateVersion := m.setStateLocked(StateActive, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateActive, stateChanged, stateVersion)
	return nil
}

// Stop 关闭 session 状态机，进入 CLOSED。
func (m *Manager) Stop(ctx context.Context) error {
	_ = ctx // skeleton: 预留关闭流程
	m.mu.Lock()
	// 统一收敛到 CLOSED。
	stateChanged, stateVersion := m.setStateLocked(StateClosed, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateClosed, stateChanged, stateVersion)
	return nil
}

// MarkDraining 将 session 标记为 DRAINING。
func (m *Manager) MarkDraining() {
	m.mu.Lock()
	// 排空阶段：拒绝新流量。
	stateChanged, stateVersion := m.setStateLocked(StateDraining, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateDraining, stateChanged, stateVersion)
}

// MarkStale 将 session 标记为 STALE。
func (m *Manager) MarkStale() {
	m.mu.Lock()
	// 过期阶段：等待关闭或重连。
	stateChanged, stateVersion := m.setStateLocked(StateStale, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateStale, stateChanged, stateVersion)
}

// RecordHeartbeat 记录一次心跳到达时间。
func (m *Manager) RecordHeartbeat(at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 仅记录时间戳，后续用于超时判定。
	m.lastHeartbeatAt = at
}

// HeartbeatAge 返回距上次心跳的时长。
func (m *Manager) HeartbeatAge(now time.Time) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 若未收到心跳，返回一个极大值用于触发超时处理。
	if m.lastHeartbeatAt.IsZero() {
		return time.Hour * 24 * 365
	}
	return now.Sub(m.lastHeartbeatAt)
}

// IsEpochValid 判断消息 epoch 是否与当前一致。
func (m *Manager) IsEpochValid(epoch uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 旧 epoch 消息一律视为无效。
	return epoch == m.epoch
}

// AdvanceEpoch 递增 session epoch 并返回新值。
func (m *Manager) AdvanceEpoch() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 每次重连前推进 epoch，避免污染。
	m.epoch++
	return m.epoch
}

// Epoch 返回当前 session epoch。
func (m *Manager) Epoch() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// epoch 用于防止旧连接污染。
	return m.epoch
}

// State 返回当前 session 状态。
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 只读访问需要锁保护。
	return m.state
}

// connectOnce 执行单次建连 + 鉴权 + 心跳启动。
func (m *Manager) connectOnce(ctx context.Context) error {
	m.mu.Lock()
	// 切换到 CONNECTING 状态。
	connectingChanged, connectingVersion := m.setStateLocked(StateConnecting, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateConnecting, connectingChanged, connectingVersion)

	if m.dialer != nil {
		// 先建立到底层传输的连接。
		if err := m.dialer.Dial(ctx); err != nil {
			return err
		}
	}

	m.mu.Lock()
	// 进入 AUTHENTICATING 状态。
	authenticatingChanged, authenticatingVersion := m.setStateLocked(StateAuthenticating, time.Now())
	m.mu.Unlock()
	m.notifyStateHandler(StateAuthenticating, authenticatingChanged, authenticatingVersion)

	if m.authenticator != nil {
		// 执行鉴权流程。
		if err := m.authenticator.Authenticate(ctx); err != nil {
			return err
		}
	}

	m.mu.Lock()
	// 鉴权成功后进入 ACTIVE 状态。
	activeChanged, activeVersion := m.setStateLocked(StateActive, time.Now())
	m.reconnectAttempt = 0
	m.mu.Unlock()
	m.notifyStateHandler(StateActive, activeChanged, activeVersion)

	if m.heartbeatSender != nil {
		// 启动心跳循环，错误由发送方内部处理。
		go func() {
			_ = m.heartbeat.Run(ctx, m.heartbeatSender)
		}()
	}

	return nil
}

// setStateLocked 在持锁上下文中切换 session 状态并同步指标。
func (m *Manager) setStateLocked(state State, stateAt time.Time) (bool, uint64) {
	stateChanged := m.state != state
	if stateChanged {
		m.stateVersion++
	}
	m.state = state
	m.lastStateAt = stateAt
	if m.metrics != nil {
		// 状态变更时同步写入 session 状态指标，便于外部实时观测。
		m.metrics.SetAgentSessionState(string(state))
	}
	return stateChanged, m.stateVersion
}

func (m *Manager) notifyStateHandler(state State, stateChanged bool, stateVersion uint64) {
	if m == nil || !stateChanged {
		return
	}
	m.stateHandlerMu.Lock()
	defer m.stateHandlerMu.Unlock()

	m.mu.RLock()
	stateHandler := m.stateHandler
	shouldNotify := stateHandler != nil && m.state == state && m.stateVersion == stateVersion
	if !shouldNotify {
		m.mu.RUnlock()
		return
	}
	// 在回调执行期间保持读锁，避免版本检查后状态再次推进导致陈旧回调落地。
	_, err := stateHandler.HandleSessionState(string(state))
	m.mu.RUnlock()
	if err != nil {
		log.Printf("agent session state handler failed state=%s err=%v", state, err)
	}
}
