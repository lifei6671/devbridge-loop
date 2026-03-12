package session

import (
	"fmt"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Event 表示驱动会话状态机流转的事件类型。
type Event string

const (
	// EventConnected 表示底层连接已建立。
	EventConnected Event = "CONNECTED"
	// EventAuthStart 表示认证流程开始。
	EventAuthStart Event = "AUTH_START"
	// EventAuthSuccess 表示认证成功。
	EventAuthSuccess Event = "AUTH_SUCCESS"
	// EventAuthFailed 表示认证失败。
	EventAuthFailed Event = "AUTH_FAILED"
	// EventHeartbeatTimeout 表示心跳超时。
	EventHeartbeatTimeout Event = "HEARTBEAT_TIMEOUT"
	// EventNewEpochTakeover 表示新会话 epoch 已接管。
	EventNewEpochTakeover Event = "NEW_EPOCH_TAKEOVER"
	// EventClosed 表示连接已关闭。
	EventClosed Event = "CLOSED"
)

// NextState 根据当前状态和事件计算下一状态。
func NextState(current pb.SessionState, event Event) (pb.SessionState, error) {
	switch current {
	case pb.SessionStateConnecting:
		// CONNECTING 只允许进入 AUTHENTICATING 或直接关闭。
		switch event {
		case EventConnected, EventAuthStart:
			return pb.SessionStateAuthenticating, nil
		case EventClosed:
			return pb.SessionStateClosed, nil
		}
	case pb.SessionStateAuthenticating:
		// AUTHENTICATING 成功进入 ACTIVE，失败直接 CLOSED。
		switch event {
		case EventAuthSuccess:
			return pb.SessionStateActive, nil
		case EventAuthFailed, EventClosed:
			return pb.SessionStateClosed, nil
		}
	case pb.SessionStateActive:
		// ACTIVE 在新 epoch 接管或超时后进入 DRAINING/STALE。
		switch event {
		case EventNewEpochTakeover:
			return pb.SessionStateDraining, nil
		case EventHeartbeatTimeout:
			return pb.SessionStateStale, nil
		case EventClosed:
			return pb.SessionStateClosed, nil
		}
	case pb.SessionStateDraining:
		// DRAINING 只允许收尾关闭。
		if event == EventClosed {
			return pb.SessionStateClosed, nil
		}
	case pb.SessionStateStale:
		// STALE 只允许关闭，不允许恢复到 ACTIVE。
		if event == EventClosed {
			return pb.SessionStateClosed, nil
		}
	case pb.SessionStateClosed:
		// CLOSED 为终态，外部必须新建会话而不是回退状态。
	}
	return "", ltfperrors.New(ltfperrors.CodeInvalidStateTransition, fmt.Sprintf("invalid transition: state=%s event=%s", current, event))
}

// ValidateHandshakeSequence 校验握手消息链路顺序。
func ValidateHandshakeSequence(sequence []pb.ControlMessageType) error {
	expected := []pb.ControlMessageType{
		pb.ControlMessageConnectorHello,
		pb.ControlMessageConnectorWelcome,
		pb.ControlMessageConnectorAuth,
		pb.ControlMessageConnectorAuthAck,
	}
	// 序列至少需要包含 HELLO/WELCOME/AUTH/AUTH_ACK 四步。
	if len(sequence) < len(expected) {
		return ltfperrors.New(ltfperrors.CodeInvalidPayload, "handshake sequence is incomplete")
	}
	for index, expectedType := range expected {
		// 固定顺序校验，避免握手链路语义漂移。
		if sequence[index] != expectedType {
			return ltfperrors.New(ltfperrors.CodeInvalidPayload, fmt.Sprintf("invalid handshake order at index=%d: got=%s want=%s", index, sequence[index], expectedType))
		}
	}
	// AUTH_ACK 之后若有消息，必须全部是 HEARTBEAT。
	for index := len(expected); index < len(sequence); index++ {
		if sequence[index] != pb.ControlMessageHeartbeat {
			return ltfperrors.New(ltfperrors.CodeInvalidPayload, fmt.Sprintf("message after authAck must be heartbeat, got=%s", sequence[index]))
		}
	}
	return nil
}

// ValidateAuthEpochAuthority 校验握手期 sessionEpoch 权威规则。
func ValidateAuthEpochAuthority(welcome pb.ConnectorWelcome, authAck pb.ConnectorAuthAck) error {
	// 认证失败时不要求 epoch 一致，但成功时必须一致。
	if !authAck.Success {
		return nil
	}
	// 成功认证必须携带最终生效 sessionEpoch。
	if authAck.SessionEpoch == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "authAck.sessionEpoch must be greater than 0 when auth success")
	}
	// 成功认证时 authAck epoch 必须等于 welcome 预分配值。
	if authAck.SessionEpoch != welcome.AssignedSessionEpoch {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "authAck.sessionEpoch must equal welcome.assignedSessionEpoch")
	}
	return nil
}

// IsHeartbeatTimeout 判断会话是否已发生心跳超时。
func IsHeartbeatTimeout(lastHeartbeat time.Time, now time.Time, intervalSec uint32, multiplier uint32) bool {
	// lastHeartbeat 为空时视为已超时，调用方应尽快摘除会话。
	if lastHeartbeat.IsZero() {
		return true
	}
	// multiplier 非法时回退到 1，避免配置错误导致除零语义。
	if multiplier == 0 {
		multiplier = 1
	}
	timeout := time.Duration(intervalSec*multiplier) * time.Second
	// intervalSec 为 0 时使用 1 秒兜底，避免配置失误导致永不超时。
	if timeout == 0 {
		timeout = time.Second
	}
	// 超过超时窗口即视为 STALE 候选。
	return now.Sub(lastHeartbeat) > timeout
}

// ShouldRejectResourceMutation 判断当前会话状态是否应拒绝资源写操作。
func ShouldRejectResourceMutation(state pb.SessionState) bool {
	// DRAINING/STALE/CLOSED 都不允许继续修改资源状态。
	switch state {
	case pb.SessionStateDraining, pb.SessionStateStale, pb.SessionStateClosed:
		return true
	default:
		return false
	}
}
