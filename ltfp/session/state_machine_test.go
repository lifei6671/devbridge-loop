package session

import (
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestValidateHandshakeSequence 验证握手顺序校验。
func TestValidateHandshakeSequence(t *testing.T) {
	t.Parallel()

	sequence := []pb.ControlMessageType{
		pb.ControlMessageConnectorHello,
		pb.ControlMessageConnectorWelcome,
		pb.ControlMessageConnectorAuth,
		pb.ControlMessageConnectorAuthAck,
		pb.ControlMessageHeartbeat,
	}
	if err := ValidateHandshakeSequence(sequence); err != nil {
		t.Fatalf("validate handshake sequence failed: %v", err)
	}
}

// TestValidateHandshakeSequenceRejectInvalidOrder 验证非法握手顺序会被拒绝。
func TestValidateHandshakeSequenceRejectInvalidOrder(t *testing.T) {
	t.Parallel()

	sequence := []pb.ControlMessageType{
		pb.ControlMessageConnectorHello,
		pb.ControlMessageConnectorAuth,
		pb.ControlMessageConnectorWelcome,
		pb.ControlMessageConnectorAuthAck,
	}
	err := ValidateHandshakeSequence(sequence)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 握手顺序错误应归类为非法 payload。
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidPayload) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateAuthEpochAuthority 验证认证成功时 epoch 权威规则。
func TestValidateAuthEpochAuthority(t *testing.T) {
	t.Parallel()

	welcome := pb.ConnectorWelcome{AssignedSessionEpoch: 11}
	authAck := pb.ConnectorAuthAck{Success: true, SessionEpoch: 11}
	if err := ValidateAuthEpochAuthority(welcome, authAck); err != nil {
		t.Fatalf("validate auth epoch authority failed: %v", err)
	}
}

// TestValidateAuthEpochAuthorityRejectMismatch 验证 epoch 不一致会被拒绝。
func TestValidateAuthEpochAuthorityRejectMismatch(t *testing.T) {
	t.Parallel()

	welcome := pb.ConnectorWelcome{AssignedSessionEpoch: 11}
	authAck := pb.ConnectorAuthAck{Success: true, SessionEpoch: 12}
	err := ValidateAuthEpochAuthority(welcome, authAck)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 权威 epoch 不一致应返回 INVALID_SESSION_EPOCH。
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidSessionEpoch) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestNextState 验证会话状态机的合法流转。
func TestNextState(t *testing.T) {
	t.Parallel()

	state, err := NextState(pb.SessionStateConnecting, EventConnected)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}
	if state != pb.SessionStateAuthenticating {
		t.Fatalf("unexpected state: %s", state)
	}

	state, err = NextState(state, EventAuthSuccess)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}
	if state != pb.SessionStateActive {
		t.Fatalf("unexpected state: %s", state)
	}
}

// TestNextStateRejectInvalidTransition 验证非法状态流转会被拒绝。
func TestNextStateRejectInvalidTransition(t *testing.T) {
	t.Parallel()

	_, err := NextState(pb.SessionStateClosed, EventAuthSuccess)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// CLOSED 回退到 AUTH_SUCCESS 应视为非法流转。
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidStateTransition) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIsHeartbeatTimeout 验证心跳超时判定。
func TestIsHeartbeatTimeout(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	// 超过 interval*multiplier 后应判定超时。
	if !IsHeartbeatTimeout(now.Add(-10*time.Second), now, 3, 2) {
		t.Fatalf("expected heartbeat timeout")
	}
	// 未超过阈值时不应判定超时。
	if IsHeartbeatTimeout(now.Add(-2*time.Second), now, 3, 2) {
		t.Fatalf("expected heartbeat not timeout")
	}
}

// TestShouldRejectResourceMutation 验证禁止写入状态集合。
func TestShouldRejectResourceMutation(t *testing.T) {
	t.Parallel()

	if !ShouldRejectResourceMutation(pb.SessionStateDraining) {
		t.Fatalf("expected reject mutation for draining")
	}
	if ShouldRejectResourceMutation(pb.SessionStateActive) {
		t.Fatalf("expected allow mutation for active")
	}
}
