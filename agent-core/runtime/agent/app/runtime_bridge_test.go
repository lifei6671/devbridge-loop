package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/events"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
)

type testPrioritizedControlChannel struct {
	lastFrame transport.PrioritizedControlFrame
	frames    []transport.PrioritizedControlFrame
	doneChan  chan struct{}
}

func newTestPrioritizedControlChannel() *testPrioritizedControlChannel {
	return &testPrioritizedControlChannel{
		doneChan: make(chan struct{}),
	}
}

func (channel *testPrioritizedControlChannel) WriteControlFrame(
	_ context.Context,
	frame transport.ControlFrame,
) error {
	channel.lastFrame = transport.PrioritizedControlFrame{
		Priority: transport.ControlMessagePriorityNormal,
		Frame:    frame,
	}
	channel.frames = append(channel.frames, channel.lastFrame)
	return nil
}

func (channel *testPrioritizedControlChannel) WritePrioritizedControlFrame(
	_ context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	channel.lastFrame = frame
	channel.frames = append(channel.frames, frame)
	return nil
}

func (channel *testPrioritizedControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	<-ctx.Done()
	return transport.ControlFrame{}, ctx.Err()
}

func (channel *testPrioritizedControlChannel) Close(_ context.Context) error {
	select {
	case <-channel.doneChan:
	default:
		close(channel.doneChan)
	}
	return nil
}

func (channel *testPrioritizedControlChannel) Done() <-chan struct{} {
	return channel.doneChan
}

func (channel *testPrioritizedControlChannel) Err() error {
	return nil
}

func (channel *testPrioritizedControlChannel) Frames() []transport.PrioritizedControlFrame {
	cloned := make([]transport.PrioritizedControlFrame, len(channel.frames))
	copy(cloned, channel.frames)
	return cloned
}

type handshakeControlChannel struct {
	lastFrame      transport.PrioritizedControlFrame
	frames         []transport.PrioritizedControlFrame
	doneChan       chan struct{}
	readQueue      chan transport.ControlFrame
	authAckSuccess bool

	lastHelloPayload pb.ConnectorHello
	lastAuthPayload  pb.ConnectorAuth
}

func newHandshakeControlChannel(authAckSuccess bool) *handshakeControlChannel {
	return &handshakeControlChannel{
		doneChan:       make(chan struct{}),
		readQueue:      make(chan transport.ControlFrame, 8),
		authAckSuccess: authAckSuccess,
	}
}

func (channel *handshakeControlChannel) WriteControlFrame(
	ctx context.Context,
	frame transport.ControlFrame,
) error {
	return channel.WritePrioritizedControlFrame(
		ctx,
		transport.PrioritizedControlFrame{
			Priority: transport.ControlMessagePriorityNormal,
			Frame:    frame,
		},
	)
}

func (channel *handshakeControlChannel) WritePrioritizedControlFrame(
	_ context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	channel.lastFrame = frame
	channel.frames = append(channel.frames, frame)
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frame.Frame)
	if err != nil {
		// 非业务控制帧（如 ping/pong）不参与握手应答。
		return nil
	}
	switch envelope.MessageType {
	case pb.ControlMessageConnectorHello:
		// 记录 hello 载荷，供测试断言发送内容。
		_ = json.Unmarshal(envelope.Payload, &channel.lastHelloPayload)
		welcomePayload, marshalErr := json.Marshal(pb.ConnectorWelcome{
			AssignedSessionEpoch: 42,
			HeartbeatIntervalSec: 5,
			SelectedBinding:      "tcp_framed",
		})
		if marshalErr != nil {
			return marshalErr
		}
		welcomeFrame, encodeErr := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
			VersionMajor: 2,
			VersionMinor: 1,
			MessageType:  pb.ControlMessageConnectorWelcome,
			ConnectorID:  envelope.ConnectorID,
			Payload:      welcomePayload,
		})
		if encodeErr != nil {
			return encodeErr
		}
		channel.readQueue <- welcomeFrame
	case pb.ControlMessageConnectorAuth:
		// 记录 auth 载荷，供测试断言 token/client_cap_version 是否按配置填充。
		_ = json.Unmarshal(envelope.Payload, &channel.lastAuthPayload)
		authAckPayload := pb.ConnectorAuthAck{
			Success:      channel.authAckSuccess,
			SessionID:    "session-auth-42",
			SessionEpoch: 42,
		}
		if !channel.authAckSuccess {
			authAckPayload.ErrorCode = ltfperrors.CodeAuthInvalidToken
			authAckPayload.ErrorMessage = "invalid token"
		}
		encodedAuthAck, marshalErr := json.Marshal(authAckPayload)
		if marshalErr != nil {
			return marshalErr
		}
		authAckFrame, encodeErr := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
			VersionMajor: 2,
			VersionMinor: 1,
			MessageType:  pb.ControlMessageConnectorAuthAck,
			ConnectorID:  envelope.ConnectorID,
			SessionID:    authAckPayload.SessionID,
			SessionEpoch: authAckPayload.SessionEpoch,
			Payload:      encodedAuthAck,
		})
		if encodeErr != nil {
			return encodeErr
		}
		channel.readQueue <- authAckFrame
	}
	return nil
}

func (channel *handshakeControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	select {
	case <-ctx.Done():
		return transport.ControlFrame{}, ctx.Err()
	case frame := <-channel.readQueue:
		return frame, nil
	}
}

func (channel *handshakeControlChannel) Close(_ context.Context) error {
	select {
	case <-channel.doneChan:
	default:
		close(channel.doneChan)
	}
	return nil
}

func (channel *handshakeControlChannel) Done() <-chan struct{} {
	return channel.doneChan
}

func (channel *handshakeControlChannel) Err() error {
	return nil
}

type runtimeBridgeTestHealthProbe struct {
	result pb.HealthStatus
}

func (probe *runtimeBridgeTestHealthProbe) Probe(
	_ context.Context,
	_ adapter.LocalRegistration,
	_ pb.ServiceEndpoint,
) (pb.HealthStatus, string) {
	if probe == nil {
		return pb.HealthStatusUnknown, "probe is nil"
	}
	return probe.result, "stub"
}

type runtimeBridgeBlockingHealthProbe struct {
	started chan struct{}
	release chan struct{}
}

func (probe *runtimeBridgeBlockingHealthProbe) Probe(
	ctx context.Context,
	_ adapter.LocalRegistration,
	_ pb.ServiceEndpoint,
) (pb.HealthStatus, string) {
	if probe == nil {
		return pb.HealthStatusUnknown, "probe is nil"
	}
	if probe.started != nil {
		select {
		case <-probe.started:
		default:
			close(probe.started)
		}
	}
	if probe.release != nil {
		select {
		case <-probe.release:
		case <-ctx.Done():
			return pb.HealthStatusUnknown, "probe canceled"
		}
	}
	return pb.HealthStatusHealthy, "released"
}

type testRefillScheduler struct {
	snapshot   tunnel.Snapshot
	lastTarget int
	lastReason string
	callCount  int
}

func (scheduler *testRefillScheduler) Snapshot() tunnel.Snapshot {
	return scheduler.snapshot
}

func (scheduler *testRefillScheduler) RequestRefill(targetIdle int, reason string) bool {
	scheduler.lastTarget = targetIdle
	scheduler.lastReason = reason
	scheduler.callCount++
	return true
}

type runtimeBridgeTestTunnel struct {
	tunnelID string
}

func (tunnel *runtimeBridgeTestTunnel) ID() string {
	return tunnel.tunnelID
}

// TestNextTunnelIDUsesSessionScopedPrefix 验证 tunnel_id 默认带 session 作用域，避免多 Agent 冲突。
func TestNextTunnelIDUsesSessionScopedPrefix(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		cfg: Config{
			AgentID: "agent-local",
		},
		bridgeSession: "session-a1:b2/c3",
	}

	firstTunnelID := runtime.nextTunnelID()
	secondTunnelID := runtime.nextTunnelID()
	runtime.bridgeSession = ""
	thirdTunnelID := runtime.nextTunnelID()

	if firstTunnelID != "tun-a1_b2_c3-1" {
		testingObject.Fatalf("unexpected first tunnel id: got=%s want=%s", firstTunnelID, "tun-a1_b2_c3-1")
	}
	if secondTunnelID != "tun-a1_b2_c3-2" {
		testingObject.Fatalf("unexpected second tunnel id: got=%s want=%s", secondTunnelID, "tun-a1_b2_c3-2")
	}
	if thirdTunnelID != "tun-agent-local-3" {
		testingObject.Fatalf("unexpected third tunnel id: got=%s want=%s", thirdTunnelID, "tun-agent-local-3")
	}
}

func (tunnel *runtimeBridgeTestTunnel) Close() error {
	_ = tunnel
	return nil
}

// testHasDiagnoseCode 判断 diagnose.logs 返回体中是否包含指定事件码。
func testHasDiagnoseCode(payload map[string]any, eventCode string) bool {
	items, ok := payload["items"].([]map[string]any)
	if !ok {
		return false
	}
	for _, item := range items {
		codeValue, _ := item["code"].(string)
		if codeValue == eventCode {
			return true
		}
	}
	return false
}

// TestComputeBridgeRetryBackoffWithJitter 验证指数退避基线为 1/2/4/8 秒。
func TestComputeBridgeRetryBackoffWithJitter(testingObject *testing.T) {
	testCases := []struct {
		failStreak uint32
		expected   time.Duration
	}{
		{failStreak: 0, expected: 0},
		{failStreak: 1, expected: time.Second},
		{failStreak: 2, expected: 2 * time.Second},
		{failStreak: 3, expected: 4 * time.Second},
		{failStreak: 4, expected: 8 * time.Second},
		{failStreak: 5, expected: 8 * time.Second},
	}

	for _, testCase := range testCases {
		actual := computeBridgeRetryBackoffWithJitter(testCase.failStreak, 0)
		if actual != testCase.expected {
			testingObject.Fatalf(
				"unexpected backoff fail_streak=%d got=%s want=%s",
				testCase.failStreak,
				actual,
				testCase.expected,
			)
		}
	}
}

// TestComputeBridgeRetryBackoffWithJitterClamp 验证抖动与上下界钳制策略。
func TestComputeBridgeRetryBackoffWithJitterClamp(testingObject *testing.T) {
	if got := computeBridgeRetryBackoffWithJitter(1, -1); got != time.Second {
		testingObject.Fatalf("expected first retry backoff to stay at 1s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(3, -1); got != 3200*time.Millisecond {
		testingObject.Fatalf("expected 4s with -20%% jitter to be 3.2s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(3, 1); got != 4800*time.Millisecond {
		testingObject.Fatalf("expected 4s with +20%% jitter to be 4.8s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(10, 1); got != 8*time.Second {
		testingObject.Fatalf("expected max retry backoff clamp to 8s, got=%s", got)
	}
}

// TestNextBridgeRetryFailStreak 验证预认证阶段失败会累计退避，ready 后才重置。
func TestNextBridgeRetryFailStreak(testingObject *testing.T) {
	testingObject.Parallel()

	failStreak := uint32(0)
	// connect 成功只表示阶段前进，不应重置失败计数。
	failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageConnect, true)
	if failStreak != 0 {
		testingObject.Fatalf("unexpected fail streak after connect success: got=%d want=0", failStreak)
	}
	// 第一次握手失败。
	failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageHandshake, false)
	if failStreak != 1 {
		testingObject.Fatalf("unexpected fail streak after first handshake failure: got=%d want=1", failStreak)
	}
	// 下一轮 connect 成功后，失败计数仍需保留，避免持续回退到 1s。
	failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageConnect, true)
	if failStreak != 1 {
		testingObject.Fatalf("unexpected fail streak after reconnect success: got=%d want=1", failStreak)
	}
	// 第二次握手失败，指数退避计数继续增加。
	failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageHandshake, false)
	if failStreak != 2 {
		testingObject.Fatalf("unexpected fail streak after second handshake failure: got=%d want=2", failStreak)
	}
	// 会话 ready 后才允许重置。
	failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageReady, true)
	if failStreak != 0 {
		testingObject.Fatalf("unexpected fail streak after ready success: got=%d want=0", failStreak)
	}
}

// TestSendControlHeartbeatPingWritesHighPriorityFrame 验证 ping 走高优先级控制帧。
func TestSendControlHeartbeatPingWritesHighPriorityFrame(testingObject *testing.T) {
	runtime := &Runtime{}
	controlChannel := newTestPrioritizedControlChannel()

	if err := runtime.sendControlHeartbeatPing(context.Background(), controlChannel); err != nil {
		testingObject.Fatalf("send heartbeat ping failed: %v", err)
	}
	if controlChannel.lastFrame.Priority != transport.ControlMessagePriorityHigh {
		testingObject.Fatalf("expected ping priority=high, got=%s", controlChannel.lastFrame.Priority)
	}
	if controlChannel.lastFrame.Frame.Type != transport.ControlFrameTypeHeartbeatPing {
		testingObject.Fatalf(
			"expected ping frame type=%d, got=%d",
			transport.ControlFrameTypeHeartbeatPing,
			controlChannel.lastFrame.Frame.Type,
		)
	}
	if len(controlChannel.lastFrame.Frame.Payload) != 0 {
		testingObject.Fatalf("expected empty ping payload")
	}
}

// TestPerformControlHandshakeBuildsHelloAndAuth 验证握手会发送 Hello/Auth 并回填权威会话。
func TestPerformControlHandshakeBuildsHelloAndAuth(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		cfg:            DefaultConfig(),
		controlChannel: newHandshakeControlChannel(true),
	}
	if err := runtime.performControlHandshake(context.Background()); err != nil {
		testingObject.Fatalf("perform control handshake failed: %v", err)
	}
	if runtime.bridgeSession != "session-auth-42" || runtime.bridgeEpoch != 42 {
		testingObject.Fatalf(
			"unexpected authoritative session: session_id=%s session_epoch=%d",
			runtime.bridgeSession,
			runtime.bridgeEpoch,
		)
	}
	handshakeChannel := runtime.controlChannel.(*handshakeControlChannel)
	if strings.TrimSpace(handshakeChannel.lastHelloPayload.ConnectorID) != strings.TrimSpace(runtime.cfg.AgentID) {
		testingObject.Fatalf("unexpected connector_id in hello payload: %s", handshakeChannel.lastHelloPayload.ConnectorID)
	}
	if strings.TrimSpace(handshakeChannel.lastAuthPayload.AuthMethod) != strings.TrimSpace(runtime.cfg.Session.AuthMethod) {
		testingObject.Fatalf("unexpected auth_method in auth payload: %s", handshakeChannel.lastAuthPayload.AuthMethod)
	}
	if strings.TrimSpace(handshakeChannel.lastAuthPayload.Token) != strings.TrimSpace(runtime.cfg.Session.AuthToken) {
		testingObject.Fatalf("unexpected token in auth payload")
	}
	if strings.TrimSpace(handshakeChannel.lastAuthPayload.ClientCapVersion) != strings.TrimSpace(runtime.cfg.Session.ClientCapVersion) {
		testingObject.Fatalf(
			"unexpected client_cap_version in auth payload: got=%s want=%s",
			handshakeChannel.lastAuthPayload.ClientCapVersion,
			runtime.cfg.Session.ClientCapVersion,
		)
	}
}

// TestPerformControlHandshakeAuthRejectDoesNotLeakToken 验证认证失败错误不会携带 token 明文。
func TestPerformControlHandshakeAuthRejectDoesNotLeakToken(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		cfg:            DefaultConfig(),
		controlChannel: newHandshakeControlChannel(false),
	}
	err := runtime.performControlHandshake(context.Background())
	if err == nil {
		testingObject.Fatalf("expected handshake error, got nil")
	}
	if strings.Contains(err.Error(), runtime.cfg.Session.AuthToken) {
		testingObject.Fatalf("unexpected token leakage in handshake error: %v", err)
	}
}

// TestSendControlHeartbeatPongWritesHighPriorityFrame 验证 pong 走高优先级控制帧。
func TestSendControlHeartbeatPongWritesHighPriorityFrame(testingObject *testing.T) {
	runtime := &Runtime{}
	controlChannel := newTestPrioritizedControlChannel()

	if err := runtime.sendControlHeartbeatPong(context.Background(), controlChannel); err != nil {
		testingObject.Fatalf("send heartbeat pong failed: %v", err)
	}
	if controlChannel.lastFrame.Priority != transport.ControlMessagePriorityHigh {
		testingObject.Fatalf("expected pong priority=high, got=%s", controlChannel.lastFrame.Priority)
	}
	if controlChannel.lastFrame.Frame.Type != transport.ControlFrameTypeHeartbeatPong {
		testingObject.Fatalf(
			"expected pong frame type=%d, got=%d",
			transport.ControlFrameTypeHeartbeatPong,
			controlChannel.lastFrame.Frame.Type,
		)
	}
	if len(controlChannel.lastFrame.Frame.Payload) != 0 {
		testingObject.Fatalf("expected empty pong payload")
	}
}

// TestHandleBridgeBusinessControlFrameTunnelRefillRequest 验证 Agent 可解析并处理补池请求控制消息。
func TestHandleBridgeBusinessControlFrameTunnelRefillRequest(testingObject *testing.T) {
	testingObject.Parallel()

	scheduler := &testRefillScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 2},
	}
	refillHandler, err := control.NewRefillHandler(scheduler, control.RefillHandlerConfig{MaxIdle: 32})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}
	refillHandler.SetSession("session-001", 9)
	runtime := &Runtime{
		refillHandler: refillHandler,
	}

	refillPayload := pb.TunnelRefillRequest{
		SessionID:          "session-001",
		SessionEpoch:       9,
		RequestID:          "req-001",
		RequestedIdleDelta: 3,
		Reason:             string(control.TunnelRefillReasonLowWatermark),
		TimestampUnix:      time.Now().UTC().Unix(),
		Metadata: map[string]string{
			"target_idle_count":   "5",
			"bridge_idle_count":   "11",
			"bridge_in_use_count": "2",
		},
	}
	encodedPayload, err := json.Marshal(refillPayload)
	if err != nil {
		testingObject.Fatalf("marshal refill payload failed: %v", err)
	}
	controlFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageTunnelRefillRequest,
		SessionID:    "session-001",
		SessionEpoch: 9,
		RequestID:    "req-001",
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode refill control frame failed: %v", err)
	}

	if err := runtime.handleBridgeBusinessControlFrame(context.Background(), controlFrame); err != nil {
		testingObject.Fatalf("handle refill control frame failed: %v", err)
	}
	if scheduler.lastTarget != 5 {
		testingObject.Fatalf("unexpected refill target: got=%d want=5", scheduler.lastTarget)
	}
	if scheduler.lastReason != string(control.TunnelRefillReasonLowWatermark) {
		testingObject.Fatalf(
			"unexpected refill reason: got=%s want=%s",
			scheduler.lastReason,
			control.TunnelRefillReasonLowWatermark,
		)
	}
	diagnoseLogsPayload := runtime.diagnoseLogsPayload()
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillRequestReceived) {
		testingObject.Fatalf("expected diagnose logs contain TUNNEL_REFILL_REQUEST_RECEIVED")
	}
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillApplied) {
		testingObject.Fatalf("expected diagnose logs contain TUNNEL_REFILL_APPLIED")
	}
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillExpansionCheck) {
		testingObject.Fatalf("expected diagnose logs contain TUNNEL_REFILL_EXPANSION_CHECK")
	}
}

// TestHandleBridgeBusinessControlFrameTunnelRefillRequestIgnoredWhenSatisfied
// 验证当当前 idle 已满足请求目标时，Refill 事件会标记为 ignored。
func TestHandleBridgeBusinessControlFrameTunnelRefillRequestIgnoredWhenSatisfied(testingObject *testing.T) {
	testingObject.Parallel()

	scheduler := &testRefillScheduler{
		snapshot: tunnel.Snapshot{IdleCount: 8},
	}
	refillHandler, err := control.NewRefillHandler(scheduler, control.RefillHandlerConfig{MaxIdle: 32})
	if err != nil {
		testingObject.Fatalf("new refill handler failed: %v", err)
	}
	refillHandler.SetSession("session-002", 10)
	runtime := &Runtime{
		refillHandler: refillHandler,
	}

	refillPayload := pb.TunnelRefillRequest{
		SessionID:          "session-002",
		SessionEpoch:       10,
		RequestID:          "req-ignored-1",
		RequestedIdleDelta: 4,
		Reason:             string(control.TunnelRefillReasonLowWatermark),
		TimestampUnix:      time.Now().UTC().Unix(),
		Metadata: map[string]string{
			"target_idle_count":   "8",
			"bridge_idle_count":   "24",
			"bridge_in_use_count": "0",
		},
	}
	encodedPayload, err := json.Marshal(refillPayload)
	if err != nil {
		testingObject.Fatalf("marshal refill payload failed: %v", err)
	}
	controlFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageTunnelRefillRequest,
		SessionID:    "session-002",
		SessionEpoch: 10,
		RequestID:    "req-ignored-1",
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode refill control frame failed: %v", err)
	}

	if err := runtime.handleBridgeBusinessControlFrame(context.Background(), controlFrame); err != nil {
		testingObject.Fatalf("handle refill control frame failed: %v", err)
	}
	if scheduler.callCount != 0 {
		testingObject.Fatalf("expected no refill schedule call, got=%d", scheduler.callCount)
	}
	diagnoseLogsPayload := runtime.diagnoseLogsPayload()
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillIgnored) {
		testingObject.Fatalf("expected diagnose logs contain TUNNEL_REFILL_IGNORED")
	}
	if testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillApplied) {
		testingObject.Fatalf("expected diagnose logs not contain TUNNEL_REFILL_APPLIED")
	}
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeTunnelRefillExpansionCheck) {
		testingObject.Fatalf("expected diagnose logs contain TUNNEL_REFILL_EXPANSION_CHECK")
	}
}

// TestHandleBridgeBusinessControlFrameControlError 验证控制面错误消息会写入 runtime 最近错误字段。
func TestHandleBridgeBusinessControlFrameControlError(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{}
	controlErrorPayload := pb.ControlError{
		Scope:   "bridge.control",
		Code:    "REFILL_REJECTED",
		Message: "refill request rejected by policy",
	}
	encodedPayload, err := json.Marshal(controlErrorPayload)
	if err != nil {
		testingObject.Fatalf("marshal control error payload failed: %v", err)
	}
	controlFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageControlError,
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode control error frame failed: %v", err)
	}

	if err := runtime.handleBridgeBusinessControlFrame(context.Background(), controlFrame); err != nil {
		testingObject.Fatalf("handle control error frame failed: %v", err)
	}
	sessionSnapshot := runtime.sessionSnapshot()
	if sessionSnapshot.lastError == "" {
		testingObject.Fatalf("expected last_error to be updated")
	}
	diagnoseLogsPayload := runtime.diagnoseLogsPayload()
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeBridgeControlError) {
		testingObject.Fatalf("expected diagnose logs contain BRIDGE_CONTROL_ERROR")
	}
}

// TestHandleBridgeBusinessControlFrameRouteAck 验证 route ack 会写入诊断日志，便于排查路由同步问题。
func TestHandleBridgeBusinessControlFrameRouteAck(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{}
	routeAssignAckPayload := pb.RouteAssignAck{
		Accepted:                false,
		RouteID:                 "agent-auto-route-svc-1",
		AcceptedResourceVersion: 11,
		CurrentResourceVersion:  12,
		ErrorCode:               "STALE_EPOCH_EVENT",
		ErrorMessage:            "session epoch mismatch for route event",
	}
	encodedAssignAckPayload, err := json.Marshal(routeAssignAckPayload)
	if err != nil {
		testingObject.Fatalf("marshal route assign ack payload failed: %v", err)
	}
	assignAckFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageRouteAssignAck,
		SessionID:    "session-ack",
		SessionEpoch: 3,
		RequestID:    "req-route-assign-ack",
		Payload:      encodedAssignAckPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode route assign ack frame failed: %v", err)
	}
	if err := runtime.handleBridgeBusinessControlFrame(context.Background(), assignAckFrame); err != nil {
		testingObject.Fatalf("handle route assign ack frame failed: %v", err)
	}

	routeRevokeAckPayload := pb.RouteRevokeAck{
		Accepted:                true,
		RouteID:                 "agent-auto-route-svc-1",
		AcceptedResourceVersion: 12,
		CurrentResourceVersion:  12,
	}
	encodedRevokeAckPayload, err := json.Marshal(routeRevokeAckPayload)
	if err != nil {
		testingObject.Fatalf("marshal route revoke ack payload failed: %v", err)
	}
	revokeAckFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageRouteRevokeAck,
		SessionID:    "session-ack",
		SessionEpoch: 3,
		RequestID:    "req-route-revoke-ack",
		Payload:      encodedRevokeAckPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode route revoke ack frame failed: %v", err)
	}
	if err := runtime.handleBridgeBusinessControlFrame(context.Background(), revokeAckFrame); err != nil {
		testingObject.Fatalf("handle route revoke ack frame failed: %v", err)
	}

	diagnoseLogsPayload := runtime.diagnoseLogsPayload()
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeRouteAssignRejected) {
		testingObject.Fatalf("expected diagnose logs contain ROUTE_ASSIGN_REJECTED")
	}
	if !testHasDiagnoseCode(diagnoseLogsPayload, events.CodeRouteRevokeAccepted) {
		testingObject.Fatalf("expected diagnose logs contain ROUTE_REVOKE_ACCEPTED")
	}
}

// TestInitTransportSupportsGRPCH2 验证 grpc_h2 已接入 runtime 初始化路径。
func TestInitTransportSupportsGRPCH2(testingObject *testing.T) {
	testingObject.Parallel()
	runtime := &Runtime{
		cfg: Config{
			BridgeTransport: transport.BindingTypeGRPCH2.String(),
			ControlChannel: ControlChannelConfig{
				DialTimeout: time.Second,
			},
		},
	}
	if err := runtime.initTransport(); err != nil {
		testingObject.Fatalf("init grpc_h2 transport failed: %v", err)
	}
	if runtime.grpcTransport == nil {
		testingObject.Fatalf("expected grpc transport initialized")
	}
}

// TestBridgeTunnelDialTimeoutUsesTCPTransportConfig 验证数据面 tunnel 拨号超时不再跟随控制面超时。
func TestBridgeTunnelDialTimeoutUsesTCPTransportConfig(testingObject *testing.T) {
	testingObject.Parallel()

	tcpTransport, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{
		DialTimeout: 17 * time.Second,
	})
	if err != nil {
		testingObject.Fatalf("new tcp transport failed: %v", err)
	}
	runtime := &Runtime{
		cfg: Config{
			ControlChannel: ControlChannelConfig{
				DialTimeout: 25 * time.Millisecond,
			},
		},
		tcpTransport: tcpTransport,
	}

	if got, want := runtime.bridgeTunnelDialTimeout(), 17*time.Second; got != want {
		testingObject.Fatalf("unexpected tunnel dial timeout: got=%s want=%s", got, want)
	}
}

// TestSyncServiceControlState 验证 ACTIVE 会话可发送服务发布与健康上报。
func TestSyncServiceControlState(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	serviceCatalog := service.NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	serviceCatalog.Upsert(now, adapter.LocalRegistration{
		LogicalServiceID: "svc-5001",
		InstanceID:       "inst-5001",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18080, ServerName: "order.demo.example.com"},
		},
	})
	runtime := &Runtime{
		controlChannel:   controlChannel,
		controlPublisher: control.NewPublisher("session-5001", 2, 0),
		serviceCatalog:   serviceCatalog,
		healthReporter: control.NewHealthReporter(control.HealthReporterOptions{
			Probe: &runtimeBridgeTestHealthProbe{result: pb.HealthStatusHealthy},
			Now:   func() time.Time { return now.Add(time.Second) },
		}),
	}

	if err := runtime.syncServiceControlState(context.Background()); err != nil {
		testingObject.Fatalf("sync service control state failed: %v", err)
	}
	frames := controlChannel.Frames()
	if len(frames) != 3 {
		testingObject.Fatalf("unexpected control frame count: got=%d want=3", len(frames))
	}
	firstEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode first business frame failed: %v", err)
	}
	if firstEnvelope.MessageType != pb.ControlMessageHeartbeat {
		testingObject.Fatalf("unexpected first message type: got=%s want=%s", firstEnvelope.MessageType, pb.ControlMessageHeartbeat)
	}
	secondEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[1].Frame)
	if err != nil {
		testingObject.Fatalf("decode second business frame failed: %v", err)
	}
	if secondEnvelope.MessageType != pb.ControlMessagePublishService {
		testingObject.Fatalf(
			"unexpected second message type: got=%s want=%s",
			secondEnvelope.MessageType,
			pb.ControlMessagePublishService,
		)
	}
	thirdEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[2].Frame)
	if err != nil {
		testingObject.Fatalf("decode third business frame failed: %v", err)
	}
	if thirdEnvelope.MessageType != pb.ControlMessageServiceHealthReport {
		testingObject.Fatalf(
			"unexpected third message type: got=%s want=%s",
			thirdEnvelope.MessageType,
			pb.ControlMessageServiceHealthReport,
		)
	}
	serviceListPayload := runtime.serviceListPayload()
	services, ok := serviceListPayload["services"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected service list payload type: %T", serviceListPayload["services"])
	}
	if len(services) != 1 {
		testingObject.Fatalf("unexpected service list size: got=%d want=1", len(services))
	}
	if services[0]["health_status"] != string(pb.HealthStatusHealthy) {
		testingObject.Fatalf("unexpected health_status in service.list: %+v", services[0]["health_status"])
	}
	if services[0]["sni_name"] != "order.demo.example.com" {
		testingObject.Fatalf("unexpected sni_name in service.list: %+v", services[0]["sni_name"])
	}
}

// TestSyncServiceControlStateSendsHeartbeatWithoutCatalog 验证空目录场景仍会上报业务心跳建立会话上下文。
func TestSyncServiceControlStateSendsHeartbeatWithoutCatalog(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	runtime := &Runtime{
		controlChannel:   controlChannel,
		controlPublisher: control.NewPublisher("session-empty", 5, 0),
		serviceCatalog:   service.NewCatalog(),
	}

	if err := runtime.syncServiceControlState(context.Background()); err != nil {
		testingObject.Fatalf("sync service control state failed: %v", err)
	}
	frames := controlChannel.Frames()
	if len(frames) != 1 {
		testingObject.Fatalf("unexpected control frame count: got=%d want=1", len(frames))
	}
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode heartbeat frame failed: %v", err)
	}
	if envelope.MessageType != pb.ControlMessageHeartbeat {
		testingObject.Fatalf("unexpected heartbeat message type: got=%s want=%s", envelope.MessageType, pb.ControlMessageHeartbeat)
	}
}

// TestAddOrUpdateServiceSyncsDuringSessionWarmup 验证会话 warmup 期间新增服务仍会触发控制面同步。
func TestAddOrUpdateServiceSyncsDuringSessionWarmup(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	now := time.Unix(1700001200, 0).UTC()
	runtime := &Runtime{
		controlChannel:     controlChannel,
		controlPublisher:   control.NewPublisher("session-8101", 4, 0),
		serviceCatalog:     service.NewCatalog(),
		healthReporter:     control.NewHealthReporter(control.HealthReporterOptions{Probe: &runtimeBridgeTestHealthProbe{result: pb.HealthStatusHealthy}, Now: func() time.Time { return now }}),
		bridgeState:        events.BridgeStateActive,
		bridgeSession:      "session-8101",
		bridgeEpoch:        4,
		bridgeSessionReady: false,
	}

	if _, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:  "inst-8101",
		Scope:       pb.Scope{Namespace: "dev", Environment: "demo"},
		ServiceName: "inventory-service",
		Protocol:    "http",
		Host:        "127.0.0.1",
		Port:        18081,
		SNIName:     "inventory.demo.example.com",
	}); err != nil {
		testingObject.Fatalf("add or update service failed: %v", err)
	}

	frames := controlChannel.Frames()
	if len(frames) != 3 {
		testingObject.Fatalf("unexpected control frame count during warmup sync: got=%d want=3", len(frames))
	}
	firstEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode first business frame failed: %v", err)
	}
	if firstEnvelope.MessageType != pb.ControlMessageHeartbeat {
		testingObject.Fatalf("unexpected first message type: got=%s want=%s", firstEnvelope.MessageType, pb.ControlMessageHeartbeat)
	}
	secondEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[1].Frame)
	if err != nil {
		testingObject.Fatalf("decode second business frame failed: %v", err)
	}
	if secondEnvelope.MessageType != pb.ControlMessagePublishService {
		testingObject.Fatalf(
			"unexpected second message type: got=%s want=%s",
			secondEnvelope.MessageType,
			pb.ControlMessagePublishService,
		)
	}
}

// TestAddOrUpdateServiceUsesDefaultHealthCheckConfig 验证 service.add 默认健康检查参数生效。
func TestAddOrUpdateServiceUsesDefaultHealthCheckConfig(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		serviceCatalog: service.NewCatalog(),
	}
	addedPayload, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:  "inst-default-health",
		Scope:       pb.Scope{Namespace: "dev", Environment: "demo"},
		ServiceName: "payment-service",
		Protocol:    "http",
		Host:        "127.0.0.1",
		Port:        18090,
	})
	if err != nil {
		testingObject.Fatalf("add or update service failed: %v", err)
	}
	if addedPayload["health_check_mode"] != "http" {
		testingObject.Fatalf("unexpected default health_check_mode: %+v", addedPayload["health_check_mode"])
	}
	if addedPayload["health_check_interval_sec"] != uint32(defaultServiceHealthCheckIntervalSec) {
		testingObject.Fatalf(
			"unexpected default health_check_interval_sec: %+v",
			addedPayload["health_check_interval_sec"],
		)
	}
	if addedPayload["health_check_path"] != "/" {
		testingObject.Fatalf("unexpected default health_check_path: %+v", addedPayload["health_check_path"])
	}
	records := runtime.serviceCatalog.List()
	if len(records) != 1 {
		testingObject.Fatalf("unexpected service catalog count: got=%d want=1", len(records))
	}
	if records[0].Registration.HealthCheck.Type != "http" {
		testingObject.Fatalf("unexpected catalog health_check.type: %+v", records[0].Registration.HealthCheck.Type)
	}
	if records[0].Registration.HealthCheck.IntervalSec != uint32(defaultServiceHealthCheckIntervalSec) {
		testingObject.Fatalf(
			"unexpected catalog health_check.interval_sec: %+v",
			records[0].Registration.HealthCheck.IntervalSec,
		)
	}
	if records[0].Registration.HealthCheck.Endpoint != "/" {
		testingObject.Fatalf("unexpected catalog health_check.endpoint: %+v", records[0].Registration.HealthCheck.Endpoint)
	}
}

// TestAddOrUpdateServiceRejectsMissingNamespace 验证缺失 namespace 会被拒绝。
func TestAddOrUpdateServiceRejectsMissingNamespace(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		serviceCatalog: service.NewCatalog(),
	}
	_, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:  "inst-missing-scope",
		ServiceName: "payment-service",
		Protocol:    "http",
		Host:        "127.0.0.1",
		Port:        18090,
	})
	if err == nil {
		testingObject.Fatalf("expected add service to reject missing namespace")
	}
	if !strings.Contains(err.Error(), "scope.namespace is required") {
		testingObject.Fatalf("unexpected error for missing namespace: %v", err)
	}
}

// TestAddOrUpdateServiceRejectsMissingEnvironment 验证缺失 environment 会被拒绝。
func TestAddOrUpdateServiceRejectsMissingEnvironment(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		serviceCatalog: service.NewCatalog(),
	}
	_, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:  "inst-missing-environment",
		ServiceName: "payment-service",
		Protocol:    "http",
		Scope: pb.Scope{
			Namespace: "dev",
		},
		Host: "127.0.0.1",
		Port: 18091,
	})
	if err == nil {
		testingObject.Fatalf("expected add service to reject missing environment")
	}
	if !strings.Contains(err.Error(), "scope.environment is required") {
		testingObject.Fatalf("unexpected error for missing environment: %v", err)
	}
}

// TestAddOrUpdateServiceDropsSNIForNonTLS 验证非 https upstream 不保留 SNI。
func TestAddOrUpdateServiceDropsSNIForNonTLS(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		serviceCatalog: service.NewCatalog(),
	}
	addedPayload, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:  "inst-drop-sni",
		ServiceName: "payment-service",
		Protocol:    "http",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Host:    "127.0.0.1",
		Port:    18091,
		SNIName: "pay.demo.example.com",
	})
	if err != nil {
		testingObject.Fatalf("add service failed: %v", err)
	}
	if addedPayload["sni_name"] != "" {
		testingObject.Fatalf("unexpected sni_name payload: %+v", addedPayload["sni_name"])
	}
	records := runtime.serviceCatalog.List()
	if len(records) != 1 {
		testingObject.Fatalf("unexpected service catalog count: got=%d want=1", len(records))
	}
	if records[0].Registration.Endpoints[0].ServerName != "" {
		testingObject.Fatalf("unexpected endpoint server_name: %+v", records[0].Registration.Endpoints[0].ServerName)
	}
}

// TestAddOrUpdateServiceRejectsInvalidRouteHint 验证非法 route_hint 会在本地入口被拦截。
func TestAddOrUpdateServiceRejectsInvalidRouteHint(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name      string
		routeHint pb.RouteHint
		errorCode string
	}{
		{
			name: "missing matcher name",
			routeHint: pb.RouteHint{
				MatchHeaders: []pb.HeaderMatcher{
					{Exact: "demo"},
				},
			},
			errorCode: ltfperrors.CodeMissingRequiredField,
		},
		{
			name: "invalid regex",
			routeHint: pb.RouteHint{
				MatchHeaders: []pb.HeaderMatcher{
					{Name: "x-tenant", Regex: "["},
				},
			},
			errorCode: ltfperrors.CodeUnsupportedValue,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()

			runtime := &Runtime{
				serviceCatalog: service.NewCatalog(),
			}
			_, err := runtime.addOrUpdateService(runtimeServiceAddInput{
				InstanceID:  "inst-invalid-route-hint",
				ServiceName: "order-service",
				Protocol:    "http",
				Scope: pb.Scope{
					Namespace:   "dev",
					Environment: "demo",
				},
				Host:      "127.0.0.1",
				Port:      18090,
				RouteHint: testCase.routeHint,
			})
			if err == nil {
				testingObject.Fatalf("expected add service to reject invalid route_hint")
			}
			if !ltfperrors.IsCode(err, testCase.errorCode) {
				testingObject.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestAddOrUpdateServiceValidatesHealthCheckMode 验证非法探测模式会被拒绝。
func TestAddOrUpdateServiceValidatesHealthCheckMode(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		serviceCatalog: service.NewCatalog(),
	}
	_, err := runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:             "inst-invalid-mode",
		Scope:                  pb.Scope{Namespace: "dev", Environment: "demo"},
		ServiceName:            "payment-service",
		Protocol:               "http",
		Host:                   "127.0.0.1",
		Port:                   18090,
		HealthCheckMode:        "grpc",
		HealthCheckPath:        "/healthz",
		HealthCheckIntervalSec: 10,
	})
	if err == nil {
		testingObject.Fatalf("expected add service to reject invalid health_check_mode")
	}
	if !strings.Contains(err.Error(), "invalid health_check_mode") {
		testingObject.Fatalf("unexpected error for invalid health_check_mode: %v", err)
	}
}

// TestReportCatalogHealthByIntervalRespectsServiceInterval 验证仅到期服务会触发健康上报。
func TestReportCatalogHealthByIntervalRespectsServiceInterval(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	serviceCatalog := service.NewCatalog()
	now := time.Now().UTC()
	serviceCatalog.Upsert(now.Add(-40*time.Second), adapter.LocalRegistration{
		LogicalServiceID: "svc-due",
		InstanceID:       "inst-due",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "svc-due",
		ServiceType: "tcp",
		HealthCheck: pb.HealthCheckConfig{
			Type:        "tcp",
			IntervalSec: 30,
		},
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-due", Protocol: "tcp", Host: "127.0.0.1", Port: 19001},
		},
	})
	serviceCatalog.Upsert(now.Add(-5*time.Second), adapter.LocalRegistration{
		LogicalServiceID: "svc-not-due",
		InstanceID:       "inst-not-due",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "svc-not-due",
		ServiceType: "tcp",
		HealthCheck: pb.HealthCheckConfig{
			Type:        "tcp",
			IntervalSec: 30,
		},
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-not-due", Protocol: "tcp", Host: "127.0.0.1", Port: 19002},
		},
	})
	runtime := &Runtime{
		controlChannel:   controlChannel,
		controlPublisher: control.NewPublisher("session-health-loop", 5, 0),
		serviceCatalog:   serviceCatalog,
		healthReporter: control.NewHealthReporter(control.HealthReporterOptions{
			Probe: &runtimeBridgeTestHealthProbe{result: pb.HealthStatusHealthy},
		}),
	}

	if err := runtime.reportCatalogHealthByInterval(context.Background()); err != nil {
		testingObject.Fatalf("report catalog health by interval failed: %v", err)
	}
	frames := controlChannel.Frames()
	if len(frames) != 1 {
		testingObject.Fatalf("unexpected health report frame count: got=%d want=1", len(frames))
	}
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode health report frame failed: %v", err)
	}
	var healthReport pb.ServiceHealthReport
	if err := json.Unmarshal(envelope.Payload, &healthReport); err != nil {
		testingObject.Fatalf("unmarshal health report payload failed: %v", err)
	}
	if healthReport.LogicalServiceID != "svc-due" {
		testingObject.Fatalf("unexpected due logical_service_id: %+v", healthReport.LogicalServiceID)
	}
	if healthReport.InstanceID != "inst-due" {
		testingObject.Fatalf("unexpected due instance_id: %+v", healthReport.InstanceID)
	}

	if err := runtime.reportCatalogHealthByInterval(context.Background()); err != nil {
		testingObject.Fatalf("second report catalog health by interval failed: %v", err)
	}
	frames = controlChannel.Frames()
	if len(frames) != 1 {
		testingObject.Fatalf("unexpected frame count after second due scan: got=%d want=1", len(frames))
	}
}

// TestWaitForActiveExitKeepsCommandResponsiveDuringHealthScan 验证健康扫描不会阻塞命令处理。
func TestWaitForActiveExitKeepsCommandResponsiveDuringHealthScan(testingObject *testing.T) {
	testingObject.Parallel()

	probeStarted := make(chan struct{})
	probeRelease := make(chan struct{})
	defer close(probeRelease)

	serviceCatalog := service.NewCatalog()
	serviceCatalog.Upsert(time.Now().UTC().Add(-2*time.Minute), adapter.LocalRegistration{
		LogicalServiceID: "svc-blocking-scan",
		InstanceID:       "inst-blocking-scan",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "svc-blocking-scan",
		ServiceType: "tcp",
		HealthCheck: pb.HealthCheckConfig{
			Type:        "tcp",
			IntervalSec: 1,
		},
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-blocking-scan", Protocol: "tcp", Host: "127.0.0.1", Port: 19011},
		},
	})

	runtime := &Runtime{
		cfg:               DefaultConfig(),
		bridgeCommandChan: make(chan bridgeCommand, 1),
		controlChannel:    newTestPrioritizedControlChannel(),
		controlPublisher:  control.NewPublisher("session-active-exit", 6, 0),
		serviceCatalog:    serviceCatalog,
		healthReporter: control.NewHealthReporter(control.HealthReporterOptions{
			Probe: &runtimeBridgeBlockingHealthProbe{
				started: probeStarted,
				release: probeRelease,
			},
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultChan := make(chan activeExitResult, 1)
	go func() {
		resultChan <- runtime.waitForActiveExit(ctx)
	}()

	select {
	case <-probeStarted:
	case <-time.After(3 * time.Second):
		testingObject.Fatalf("health scan did not start in time")
	}

	runtime.bridgeCommandChan <- bridgeCommand{
		kind:         bridgeCommandReconnect,
		resetBackoff: true,
	}
	select {
	case result := <-resultChan:
		if result.reason != activeExitReconnect {
			testingObject.Fatalf("unexpected active exit reason: got=%d want=%d", result.reason, activeExitReconnect)
		}
		if !result.resetBackoff {
			testingObject.Fatalf("expected reconnect to reset backoff")
		}
	case <-time.After(300 * time.Millisecond):
		testingObject.Fatalf("reconnect command blocked by health scan")
	}
}

// TestRemoveServicePublishesUnpublishWhenSessionActive 验证删除服务会下发 UnpublishService。
func TestRemoveServicePublishesUnpublishWhenSessionActive(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	serviceCatalog := service.NewCatalog()
	serviceCatalog.Upsert(time.Now().UTC(), adapter.LocalRegistration{
		LogicalServiceID: "svc-7001",
		InstanceID:       "inst-7001",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	runtime := &Runtime{
		controlChannel:     controlChannel,
		controlPublisher:   control.NewPublisher("session-7001", 1, 0),
		serviceCatalog:     serviceCatalog,
		bridgeState:        events.BridgeStateActive,
		bridgeSession:      "session-7001",
		bridgeEpoch:        1,
		bridgeSessionReady: false,
	}

	payload, err := runtime.removeService(runtimeServiceDeleteInput{LogicalServiceID: "svc-7001"})
	if err != nil {
		testingObject.Fatalf("remove service failed: %v", err)
	}
	if deleted, _ := payload["deleted"].(bool); !deleted {
		testingObject.Fatalf("expected deleted=true, got=%+v", payload["deleted"])
	}
	if len(runtime.serviceCatalog.List()) != 0 {
		testingObject.Fatalf("expected service catalog empty after delete")
	}
	frames := controlChannel.Frames()
	if len(frames) != 1 {
		testingObject.Fatalf("unexpected frame count after delete: got=%d want=1", len(frames))
	}
	unpublishEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode unpublish frame failed: %v", err)
	}
	if unpublishEnvelope.MessageType != pb.ControlMessageUnpublishService {
		testingObject.Fatalf(
			"unexpected first message type: got=%s want=%s",
			unpublishEnvelope.MessageType,
			pb.ControlMessageUnpublishService,
		)
	}
}

// TestSendTunnelPoolReport 验证 tunnel 池上报会写入 TunnelPoolReport 业务帧。
func TestSendTunnelPoolReport(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	runtime := &Runtime{
		cfg:                Config{AgentID: "agent-6001"},
		controlChannel:     controlChannel,
		controlPublisher:   control.NewPublisher("session-6001", 3, 0),
		bridgeState:        events.BridgeStateActive,
		bridgeSession:      "session-6001",
		bridgeEpoch:        3,
		bridgeSessionReady: true,
	}
	report := control.TunnelPoolReport{
		SessionID:       "session-6001",
		SessionEpoch:    3,
		IdleCount:       1,
		InUseCount:      4,
		TargetIdleCount: 8,
		Trigger:         "event:idle_low",
		Timestamp:       time.Unix(1700000600, 0).UTC(),
	}
	if err := runtime.SendTunnelPoolReport(context.Background(), report); err != nil {
		testingObject.Fatalf("send tunnel pool report failed: %v", err)
	}
	frames := controlChannel.Frames()
	if len(frames) != 1 {
		testingObject.Fatalf("unexpected control frame count: got=%d want=1", len(frames))
	}
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode tunnel pool report frame failed: %v", err)
	}
	if envelope.MessageType != pb.ControlMessageTunnelPoolReport {
		testingObject.Fatalf(
			"unexpected message type: got=%s want=%s",
			envelope.MessageType,
			pb.ControlMessageTunnelPoolReport,
		)
	}
	if envelope.ConnectorID != "agent-6001" {
		testingObject.Fatalf("unexpected connector_id: got=%s want=agent-6001", envelope.ConnectorID)
	}
	var payload pb.TunnelPoolReport
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		testingObject.Fatalf("unmarshal tunnel pool report payload failed: %v", err)
	}
	if payload.IdleCount != 1 || payload.TargetIdleCount != 8 {
		testingObject.Fatalf(
			"unexpected tunnel pool report payload: idle=%d target=%d",
			payload.IdleCount,
			payload.TargetIdleCount,
		)
	}
}

// TestTunnelListPayloadUsesRuntimeAssociation 验证 tunnel.list 可返回真实关联信息。
func TestTunnelListPayloadUsesRuntimeAssociation(testingObject *testing.T) {
	testingObject.Parallel()

	registry := tunnel.NewRegistry()
	now := time.Unix(1700001000, 0).UTC()
	added, err := registry.TryAddOpenedAsIdle(now, &runtimeBridgeTestTunnel{tunnelID: "tunnel-1"}, 4)
	if err != nil {
		testingObject.Fatalf("add idle tunnel failed: %v", err)
	}
	if !added {
		testingObject.Fatalf("expected tunnel added to registry")
	}
	runtime := &Runtime{
		cfg: Config{
			BridgeAddr:      "127.0.0.1:39080",
			BridgeTransport: "tcp_framed",
		},
		tunnelRegistry:     registry,
		tunnelAssociations: make(map[string]tunnelAssociation),
	}
	runtime.upsertTunnelAssociation(tunnelAssociation{
		TunnelID:              "tunnel-1",
		TrafficID:             "traffic-1",
		LogicalServiceID:      "ls-1",
		InstanceID:            "inst-1",
		LocalAddr:             "127.0.0.1:18080",
		OpenAckLatencyMS:      23,
		UpstreamDialLatencyMS: 7,
		UpdatedAt:             now.Add(time.Second),
	})

	payload := runtime.tunnelListPayload()
	tunnels, ok := payload["tunnels"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected tunnels payload type: %T", payload["tunnels"])
	}
	if len(tunnels) != 1 {
		testingObject.Fatalf("unexpected tunnel list size: got=%d want=1", len(tunnels))
	}
	item := tunnels[0]
	if item["logical_service_id"] != "ls-1" {
		testingObject.Fatalf("unexpected logical_service_id: %+v", item["logical_service_id"])
	}
	if item["instance_id"] != "inst-1" {
		testingObject.Fatalf("unexpected instance_id: %+v", item["instance_id"])
	}
	if item["local_addr"] != "127.0.0.1:18080" {
		testingObject.Fatalf("unexpected local_addr: %+v", item["local_addr"])
	}
	if item["remote_addr"] != "127.0.0.1:39080" {
		testingObject.Fatalf("unexpected remote_addr: %+v", item["remote_addr"])
	}
	if item["protocol"] != "tcp_framed" {
		testingObject.Fatalf("unexpected protocol: %+v", item["protocol"])
	}
	if item["latency_ms"] != uint64(23) {
		testingObject.Fatalf("unexpected latency_ms: %+v", item["latency_ms"])
	}
	if item["upstream_dial_latency_ms"] != uint64(7) {
		testingObject.Fatalf("unexpected upstream_dial_latency_ms: %+v", item["upstream_dial_latency_ms"])
	}
}

// TestTunnelListPayloadShowsActiveStateForGRPCH2 验证 grpc_h2 tunnel 处于 active 时可被 tunnel.list 正确展示。
func TestTunnelListPayloadShowsActiveStateForGRPCH2(testingObject *testing.T) {
	testingObject.Parallel()

	registry := tunnel.NewRegistry()
	now := time.Unix(1700001200, 0).UTC()
	added, err := registry.TryAddOpenedAsIdle(now, &runtimeBridgeTestTunnel{tunnelID: "tunnel-grpc-1"}, 4)
	if err != nil {
		testingObject.Fatalf("add grpc idle tunnel failed: %v", err)
	}
	if !added {
		testingObject.Fatalf("expected grpc tunnel added to registry")
	}
	if _, err := registry.ActivateIdleByID(now.Add(time.Millisecond), "tunnel-grpc-1"); err != nil {
		testingObject.Fatalf("activate grpc tunnel failed: %v", err)
	}

	runtime := &Runtime{
		cfg: Config{
			BridgeAddr:      "127.0.0.1:39081",
			BridgeTransport: "grpc_h2",
		},
		tunnelRegistry:     registry,
		tunnelAssociations: make(map[string]tunnelAssociation),
	}
	payload := runtime.tunnelListPayload()
	tunnels, ok := payload["tunnels"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected tunnels payload type: %T", payload["tunnels"])
	}
	if len(tunnels) != 1 {
		testingObject.Fatalf("unexpected tunnel list size: got=%d want=1", len(tunnels))
	}
	item := tunnels[0]
	if item["state"] != string(tunnel.StateActive) {
		testingObject.Fatalf("unexpected grpc tunnel state: got=%v want=%s", item["state"], tunnel.StateActive)
	}
	if item["protocol"] != "grpc_h2" {
		testingObject.Fatalf("unexpected grpc tunnel protocol: got=%v want=%s", item["protocol"], "grpc_h2")
	}
}

// TestTrafficStatsSnapshotPayloadUsesRuntimeMetrics 验证 traffic.stats.snapshot 返回 runtime 真实链路指标。
func TestTrafficStatsSnapshotPayloadUsesRuntimeMetrics(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	metrics.AddAgentTrafficUploadBytes(4096)
	metrics.AddAgentTrafficDownloadBytes(8192)
	runtime := &Runtime{
		metrics:             metrics,
		trafficStatsLastAt:  time.Now().UTC().Add(-2 * time.Second),
		trafficUploadLast:   1024,
		trafficDownloadLast: 2048,
	}

	payload := runtime.trafficStatsSnapshotPayload()
	if payload["source"] != "agent.runtime.traffic" {
		testingObject.Fatalf("unexpected source: %+v", payload["source"])
	}
	if payload["upload_total_bytes"] != uint64(4096) {
		testingObject.Fatalf("unexpected upload_total_bytes: %+v", payload["upload_total_bytes"])
	}
	if payload["download_total_bytes"] != uint64(8192) {
		testingObject.Fatalf("unexpected download_total_bytes: %+v", payload["download_total_bytes"])
	}
	sampleWindowMS, ok := payload["sample_window_ms"].(uint64)
	if !ok {
		testingObject.Fatalf("unexpected sample_window_ms type: %T", payload["sample_window_ms"])
	}
	if sampleWindowMS == 0 {
		testingObject.Fatalf("expected sample window > 0")
	}
	uploadBytesPerSec, ok := payload["upload_bytes_per_sec"].(float64)
	if !ok {
		testingObject.Fatalf("unexpected upload_bytes_per_sec type: %T", payload["upload_bytes_per_sec"])
	}
	if uploadBytesPerSec <= 0 {
		testingObject.Fatalf("expected upload_bytes_per_sec > 0, got=%f", uploadBytesPerSec)
	}
	downloadBytesPerSec, ok := payload["download_bytes_per_sec"].(float64)
	if !ok {
		testingObject.Fatalf("unexpected download_bytes_per_sec type: %T", payload["download_bytes_per_sec"])
	}
	if downloadBytesPerSec <= 0 {
		testingObject.Fatalf("expected download_bytes_per_sec > 0, got=%f", downloadBytesPerSec)
	}
}

// TestDiagnoseSnapshotPayloadUsesEventSource 验证 diagnose.snapshot 聚合 runtime 事件源。
func TestDiagnoseSnapshotPayloadUsesEventSource(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := &Runtime{
		cfg: Config{
			AgentID: "agent-u4",
		},
	}
	runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:   events.EventInfo,
		Module:  events.ModuleAgentRuntimeBridge,
		Code:    events.CodeBridgeStateActive,
		Message: "bridge active",
	})
	runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:   events.EventWarn,
		Module:  events.ModuleAgentRuntimeBridge,
		Code:    events.CodeBridgeRetryScheduled,
		Message: "retry scheduled",
	})
	runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:   events.EventInfo,
		Module:  events.ModuleAgentRuntimeRefill,
		Code:    events.CodeTunnelRefillApplied,
		Message: "refill applied",
	})
	runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:   events.EventError,
		Module:  events.ModuleAgentRuntimeControl,
		Code:    events.CodeBridgeControlError,
		Message: "control error",
	})

	payload := runtime.diagnoseSnapshotPayload()
	if payload["event_total"] != uint64(4) {
		testingObject.Fatalf("unexpected event_total: %+v", payload["event_total"])
	}
	if payload["event_error_count"] != uint64(1) {
		testingObject.Fatalf("unexpected event_error_count: %+v", payload["event_error_count"])
	}
	if payload["event_state_changes"] != uint64(1) {
		testingObject.Fatalf("unexpected event_state_changes: %+v", payload["event_state_changes"])
	}
	if payload["event_reconnects"] != uint64(1) {
		testingObject.Fatalf("unexpected event_reconnects: %+v", payload["event_reconnects"])
	}
	if payload["event_refill_total"] != uint64(1) {
		testingObject.Fatalf("unexpected event_refill_total: %+v", payload["event_refill_total"])
	}
	lastEventCode, _ := payload["last_event_code"].(string)
	if strings.TrimSpace(lastEventCode) == "" {
		testingObject.Fatalf("expected last_event_code is not empty")
	}
	if payload["source"] != "agent.runtime.diagnose" {
		testingObject.Fatalf("unexpected diagnose source: %+v", payload["source"])
	}
}
