package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
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

type testRefillScheduler struct {
	snapshot   tunnel.Snapshot
	lastTarget int
	lastReason string
}

func (scheduler *testRefillScheduler) Snapshot() tunnel.Snapshot {
	return scheduler.snapshot
}

func (scheduler *testRefillScheduler) RequestRefill(targetIdle int, reason string) bool {
	scheduler.lastTarget = targetIdle
	scheduler.lastReason = reason
	return true
}

type runtimeBridgeTestTunnel struct {
	tunnelID string
}

func (tunnel *runtimeBridgeTestTunnel) ID() string {
	return tunnel.tunnelID
}

func (tunnel *runtimeBridgeTestTunnel) Close() error {
	_ = tunnel
	return nil
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

// TestSyncServiceControlState 验证 ACTIVE 会话可发送服务发布与健康上报。
func TestSyncServiceControlState(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	serviceCatalog := service.NewCatalog()
	now := time.Unix(1700000000, 0).UTC()
	serviceCatalog.Upsert(now, adapter.LocalRegistration{
		ServiceID:   "svc-5001",
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18080},
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
	if len(frames) != 2 {
		testingObject.Fatalf("unexpected control frame count: got=%d want=2", len(frames))
	}
	firstEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[0].Frame)
	if err != nil {
		testingObject.Fatalf("decode first business frame failed: %v", err)
	}
	if firstEnvelope.MessageType != pb.ControlMessagePublishService {
		testingObject.Fatalf("unexpected first message type: got=%s want=%s", firstEnvelope.MessageType, pb.ControlMessagePublishService)
	}
	secondEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(frames[1].Frame)
	if err != nil {
		testingObject.Fatalf("decode second business frame failed: %v", err)
	}
	if secondEnvelope.MessageType != pb.ControlMessageServiceHealthReport {
		testingObject.Fatalf(
			"unexpected second message type: got=%s want=%s",
			secondEnvelope.MessageType,
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
}

// TestSendTunnelPoolReport 验证 tunnel 池上报会写入 TunnelPoolReport 业务帧。
func TestSendTunnelPoolReport(testingObject *testing.T) {
	testingObject.Parallel()

	controlChannel := newTestPrioritizedControlChannel()
	runtime := &Runtime{
		cfg:              Config{AgentID: "agent-6001"},
		controlChannel:   controlChannel,
		controlPublisher: control.NewPublisher("session-6001", 3, 0),
		bridgeState:      "ACTIVE",
		bridgeSession:    "session-6001",
		bridgeEpoch:      3,
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
			BridgeAddr: "127.0.0.1:39080",
		},
		tunnelRegistry:     registry,
		tunnelAssociations: make(map[string]tunnelAssociation),
	}
	runtime.upsertTunnelAssociation(tunnelAssociation{
		TunnelID:              "tunnel-1",
		TrafficID:             "traffic-1",
		ServiceID:             "svc-1",
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
	if item["service_id"] != "svc-1" {
		testingObject.Fatalf("unexpected service_id: %+v", item["service_id"])
	}
	if item["local_addr"] != "127.0.0.1:18080" {
		testingObject.Fatalf("unexpected local_addr: %+v", item["local_addr"])
	}
	if item["remote_addr"] != "127.0.0.1:39080" {
		testingObject.Fatalf("unexpected remote_addr: %+v", item["remote_addr"])
	}
	if item["latency_ms"] != uint64(23) {
		testingObject.Fatalf("unexpected latency_ms: %+v", item["latency_ms"])
	}
	if item["upstream_dial_latency_ms"] != uint64(7) {
		testingObject.Fatalf("unexpected upstream_dial_latency_ms: %+v", item["upstream_dial_latency_ms"])
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
