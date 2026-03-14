package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestServeControlChannelReplyHeartbeatPong 验证 Bridge 在收到 ping 后立即回 pong。
func TestServeControlChannelReplyHeartbeatPong(testingObject *testing.T) {
	testingObject.Parallel()

	binding, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new tcp binding failed: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	serverControl, err := binding.OpenControlChannel(serverConn)
	if err != nil {
		testingObject.Fatalf("open server control channel failed: %v", err)
	}
	clientControl, err := binding.OpenControlChannel(clientConn)
	if err != nil {
		testingObject.Fatalf("open client control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveControlChannel(ctx, serverControl)
	}()

	if err := clientControl.WriteControlFrame(ctx, transport.ControlFrame{
		Type: transport.ControlFrameTypeHeartbeatPing,
	}); err != nil {
		testingObject.Fatalf("write heartbeat ping failed: %v", err)
	}

	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	replyFrame, err := clientControl.ReadControlFrame(readContext)
	if err != nil {
		testingObject.Fatalf("read heartbeat pong failed: %v", err)
	}
	if replyFrame.Type != transport.ControlFrameTypeHeartbeatPong {
		testingObject.Fatalf(
			"unexpected heartbeat reply type: got=%d want=%d",
			replyFrame.Type,
			transport.ControlFrameTypeHeartbeatPong,
		)
	}

	cancel()
	_ = clientControl.Close(context.Background())

	select {
	case doneErr := <-serverDone:
		if doneErr != nil && !errors.Is(doneErr, context.Canceled) && !isControlChannelClosedError(doneErr) {
			testingObject.Fatalf("serve control channel stopped with error: %v", doneErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("serve control channel did not stop in time")
	}
}

// TestServeControlChannelHandlePublishService 验证 Bridge 控制面可处理 PublishService 并返回 ACK。
func TestServeControlChannelHandlePublishService(testingObject *testing.T) {
	testingObject.Parallel()

	binding, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new tcp binding failed: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	serverControl, err := binding.OpenControlChannel(serverConn)
	if err != nil {
		testingObject.Fatalf("open server control channel failed: %v", err)
	}
	clientControl, err := binding.OpenControlChannel(clientConn)
	if err != nil {
		testingObject.Fatalf("open client control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveControlChannelWithDispatcher(
			ctx,
			serverControl,
			newControlMessageDispatcher(controlMessageDispatcherOptions{}),
		)
	}()

	publishPayload := pb.PublishService{
		ServiceID:   "svc-001",
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	}
	encodedPublishPayload, err := json.Marshal(publishPayload)
	if err != nil {
		testingObject.Fatalf("marshal publish payload failed: %v", err)
	}
	publishFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-001",
		SessionEpoch:    1,
		RequestID:       "req-001",
		EventID:         "evt-001",
		ResourceType:    "service",
		ResourceID:      "svc-001",
		ResourceVersion: 1,
		Payload:         encodedPublishPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode publish frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, publishFrame); err != nil {
		testingObject.Fatalf("write publish frame failed: %v", err)
	}

	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	replyFrame, err := clientControl.ReadControlFrame(readContext)
	if err != nil {
		testingObject.Fatalf("read publish ack frame failed: %v", err)
	}
	if replyFrame.Type != transport.ControlFrameTypePublishServiceAck {
		testingObject.Fatalf(
			"unexpected publish ack frame type: got=%d want=%d",
			replyFrame.Type,
			transport.ControlFrameTypePublishServiceAck,
		)
	}
	replyEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(replyFrame)
	if err != nil {
		testingObject.Fatalf("decode publish ack envelope failed: %v", err)
	}
	var publishAck pb.PublishServiceAck
	if err := json.Unmarshal(replyEnvelope.Payload, &publishAck); err != nil {
		testingObject.Fatalf("unmarshal publish ack payload failed: %v", err)
	}
	if !publishAck.Accepted {
		testingObject.Fatalf("expected publish ack accepted, got error=%s", publishAck.ErrorCode)
	}

	cancel()
	_ = clientControl.Close(context.Background())

	select {
	case doneErr := <-serverDone:
		if doneErr != nil && !errors.Is(doneErr, context.Canceled) && !isControlChannelClosedError(doneErr) {
			testingObject.Fatalf("serve control channel stopped with error: %v", doneErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("serve control channel did not stop in time")
	}
}

// TestServeGRPCControlChannelReplyHeartbeatPong 验证 grpc_h2 控制流收到 ping 后立即回 pong。
func TestServeGRPCControlChannelReplyHeartbeatPong(testingObject *testing.T) {
	testingObject.Parallel()

	listener := bufconn.Listen(1024 * 1024)
	grpcTransport, err := grpcbinding.NewTransportWithConfig(grpcbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new grpc transport failed: %v", err)
	}
	server := grpc.NewServer(grpcTransport.ServerOptions()...)
	transportgen.RegisterGRPCH2TransportServiceServer(server, &grpcControlPlaneService{})
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	clientConn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		testingObject.Fatalf("dial grpc server failed: %v", err)
	}
	defer func() {
		_ = clientConn.Close()
	}()

	client := transportgen.NewGRPCH2TransportServiceClient(clientConn)
	controlChannel, err := grpcTransport.OpenControlChannel(ctx, client)
	if err != nil {
		testingObject.Fatalf("open grpc control channel failed: %v", err)
	}
	defer func() {
		_ = controlChannel.Close(context.Background())
	}()

	if err := controlChannel.WriteControlFrame(ctx, transport.ControlFrame{
		Type: transport.ControlFrameTypeHeartbeatPing,
	}); err != nil {
		testingObject.Fatalf("write grpc heartbeat ping failed: %v", err)
	}
	replyFrame, err := controlChannel.ReadControlFrame(ctx)
	if err != nil {
		testingObject.Fatalf("read grpc heartbeat pong failed: %v", err)
	}
	if replyFrame.Type != transport.ControlFrameTypeHeartbeatPong {
		testingObject.Fatalf(
			"unexpected grpc heartbeat reply type: got=%d want=%d",
			replyFrame.Type,
			transport.ControlFrameTypeHeartbeatPong,
		)
	}

	publishPayload := pb.PublishService{
		ServiceID:   "svc-002",
		ServiceKey:  "dev/demo/pay-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "pay-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	}
	encodedPublishPayload, err := json.Marshal(publishPayload)
	if err != nil {
		testingObject.Fatalf("marshal grpc publish payload failed: %v", err)
	}
	publishFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-002",
		SessionEpoch:    1,
		RequestID:       "req-002",
		EventID:         "evt-002",
		ResourceType:    "service",
		ResourceID:      "svc-002",
		ResourceVersion: 1,
		Payload:         encodedPublishPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode grpc publish frame failed: %v", err)
	}
	if err := controlChannel.WriteControlFrame(ctx, publishFrame); err != nil {
		testingObject.Fatalf("write grpc publish frame failed: %v", err)
	}
	publishAckFrame, err := controlChannel.ReadControlFrame(ctx)
	if err != nil {
		testingObject.Fatalf("read grpc publish ack frame failed: %v", err)
	}
	if publishAckFrame.Type != transport.ControlFrameTypePublishServiceAck {
		testingObject.Fatalf(
			"unexpected grpc publish ack frame type: got=%d want=%d",
			publishAckFrame.Type,
			transport.ControlFrameTypePublishServiceAck,
		)
	}
	publishAckEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(publishAckFrame)
	if err != nil {
		testingObject.Fatalf("decode grpc publish ack envelope failed: %v", err)
	}
	var publishAck pb.PublishServiceAck
	if err := json.Unmarshal(publishAckEnvelope.Payload, &publishAck); err != nil {
		testingObject.Fatalf("unmarshal grpc publish ack payload failed: %v", err)
	}
	if !publishAck.Accepted {
		testingObject.Fatalf("expected grpc publish ack accepted, got error=%s", publishAck.ErrorCode)
	}
}

// TestControlMessageDispatcherHandleServiceHealthReport 验证健康上报可更新服务注册表。
func TestControlMessageDispatcherHandleServiceHealthReport(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-2001",
		ConnectorID: "connector-1",
		Epoch:       2,
		State:       registry.SessionActive,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(time.Now().UTC(), pb.Service{
		ServiceID:       "svc-2001",
		ServiceKey:      "dev/demo/order-service",
		Namespace:       "dev",
		Environment:     "demo",
		ServiceName:     "order-service",
		Status:          pb.ServiceStatusActive,
		ResourceVersion: 1,
		HealthStatus:    pb.HealthStatusUnknown,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
	})

	healthPayload := pb.ServiceHealthReport{
		ServiceID:           "svc-2001",
		ServiceKey:          "dev/demo/order-service",
		ServiceHealthStatus: pb.HealthStatusUnhealthy,
		CheckTimeUnix:       time.Now().UTC().Unix(),
	}
	encodedPayload, err := json.Marshal(healthPayload)
	if err != nil {
		testingObject.Fatalf("marshal health payload failed: %v", err)
	}
	replyEnvelope, err := dispatcher.dispatchEnvelope(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-2001",
		SessionEpoch: 2,
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("dispatch health envelope failed: %v", err)
	}
	if replyEnvelope != nil {
		testingObject.Fatalf("service health report should not generate ack envelope")
	}

	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-2001")
	if !exists {
		testingObject.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.HealthStatus != pb.HealthStatusUnhealthy {
		testingObject.Fatalf(
			"unexpected health status: got=%s want=%s",
			serviceSnapshot.HealthStatus,
			pb.HealthStatusUnhealthy,
		)
	}
}

// TestControlMessageDispatcherHandleTunnelPoolReport 验证 tunnel 池上报可触发补池请求。
func TestControlMessageDispatcherHandleTunnelPoolReport(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-3001",
		ConnectorID: "connector-1",
		Epoch:       7,
		State:       registry.SessionActive,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
	})

	reportPayload := pb.TunnelPoolReport{
		SessionID:       "session-3001",
		SessionEpoch:    7,
		IdleCount:       0,
		InUseCount:      5,
		TargetIdleCount: 8,
		Trigger:         "event:idle_low",
		TimestampUnix:   time.Now().UTC().Unix(),
	}
	encodedPayload, err := json.Marshal(reportPayload)
	if err != nil {
		testingObject.Fatalf("marshal tunnel report payload failed: %v", err)
	}
	replyEnvelope, err := dispatcher.dispatchEnvelope(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-3001",
		SessionEpoch: 7,
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("dispatch tunnel pool report failed: %v", err)
	}
	if replyEnvelope == nil {
		testingObject.Fatalf("expected refill request envelope")
	}
	if replyEnvelope.MessageType != pb.ControlMessageTunnelRefillRequest {
		testingObject.Fatalf(
			"unexpected reply message type: got=%s want=%s",
			replyEnvelope.MessageType,
			pb.ControlMessageTunnelRefillRequest,
		)
	}
	var refillRequest pb.TunnelRefillRequest
	if err := json.Unmarshal(replyEnvelope.Payload, &refillRequest); err != nil {
		testingObject.Fatalf("unmarshal refill payload failed: %v", err)
	}
	if refillRequest.RequestedIdleDelta <= 0 {
		testingObject.Fatalf("unexpected refill delta: %d", refillRequest.RequestedIdleDelta)
	}
	if refillRequest.SessionID != "session-3001" || refillRequest.SessionEpoch != 7 {
		testingObject.Fatalf("unexpected refill session fields: %+v", refillRequest)
	}
}

type controlPlaneLifecycleTestTunnel struct {
	tunnelID string
	closed   bool
}

func (tunnel *controlPlaneLifecycleTestTunnel) ID() string {
	return tunnel.tunnelID
}

func (tunnel *controlPlaneLifecycleTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	_ = ctx
	return pb.StreamPayload{}, errors.New("test tunnel has no payload")
}

func (tunnel *controlPlaneLifecycleTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	_ = ctx
	_ = payload
	return nil
}

func (tunnel *controlPlaneLifecycleTestTunnel) Close() error {
	tunnel.closed = true
	return nil
}

// TestControlMessageDispatcherSessionTakeoverLifecycle 验证同 connector 新 epoch 会收敛旧会话资源。
func TestControlMessageDispatcherSessionTakeoverLifecycle(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-old",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-old",
		ServiceKey:   "dev/demo/order-service",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	oldTunnel := &controlPlaneLifecycleTestTunnel{tunnelID: "tunnel-old"}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-old", oldTunnel); err != nil {
		testingObject.Fatalf("upsert old session tunnel failed: %v", err)
	}

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	// 同 connector 建立更高 epoch 会话，触发旧会话 DRAINING + tunnel/service 收敛。
	dispatcher.upsertSessionFromEnvelope(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageTunnelPoolReport,
		SessionID:       "session-new",
		SessionEpoch:    2,
		ConnectorID:     "connector-1",
		EventID:         "evt-new",
		ResourceVersion: 9,
	})

	oldSession, exists := sessionRegistry.GetBySession("session-old")
	if !exists {
		testingObject.Fatalf("expected old session exists")
	}
	if oldSession.State != registry.SessionDraining {
		testingObject.Fatalf("unexpected old session state: got=%s want=%s", oldSession.State, registry.SessionDraining)
	}
	newSession, exists := sessionRegistry.GetBySession("session-new")
	if !exists {
		testingObject.Fatalf("expected new session exists")
	}
	if newSession.State != registry.SessionActive {
		testingObject.Fatalf("unexpected new session state: got=%s want=%s", newSession.State, registry.SessionActive)
	}
	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-old")
	if !exists {
		testingObject.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusInactive {
		testingObject.Fatalf("unexpected service status after takeover: got=%s want=%s", serviceSnapshot.Status, pb.ServiceStatusInactive)
	}
	if _, exists := tunnelRegistry.Get("tunnel-old"); exists {
		testingObject.Fatalf("expected old session tunnel purged")
	}
	if !oldTunnel.closed {
		testingObject.Fatalf("expected old session tunnel closed")
	}
}

// TestControlMessageDispatcherSweepSessionLifecycle 验证 heartbeat 超时会触发 STALE/CLOSED 收敛。
func TestControlMessageDispatcherSweepSessionLifecycle(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-4001",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-2 * time.Minute),
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-4001",
		ServiceKey:   "dev/demo/order-service",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	staleTunnel := &controlPlaneLifecycleTestTunnel{tunnelID: "tunnel-4001"}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-4001", staleTunnel); err != nil {
		testingObject.Fatalf("upsert stale tunnel failed: %v", err)
	}

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	dispatcher.sweepSessionLifecycle(now, 30*time.Second, 30*time.Second)

	staleSession, exists := sessionRegistry.GetBySession("session-4001")
	if !exists {
		testingObject.Fatalf("expected stale session exists")
	}
	if staleSession.State != registry.SessionStale {
		testingObject.Fatalf("unexpected session state after first sweep: got=%s want=%s", staleSession.State, registry.SessionStale)
	}
	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-4001")
	if !exists {
		testingObject.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusStale {
		testingObject.Fatalf("unexpected service status after stale: got=%s want=%s", serviceSnapshot.Status, pb.ServiceStatusStale)
	}
	if _, exists := tunnelRegistry.Get("tunnel-4001"); exists {
		testingObject.Fatalf("expected stale session tunnel purged")
	}
	if !staleTunnel.closed {
		testingObject.Fatalf("expected stale session tunnel closed")
	}

	dispatcher.sweepSessionLifecycle(now.Add(time.Minute), 30*time.Second, 30*time.Second)
	closedSession, exists := sessionRegistry.GetBySession("session-4001")
	if !exists {
		testingObject.Fatalf("expected closed session exists")
	}
	if closedSession.State != registry.SessionClosed {
		testingObject.Fatalf("unexpected session state after second sweep: got=%s want=%s", closedSession.State, registry.SessionClosed)
	}
}

// TestControlMessageDispatcherStaleOldSessionDoesNotDowngradeCurrentServices
// 验证旧会话进入 STALE 时不会把新会话已接管的服务降级。
func TestControlMessageDispatcherStaleOldSessionDoesNotDowngradeCurrentServices(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	// 同 connector 下保留旧会话（epoch=1）和当前会话（epoch=2）。
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-old",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         registry.SessionDraining,
		LastHeartbeat: now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-2 * time.Minute),
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-new",
		ConnectorID:   "connector-1",
		Epoch:         2,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-new",
		ServiceKey:   "dev/demo/order-service",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
	})
	dispatcher.transitionSessionState(
		now.Add(time.Second),
		"session-old",
		1,
		registry.SessionStale,
		"heartbeat_timeout",
	)

	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-new")
	if !exists {
		testingObject.Fatalf("expected current service exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusActive {
		testingObject.Fatalf(
			"old session stale should not downgrade current service: got=%s want=%s",
			serviceSnapshot.Status,
			pb.ServiceStatusActive,
		)
	}
}

// TestControlMessageDispatcherResourceEventDoesNotReactivateDrainingSession
// 验证同 epoch 非心跳资源事件不会把 DRAINING 会话重新提升为 ACTIVE。
func TestControlMessageDispatcherResourceEventDoesNotReactivateDrainingSession(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	oldHeartbeat := now.Add(-time.Minute)
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-draining",
		ConnectorID:   "connector-1",
		Epoch:         9,
		State:         registry.SessionDraining,
		LastHeartbeat: oldHeartbeat,
		UpdatedAt:     now,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:       "svc-1",
		ServiceKey:      "dev/demo/order-service",
		ConnectorID:     "connector-1",
		Status:          pb.ServiceStatusInactive,
		HealthStatus:    pb.HealthStatusUnknown,
		ResourceVersion: 1,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
	})

	encodedPayload, err := json.Marshal(pb.ServiceHealthReport{
		ServiceID:           "svc-1",
		ServiceKey:          "dev/demo/order-service",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       now.Unix(),
	})
	if err != nil {
		testingObject.Fatalf("marshal health report failed: %v", err)
	}
	replyEnvelope, err := dispatcher.dispatchEnvelope(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-draining",
		SessionEpoch: 9,
		ConnectorID:  "connector-1",
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("dispatch health report failed: %v", err)
	}
	if replyEnvelope != nil {
		testingObject.Fatalf("service health report should not generate ack envelope")
	}

	sessionSnapshot, exists := sessionRegistry.GetBySession("session-draining")
	if !exists {
		testingObject.Fatalf("expected draining session exists")
	}
	if sessionSnapshot.State != registry.SessionDraining {
		testingObject.Fatalf(
			"resource event should not reactivate draining session: got=%s want=%s",
			sessionSnapshot.State,
			registry.SessionDraining,
		)
	}
	if !sessionSnapshot.LastHeartbeat.Equal(oldHeartbeat) {
		testingObject.Fatalf(
			"resource event should not refresh heartbeat: got=%v want=%v",
			sessionSnapshot.LastHeartbeat,
			oldHeartbeat,
		)
	}
}

// TestControlMessageDispatcherHandleFrameRefreshesHeartbeat
// 验证 transport ping/pong 可刷新连接所属会话心跳时间。
func TestControlMessageDispatcherHandleFrameRefreshesHeartbeat(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	oldHeartbeat := now.Add(-2 * time.Minute)
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-5001",
		ConnectorID:   "connector-1",
		Epoch:         3,
		State:         registry.SessionActive,
		LastHeartbeat: oldHeartbeat,
		UpdatedAt:     oldHeartbeat,
	})

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
	})
	sessionState := &controlChannelSessionState{
		sessionID:    "session-5001",
		sessionEpoch: 3,
	}
	replyFrame, _, err := dispatcher.handleFrame(
		transport.ControlFrame{Type: transport.ControlFrameTypeHeartbeatPing},
		sessionState,
	)
	if err != nil {
		testingObject.Fatalf("handle transport heartbeat ping failed: %v", err)
	}
	if replyFrame == nil || replyFrame.Type != transport.ControlFrameTypeHeartbeatPong {
		testingObject.Fatalf("expected heartbeat pong reply")
	}

	sessionSnapshot, exists := sessionRegistry.GetBySession("session-5001")
	if !exists {
		testingObject.Fatalf("expected session exists")
	}
	if !sessionSnapshot.LastHeartbeat.After(oldHeartbeat) {
		testingObject.Fatalf(
			"expected heartbeat refreshed by transport ping: old=%v new=%v",
			oldHeartbeat,
			sessionSnapshot.LastHeartbeat,
		)
	}
}
