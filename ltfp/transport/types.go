package transport

import "time"

// BindingType 表示 transport binding 的实现类型。
type BindingType string

const (
	// BindingTypeGRPCH2 表示 gRPC over HTTP/2 binding。
	BindingTypeGRPCH2 BindingType = "grpc_h2"
	// BindingTypeTCPFramed 表示基于长度前缀帧的 TCP binding。
	BindingTypeTCPFramed BindingType = "tcp_framed"
	// BindingTypeQUICNative 表示原生 QUIC binding。
	BindingTypeQUICNative BindingType = "quic_native"
	// BindingTypeH3Stream 表示 HTTP/3 stream binding。
	BindingTypeH3Stream BindingType = "h3_stream"
)

// String 返回 binding 类型的字符串表示。
func (bindingType BindingType) String() string {
	// 直接返回常量字符串，便于日志与指标统一输出。
	return string(bindingType)
}

// BindingInfo 描述当前 session 的 binding 元信息。
type BindingInfo struct {
	Type                 BindingType
	Version              string
	MaxConcurrentStreams int64
}

// SessionMeta 描述 session 级元数据。
type SessionMeta struct {
	SessionID    string
	SessionEpoch uint64
	NodeID       string
	Labels       map[string]string
	LastError    string
}

// TunnelMeta 描述 tunnel 级元数据。
type TunnelMeta struct {
	TunnelID     string
	SessionID    string
	SessionEpoch uint64
	CreatedAt    time.Time
	Labels       map[string]string
}

// SessionState 描述 transport 层内部会话状态。
type SessionState string

const (
	// SessionStateIdle 表示会话尚未打开。
	SessionStateIdle SessionState = "idle"
	// SessionStateConnecting 表示正在建立底层连接。
	SessionStateConnecting SessionState = "connecting"
	// SessionStateConnected 表示底层连接已建立。
	SessionStateConnected SessionState = "connected"
	// SessionStateControlReady 表示控制面通道已可用。
	SessionStateControlReady SessionState = "control_ready"
	// SessionStateAuthenticated 表示认证完成且可处理业务控制面消息。
	SessionStateAuthenticated SessionState = "authenticated"
	// SessionStateDraining 表示会话进入排空阶段。
	SessionStateDraining SessionState = "draining"
	// SessionStateFailed 表示会话发生内部失败。
	SessionStateFailed SessionState = "failed"
	// SessionStateClosed 表示会话已关闭。
	SessionStateClosed SessionState = "closed"
)

// IsTerminal 判断会话状态是否为终止态。
func (sessionState SessionState) IsTerminal() bool {
	// 仅 failed/closed 视为终止态，其他状态都可能继续流转。
	return sessionState == SessionStateFailed || sessionState == SessionStateClosed
}

// TunnelState 描述 transport 层内部 tunnel 状态。
type TunnelState string

const (
	// TunnelStateOpening 表示 tunnel 正在建立。
	TunnelStateOpening TunnelState = "opening"
	// TunnelStateIdle 表示 tunnel 空闲可分配。
	TunnelStateIdle TunnelState = "idle"
	// TunnelStateReserved 表示 tunnel 已预留但尚未进入 active。
	TunnelStateReserved TunnelState = "reserved"
	// TunnelStateActive 表示 tunnel 正在承载流量。
	TunnelStateActive TunnelState = "active"
	// TunnelStateClosing 表示 tunnel 正在关闭。
	TunnelStateClosing TunnelState = "closing"
	// TunnelStateClosed 表示 tunnel 已正常关闭。
	TunnelStateClosed TunnelState = "closed"
	// TunnelStateBroken 表示 tunnel 异常损坏不可复用。
	TunnelStateBroken TunnelState = "broken"
)

// IsTerminal 判断 tunnel 状态是否为终止态。
func (tunnelState TunnelState) IsTerminal() bool {
	// closed/broken 都不能再回到可用池中。
	return tunnelState == TunnelStateClosed || tunnelState == TunnelStateBroken
}

// ProtocolState 描述对外暴露的 LTFP 协议态。
type ProtocolState string

const (
	// ProtocolStateConnecting 表示正在连接或重连中。
	ProtocolStateConnecting ProtocolState = "CONNECTING"
	// ProtocolStateAuthenticating 表示控制面初始化/认证阶段。
	ProtocolStateAuthenticating ProtocolState = "AUTHENTICATING"
	// ProtocolStateActive 表示会话处于可服务状态。
	ProtocolStateActive ProtocolState = "ACTIVE"
	// ProtocolStateDraining 表示会话排空中。
	ProtocolStateDraining ProtocolState = "DRAINING"
	// ProtocolStateStale 表示已激活会话变为失效状态。
	ProtocolStateStale ProtocolState = "STALE"
	// ProtocolStateClosed 表示会话已关闭。
	ProtocolStateClosed ProtocolState = "CLOSED"
)

// FailedMappingContext 描述 SessionState=failed 时的映射上下文。
type FailedMappingContext struct {
	// EverAuthenticated 表示本次 session 是否曾进入 authenticated。
	EverAuthenticated bool
	// Retrying 表示当前是否处于自动重试/退避重连阶段。
	Retrying bool
	// GiveUp 表示实现是否已经放弃本次 session 建立流程。
	GiveUp bool
}

// MapSessionStateToProtocolState 将 transport 内部态映射为对外协议态。
func MapSessionStateToProtocolState(sessionState SessionState, mapping FailedMappingContext) ProtocolState {
	switch sessionState {
	case SessionStateIdle, SessionStateConnecting:
		// idle/connecting 都统一暴露为 CONNECTING。
		return ProtocolStateConnecting
	case SessionStateConnected, SessionStateControlReady:
		// connected/control_ready 都属于认证前阶段。
		return ProtocolStateAuthenticating
	case SessionStateAuthenticated:
		// authenticated 直接映射为 ACTIVE。
		return ProtocolStateActive
	case SessionStateDraining:
		// draining 直接映射为 DRAINING。
		return ProtocolStateDraining
	case SessionStateClosed:
		// closed 直接映射为 CLOSED。
		return ProtocolStateClosed
	case SessionStateFailed:
		// failed 的映射必须依赖上下文，不允许 binding 自定义。
		if !mapping.EverAuthenticated && mapping.Retrying && !mapping.GiveUp {
			return ProtocolStateConnecting
		}
		if mapping.EverAuthenticated {
			return ProtocolStateStale
		}
		return ProtocolStateClosed
	default:
		// 未知状态按最保守策略收敛到 CLOSED。
		return ProtocolStateClosed
	}
}

var sessionStateTransitions = map[SessionState]map[SessionState]struct{}{
	SessionStateIdle: {
		SessionStateConnecting: {},
		SessionStateFailed:     {},
		SessionStateClosed:     {},
	},
	SessionStateConnecting: {
		SessionStateConnected: {},
		SessionStateFailed:    {},
		SessionStateClosed:    {},
	},
	SessionStateConnected: {
		SessionStateControlReady: {},
		SessionStateFailed:       {},
		SessionStateClosed:       {},
	},
	SessionStateControlReady: {
		SessionStateAuthenticated: {},
		SessionStateFailed:        {},
		SessionStateClosed:        {},
	},
	SessionStateAuthenticated: {
		SessionStateDraining: {},
		SessionStateFailed:   {},
		SessionStateClosed:   {},
	},
	SessionStateDraining: {
		SessionStateClosed: {},
		SessionStateFailed: {},
	},
	SessionStateFailed: {
		SessionStateConnecting: {},
		SessionStateClosed:     {},
	},
	SessionStateClosed: {},
}

// CanTransitionSessionState 判断 session 状态迁移是否合法。
func CanTransitionSessionState(fromState SessionState, toState SessionState) bool {
	if fromState == toState {
		// 同态迁移始终允许，便于幂等调用处理。
		return true
	}
	nextStates, exists := sessionStateTransitions[fromState]
	if !exists {
		// 未知起始状态视为非法。
		return false
	}
	_, allowed := nextStates[toState]
	return allowed
}

var tunnelStateTransitions = map[TunnelState]map[TunnelState]struct{}{
	TunnelStateOpening: {
		TunnelStateIdle:   {},
		TunnelStateBroken: {},
		TunnelStateClosed: {},
	},
	TunnelStateIdle: {
		TunnelStateReserved: {},
		TunnelStateBroken:   {},
		TunnelStateClosing:  {},
		TunnelStateClosed:   {},
	},
	TunnelStateReserved: {
		TunnelStateActive: {},
		TunnelStateClosed: {},
		TunnelStateBroken: {},
	},
	TunnelStateActive: {
		TunnelStateClosing: {},
		TunnelStateClosed:  {},
		TunnelStateBroken:  {},
	},
	TunnelStateClosing: {
		TunnelStateClosed: {},
		TunnelStateBroken: {},
	},
	TunnelStateClosed: {},
	TunnelStateBroken: {},
}

// CanTransitionTunnelState 判断 tunnel 状态迁移是否合法。
func CanTransitionTunnelState(fromState TunnelState, toState TunnelState) bool {
	if fromState == toState {
		// 同态迁移始终允许，便于上层做幂等收敛。
		return true
	}
	nextStates, exists := tunnelStateTransitions[fromState]
	if !exists {
		// 未知起始状态不允许继续迁移。
		return false
	}
	_, allowed := nextStates[toState]
	return allowed
}
