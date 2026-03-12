package transport

import (
	"context"
	"errors"
	"testing"
)

// mockControlChannel 是测试用控制面实现。
type mockControlChannel struct {
	doneChannel chan struct{}
	lastError   error
}

// WriteControlFrame 实现控制帧写入。
func (channel *mockControlChannel) WriteControlFrame(ctx context.Context, frame ControlFrame) error {
	// 测试桩不做真实发送，直接返回成功。
	return nil
}

// ReadControlFrame 实现控制帧读取。
func (channel *mockControlChannel) ReadControlFrame(ctx context.Context) (ControlFrame, error) {
	// 测试桩返回空帧，满足接口契约即可。
	return ControlFrame{}, nil
}

// Close 关闭控制面通道。
func (channel *mockControlChannel) Close(ctx context.Context) error {
	// 记录关闭错误类型，便于调试。
	channel.lastError = ErrClosed
	return nil
}

// Done 返回控制面结束信号。
func (channel *mockControlChannel) Done() <-chan struct{} {
	// 返回预置通道用于接口兼容。
	return channel.doneChannel
}

// Err 返回最近错误。
func (channel *mockControlChannel) Err() error {
	// 返回测试期间记录的错误。
	return channel.lastError
}

// mockTunnelProducer 是测试用 tunnel producer。
type mockTunnelProducer struct{}

// OpenTunnel 打开一条测试 tunnel。
func (producer *mockTunnelProducer) OpenTunnel(ctx context.Context) (Tunnel, error) {
	// 测试中不需要真实 tunnel，返回 unsupported 即可。
	return nil, ErrUnsupported
}

// mockTunnelAcceptor 是测试用 tunnel acceptor。
type mockTunnelAcceptor struct{}

// AcceptTunnel 接收一条测试 tunnel。
func (acceptor *mockTunnelAcceptor) AcceptTunnel(ctx context.Context) (Tunnel, error) {
	// 测试中不需要真实 tunnel，返回 unsupported 即可。
	return nil, ErrUnsupported
}

// TestInMemorySessionOpenAndCapability 验证会话打开流程和能力查询错误语义。
func TestInMemorySessionOpenAndCapability(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{SessionID: "session-1"},
		BindingInfo{Type: BindingTypeGRPCH2},
		SessionCapabilities{
			ControlChannel: &mockControlChannel{doneChannel: make(chan struct{})},
			Producer:       &mockTunnelProducer{},
			Acceptor:       &mockTunnelAcceptor{},
			Pool:           NewInMemoryTunnelPool(),
		},
	)

	if _, err := session.Control(); !errors.Is(err, ErrNotReady) {
		testingObject.Fatalf("expected ErrNotReady before open, got %v", err)
	}

	if err := session.Open(context.Background()); err != nil {
		testingObject.Fatalf("open session failed: %v", err)
	}
	if state := session.State(); state != SessionStateAuthenticated {
		testingObject.Fatalf("expected authenticated state, got %s", state)
	}
	if protocolState := session.ProtocolState(); protocolState != ProtocolStateActive {
		testingObject.Fatalf("expected ACTIVE protocol state, got %s", protocolState)
	}

	if _, err := session.Control(); err != nil {
		testingObject.Fatalf("expected control capability after open, got %v", err)
	}
	if _, err := session.TunnelProducer(); err != nil {
		testingObject.Fatalf("expected tunnel producer capability after open, got %v", err)
	}

	if err := session.Close(context.Background(), nil); err != nil {
		testingObject.Fatalf("close session failed: %v", err)
	}
	select {
	case <-session.Done():
		// 关闭后 done 应立即可读。
	default:
		testingObject.Fatalf("expected done channel closed")
	}
}

// TestInMemorySessionMarkFailedMapping 验证 failed 映射规则。
func TestInMemorySessionMarkFailedMapping(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{SessionID: "session-2"},
		BindingInfo{Type: BindingTypeGRPCH2},
		SessionCapabilities{},
	)

	if err := session.MarkFailed(errors.New("connect failed"), true, false); err != nil {
		testingObject.Fatalf("mark failed (retrying) failed: %v", err)
	}
	if protocolState := session.ProtocolState(); protocolState != ProtocolStateConnecting {
		testingObject.Fatalf("expected CONNECTING on retrying failed, got %s", protocolState)
	}

	if err := session.Open(context.Background()); err != nil {
		testingObject.Fatalf("open session failed: %v", err)
	}
	if err := session.MarkFailed(errors.New("heartbeat timeout"), false, false); err != nil {
		testingObject.Fatalf("mark failed after auth failed: %v", err)
	}
	if protocolState := session.ProtocolState(); protocolState != ProtocolStateStale {
		testingObject.Fatalf("expected STALE after authenticated failure, got %s", protocolState)
	}
}

// TestInMemorySessionUnsupportedCapability 验证能力缺失时返回 ErrUnsupported。
func TestInMemorySessionUnsupportedCapability(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{SessionID: "session-3"},
		BindingInfo{Type: BindingTypeTCPFramed},
		SessionCapabilities{},
	)

	if _, err := session.Control(); !errors.Is(err, ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported for control, got %v", err)
	}
	if _, err := session.TunnelProducer(); !errors.Is(err, ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported for producer, got %v", err)
	}
	if _, err := session.TunnelAcceptor(); !errors.Is(err, ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported for acceptor, got %v", err)
	}
	if _, err := session.TunnelPool(); !errors.Is(err, ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported for pool, got %v", err)
	}
}

// TestInMemorySessionCloseBeforeAuthenticated 验证预认证阶段也能正常关闭 session。
func TestInMemorySessionCloseBeforeAuthenticated(testingObject *testing.T) {
	testCases := []struct {
		name  string
		state SessionState
	}{
		{name: "idle", state: SessionStateIdle},
		{name: "connecting", state: SessionStateConnecting},
		{name: "connected", state: SessionStateConnected},
		{name: "control ready", state: SessionStateControlReady},
	}

	for _, testCase := range testCases {
		testingObject.Run(testCase.name, func(subTestingObject *testing.T) {
			session := NewInMemorySession(
				SessionMeta{SessionID: "session-close-" + testCase.name},
				BindingInfo{Type: BindingTypeGRPCH2},
				SessionCapabilities{},
			)
			session.state = testCase.state

			if err := session.Close(context.Background(), nil); err != nil {
				subTestingObject.Fatalf("close session failed from state %s: %v", testCase.state, err)
			}
			if state := session.State(); state != SessionStateClosed {
				subTestingObject.Fatalf("expected closed state, got %s", state)
			}
			select {
			case <-session.Done():
			default:
				subTestingObject.Fatalf("expected done channel closed")
			}
		})
	}
}

// TestInMemorySessionOpenRejectedAfterGiveUp 验证 giveUp 失败后的 session 不允许重新打开。
func TestInMemorySessionOpenRejectedAfterGiveUp(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{SessionID: "session-4"},
		BindingInfo{Type: BindingTypeGRPCH2},
		SessionCapabilities{},
	)

	if err := session.MarkFailed(errors.New("fatal connect failure"), false, true); err != nil {
		testingObject.Fatalf("mark failed with giveUp failed: %v", err)
	}
	select {
	case <-session.Done():
	default:
		testingObject.Fatalf("expected done channel closed after giveUp")
	}

	if err := session.Open(context.Background()); !errors.Is(err, ErrSessionClosed) {
		testingObject.Fatalf("expected ErrSessionClosed when reopening giveUp session, got %v", err)
	}
	if state := session.State(); state != SessionStateFailed {
		testingObject.Fatalf("expected failed state preserved, got %s", state)
	}
}
