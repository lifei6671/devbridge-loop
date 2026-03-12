package transport

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Session 定义 transport 聚合根接口。
type Session interface {
	ID() string
	Meta() SessionMeta
	State() SessionState
	BindingInfo() BindingInfo
	ProtocolState() ProtocolState

	Open(ctx context.Context) error
	Close(ctx context.Context, reason error) error
	MarkFailed(cause error, retrying bool, giveUp bool) error

	Control() (ControlChannel, error)
	TunnelProducer() (TunnelProducer, error)
	TunnelAcceptor() (TunnelAcceptor, error)
	TunnelPool() (TunnelPool, error)

	Done() <-chan struct{}
	Err() error
}

// SessionCapabilities 统一封装 session 可用能力。
type SessionCapabilities struct {
	ControlChannel ControlChannel
	Producer       TunnelProducer
	Acceptor       TunnelAcceptor
	Pool           TunnelPool
}

// InMemorySession 是线程安全的 Session 基础实现。
type InMemorySession struct {
	mutex sync.RWMutex

	meta        SessionMeta
	bindingInfo BindingInfo
	state       SessionState
	lastError   error

	everAuthenticated bool
	failedMapping     FailedMappingContext

	capabilities SessionCapabilities
	doneChannel  chan struct{}
	doneOnce     sync.Once
}

var _ Session = (*InMemorySession)(nil)

// NewInMemorySession 创建内存版 Session 实例。
func NewInMemorySession(meta SessionMeta, bindingInfo BindingInfo, capabilities SessionCapabilities) *InMemorySession {
	normalizedMeta := meta
	normalizedMeta.SessionID = strings.TrimSpace(meta.SessionID)
	return &InMemorySession{
		meta:         normalizedMeta,
		bindingInfo:  bindingInfo,
		state:        SessionStateIdle,
		capabilities: capabilities,
		doneChannel:  make(chan struct{}),
	}
}

// ID 返回 sessionId。
func (session *InMemorySession) ID() string {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 直接从标准化后的 meta 中读取，避免外部拼接错误。
	return session.meta.SessionID
}

// Meta 返回 session 元信息快照。
func (session *InMemorySession) Meta() SessionMeta {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 返回结构体副本，防止外部直接修改内部状态。
	return session.meta
}

// State 返回 transport 内部状态。
func (session *InMemorySession) State() SessionState {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 只读返回当前状态。
	return session.state
}

// BindingInfo 返回 binding 元信息。
func (session *InMemorySession) BindingInfo() BindingInfo {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 返回副本供上层日志和指标使用。
	return session.bindingInfo
}

// ProtocolState 返回对外协议态视图。
func (session *InMemorySession) ProtocolState() ProtocolState {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 使用固定映射规则，避免外部直接读取内部态。
	return MapSessionStateToProtocolState(session.state, session.failedMapping)
}

// Open 执行最小会话打开流程。
func (session *InMemorySession) Open(ctx context.Context) error {
	if ctx == nil {
		// 防止调用方传 nil context 导致后续读取异常。
		ctx = context.Background()
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.state == SessionStateClosed {
		// closed 会话不允许再次打开。
		return fmt.Errorf("open session: %w", ErrSessionClosed)
	}
	if session.state == SessionStateFailed && session.failedMapping.GiveUp {
		// 已明确放弃的 session 生命周期不能重新打开。
		return fmt.Errorf("open session: %w", ErrSessionClosed)
	}
	if session.state == SessionStateAuthenticated {
		// 已经打开成功时按幂等成功处理。
		return nil
	}
	if session.state == SessionStateDraining {
		// draining 阶段不允许进入新一轮 open。
		return fmt.Errorf("open session: %w: from=%s to=%s", ErrStateTransition, session.state, SessionStateConnecting)
	}
	if session.state == SessionStateIdle || session.state == SessionStateFailed {
		if err := session.transitionLocked(SessionStateConnecting); err != nil {
			return err
		}
	}
	if err := session.ensureContextActiveLocked(ctx); err != nil {
		return err
	}
	if err := session.transitionLocked(SessionStateConnected); err != nil {
		return err
	}
	if err := session.ensureContextActiveLocked(ctx); err != nil {
		return err
	}
	if err := session.transitionLocked(SessionStateControlReady); err != nil {
		return err
	}
	if err := session.ensureContextActiveLocked(ctx); err != nil {
		return err
	}
	if err := session.transitionLocked(SessionStateAuthenticated); err != nil {
		return err
	}
	// 认证完成后刷新失败映射上下文，确保协议态回归 ACTIVE。
	session.everAuthenticated = true
	session.failedMapping = FailedMappingContext{EverAuthenticated: true}
	session.lastError = nil
	session.meta.LastError = ""
	return nil
}

// Close 关闭 session 并触发 Done 信号。
func (session *InMemorySession) Close(ctx context.Context, reason error) error {
	if ctx == nil {
		// 兜底 context，保持接口行为一致。
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		// 调用方取消时直接返回，让上层决定是否重试关闭。
		return fmt.Errorf("close session: %w", ctx.Err())
	default:
		// context 仍可用时继续执行关闭流程。
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.state == SessionStateClosed {
		// 已关闭会话按幂等成功处理。
		return nil
	}
	if session.state == SessionStateAuthenticated {
		if err := session.transitionLocked(SessionStateDraining); err != nil {
			return err
		}
	}
	if err := session.transitionLocked(SessionStateClosed); err != nil {
		return err
	}
	if reason != nil {
		// 显式关闭原因优先记录调用方传入错误。
		session.lastError = reason
		session.meta.LastError = strings.TrimSpace(reason.Error())
	} else {
		// 无关闭原因时使用统一 closed 错误。
		session.lastError = ErrClosed
		session.meta.LastError = ErrClosed.Error()
	}
	session.doneOnce.Do(func() {
		// 仅首次关闭时发出 done 信号，避免重复 close panic。
		close(session.doneChannel)
	})
	return nil
}

// MarkFailed 将 session 标记为失败并记录映射上下文。
func (session *InMemorySession) MarkFailed(cause error, retrying bool, giveUp bool) error {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.state == SessionStateClosed {
		// 已关闭会话不允许再写入 failed 状态。
		return fmt.Errorf("mark failed: %w", ErrSessionClosed)
	}
	if !CanTransitionSessionState(session.state, SessionStateFailed) {
		// 遇到非法状态迁移时返回明确错误，避免静默覆盖。
		return fmt.Errorf(
			"mark failed: %w: from=%s to=%s",
			ErrStateTransition,
			session.state,
			SessionStateFailed,
		)
	}
	session.state = SessionStateFailed
	if cause != nil {
		// 保留底层失败原因，便于管理面输出 last_error。
		session.lastError = cause
		session.meta.LastError = strings.TrimSpace(cause.Error())
	} else {
		// 未提供原因时给出固定占位错误。
		session.lastError = ErrClosed
		session.meta.LastError = ErrClosed.Error()
	}
	session.failedMapping = FailedMappingContext{
		EverAuthenticated: session.everAuthenticated,
		Retrying:          retrying,
		GiveUp:            giveUp,
	}
	if giveUp {
		session.doneOnce.Do(func() {
			// 放弃本次 session 后立即关闭 done 信号。
			close(session.doneChannel)
		})
	}
	return nil
}

// Control 返回 ControlChannel 能力。
func (session *InMemorySession) Control() (ControlChannel, error) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	if session.capabilities.ControlChannel == nil {
		// 角色不支持时必须返回 ErrUnsupported。
		return nil, fmt.Errorf("control capability: %w", ErrUnsupported)
	}
	switch session.state {
	case SessionStateControlReady, SessionStateAuthenticated, SessionStateDraining:
		// 控制面在 ready 之后可被查询使用。
		return session.capabilities.ControlChannel, nil
	case SessionStateClosed, SessionStateFailed:
		// 终止态统一返回 session closed 错误。
		return nil, fmt.Errorf("control capability: %w", ErrSessionClosed)
	default:
		// 其他阶段视为尚未 ready。
		return nil, fmt.Errorf("control capability: %w", ErrNotReady)
	}
}

// TunnelProducer 返回 TunnelProducer 能力。
func (session *InMemorySession) TunnelProducer() (TunnelProducer, error) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	if session.capabilities.Producer == nil {
		// 角色不支持时必须返回 ErrUnsupported。
		return nil, fmt.Errorf("tunnel producer capability: %w", ErrUnsupported)
	}
	if session.state == SessionStateAuthenticated {
		// 仅 authenticated 会话允许生产 tunnel。
		return session.capabilities.Producer, nil
	}
	if session.state.IsTerminal() {
		// 终止态下统一返回 session closed。
		return nil, fmt.Errorf("tunnel producer capability: %w", ErrSessionClosed)
	}
	return nil, fmt.Errorf("tunnel producer capability: %w", ErrNotReady)
}

// TunnelAcceptor 返回 TunnelAcceptor 能力。
func (session *InMemorySession) TunnelAcceptor() (TunnelAcceptor, error) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	if session.capabilities.Acceptor == nil {
		// 角色不支持时必须返回 ErrUnsupported。
		return nil, fmt.Errorf("tunnel acceptor capability: %w", ErrUnsupported)
	}
	if session.state == SessionStateAuthenticated {
		// 首版约束在 authenticated 后开放 accept 能力。
		return session.capabilities.Acceptor, nil
	}
	if session.state.IsTerminal() {
		// 终止态下不再允许接收 tunnel。
		return nil, fmt.Errorf("tunnel acceptor capability: %w", ErrSessionClosed)
	}
	return nil, fmt.Errorf("tunnel acceptor capability: %w", ErrNotReady)
}

// TunnelPool 返回 TunnelPool 能力。
func (session *InMemorySession) TunnelPool() (TunnelPool, error) {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	if session.capabilities.Pool == nil {
		// 角色不支持时必须返回 ErrUnsupported。
		return nil, fmt.Errorf("tunnel pool capability: %w", ErrUnsupported)
	}
	if session.state == SessionStateAuthenticated {
		// authenticated 后才允许管理池。
		return session.capabilities.Pool, nil
	}
	if session.state.IsTerminal() {
		// 终止态统一返回 session closed。
		return nil, fmt.Errorf("tunnel pool capability: %w", ErrSessionClosed)
	}
	return nil, fmt.Errorf("tunnel pool capability: %w", ErrNotReady)
}

// Done 返回会话结束通知。
func (session *InMemorySession) Done() <-chan struct{} {
	// doneChannel 只创建一次，直接返回即可。
	return session.doneChannel
}

// Err 返回会话最近错误。
func (session *InMemorySession) Err() error {
	session.mutex.RLock()
	defer session.mutex.RUnlock()
	// 返回最近一次失败或关闭原因。
	return session.lastError
}

// ensureContextActiveLocked 校验上下文是否仍可用。
func (session *InMemorySession) ensureContextActiveLocked(ctx context.Context) error {
	select {
	case <-ctx.Done():
		// 上下文取消时写入失败态并透传错误。
		session.state = SessionStateFailed
		session.failedMapping = FailedMappingContext{
			EverAuthenticated: session.everAuthenticated,
			Retrying:          false,
			GiveUp:            false,
		}
		session.lastError = ctx.Err()
		session.meta.LastError = strings.TrimSpace(ctx.Err().Error())
		return fmt.Errorf("session context cancelled: %w", ctx.Err())
	default:
		// 上下文可用时继续流程。
		return nil
	}
}

// transitionLocked 执行受状态机约束的状态迁移。
func (session *InMemorySession) transitionLocked(nextState SessionState) error {
	if !CanTransitionSessionState(session.state, nextState) {
		// 非法迁移直接返回显式错误，禁止静默跳转。
		return fmt.Errorf(
			"session transition: %w: from=%s to=%s",
			ErrStateTransition,
			session.state,
			nextState,
		)
	}
	// 迁移合法时更新当前状态。
	session.state = nextState
	return nil
}
