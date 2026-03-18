package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	apptls "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/tls"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// performConnectorHandshakeForTest 发送 Hello/Auth 并返回握手响应载荷。
func performConnectorHandshakeForTest(
	testingObject *testing.T,
	ctx context.Context,
	controlChannel transport.ControlChannel,
	connectorID string,
	token string,
) (pb.ConnectorWelcome, pb.ConnectorAuthAck) {
	testingObject.Helper()

	helloPayload := pb.ConnectorHello{
		ConnectorID:       connectorID,
		NodeName:          "node-test",
		Version:           "agent-core",
		SupportedBindings: []string{"tcp_framed"},
	}
	encodedHelloPayload, err := json.Marshal(helloPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector hello payload failed: %v", err)
	}
	helloFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		ConnectorID:  connectorID,
		Payload:      encodedHelloPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector hello frame failed: %v", err)
	}
	if err := controlChannel.WriteControlFrame(ctx, helloFrame); err != nil {
		testingObject.Fatalf("write connector hello frame failed: %v", err)
	}

	welcomeFrame, err := controlChannel.ReadControlFrame(ctx)
	if err != nil {
		testingObject.Fatalf("read connector welcome frame failed: %v", err)
	}
	if welcomeFrame.Type != transport.ControlFrameTypeConnectorWelcome {
		testingObject.Fatalf(
			"unexpected connector welcome frame type: got=%d want=%d",
			welcomeFrame.Type,
			transport.ControlFrameTypeConnectorWelcome,
		)
	}
	welcomeEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(welcomeFrame)
	if err != nil {
		testingObject.Fatalf("decode connector welcome envelope failed: %v", err)
	}
	var welcomePayload pb.ConnectorWelcome
	if err := json.Unmarshal(welcomeEnvelope.Payload, &welcomePayload); err != nil {
		testingObject.Fatalf("unmarshal connector welcome payload failed: %v", err)
	}

	authPayload := pb.ConnectorAuth{
		AuthMethod:       "token",
		Token:            token,
		ClientCapVersion: "agent-core/v1",
	}
	encodedAuthPayload, err := json.Marshal(authPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector auth payload failed: %v", err)
	}
	authFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  connectorID,
		Payload:      encodedAuthPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector auth frame failed: %v", err)
	}
	if err := controlChannel.WriteControlFrame(ctx, authFrame); err != nil {
		testingObject.Fatalf("write connector auth frame failed: %v", err)
	}

	authAckFrame, err := controlChannel.ReadControlFrame(ctx)
	if err != nil {
		testingObject.Fatalf("read connector auth ack frame failed: %v", err)
	}
	if authAckFrame.Type != transport.ControlFrameTypeConnectorAuthAck {
		testingObject.Fatalf(
			"unexpected connector auth ack frame type: got=%d want=%d",
			authAckFrame.Type,
			transport.ControlFrameTypeConnectorAuthAck,
		)
	}
	authAckEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(authAckFrame)
	if err != nil {
		testingObject.Fatalf("decode connector auth ack envelope failed: %v", err)
	}
	var authAckPayload pb.ConnectorAuthAck
	if err := json.Unmarshal(authAckEnvelope.Payload, &authAckPayload); err != nil {
		testingObject.Fatalf("unmarshal connector auth ack payload failed: %v", err)
	}
	return welcomePayload, authAckPayload
}

// buildHelloEnvelopeForTest 构造用于握手测试的 ConnectorHello 信封。
func buildHelloEnvelopeForTest(testingObject *testing.T, connectorID string) pb.ControlEnvelope {
	testingObject.Helper()
	helloPayload := pb.ConnectorHello{
		ConnectorID:       connectorID,
		NodeName:          "node-test",
		Version:           "agent-core",
		SupportedBindings: []string{"tcp_framed"},
	}
	encodedHelloPayload, err := json.Marshal(helloPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector hello payload failed: %v", err)
	}
	return pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		ConnectorID:  connectorID,
		Payload:      encodedHelloPayload,
	}
}

// buildAuthEnvelopeForTest 构造用于握手测试的 ConnectorAuth 信封。
func buildAuthEnvelopeForTest(testingObject *testing.T, connectorID string, token string) pb.ControlEnvelope {
	testingObject.Helper()
	authPayload := pb.ConnectorAuth{
		AuthMethod:       "token",
		Token:            token,
		ClientCapVersion: "agent-core/v1",
	}
	encodedAuthPayload, err := json.Marshal(authPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector auth payload failed: %v", err)
	}
	return pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  connectorID,
		Payload:      encodedAuthPayload,
	}
}

// decodeConnectorAuthAckFromEnvelope 从响应信封中解码 ConnectorAuthAck 载荷。
func decodeConnectorAuthAckFromEnvelope(testingObject *testing.T, envelope *pb.ControlEnvelope) pb.ConnectorAuthAck {
	testingObject.Helper()
	if envelope == nil {
		testingObject.Fatalf("expected non-nil control envelope")
	}
	var authAckPayload pb.ConnectorAuthAck
	if err := json.Unmarshal(envelope.Payload, &authAckPayload); err != nil {
		testingObject.Fatalf("unmarshal connector auth ack payload failed: %v", err)
	}
	return authAckPayload
}

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
	_, authAckPayload := performConnectorHandshakeForTest(
		testingObject,
		ctx,
		clientControl,
		"agent-local",
		"dbt_agent-local.agent-dev-secret",
	)
	if !authAckPayload.Success {
		testingObject.Fatalf("expected auth success before publish, got=%s", authAckPayload.ErrorCode)
	}

	publishPayload := pb.PublishService{
		ServiceID:   "svc-001",
		ServiceKey:  "order-service/http",
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
		SessionID:       authAckPayload.SessionID,
		SessionEpoch:    authAckPayload.SessionEpoch,
		ConnectorID:     "agent-local",
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

// TestServeControlChannelRejectPublishServiceBeforeAuth 验证未认证业务消息被丢弃且不触发断链。
func TestServeControlChannelRejectPublishServiceBeforeAuth(testingObject *testing.T) {
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
		ServiceID:   "svc-unauth",
		ServiceKey:  "unauth-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "unauth-service",
		ServiceType: "http",
	}
	encodedPublishPayload, err := json.Marshal(publishPayload)
	if err != nil {
		testingObject.Fatalf("marshal publish payload failed: %v", err)
	}
	publishFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-unauth",
		SessionEpoch:    1,
		RequestID:       "req-unauth",
		EventID:         "evt-unauth",
		ResourceType:    "service",
		ResourceID:      "svc-unauth",
		ResourceVersion: 1,
		Payload:         encodedPublishPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode publish frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, publishFrame); err != nil {
		testingObject.Fatalf("write unauth publish frame failed: %v", err)
	}

	// 发送未认证业务消息后，连接应保持可用，且后续握手仍可成功。
	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	welcomePayload, authAckPayload := performConnectorHandshakeForTest(
		testingObject,
		readContext,
		clientControl,
		"agent-local",
		"dbt_agent-local.agent-dev-secret",
	)
	if welcomePayload.AssignedSessionEpoch == 0 {
		testingObject.Fatalf("expected assigned session epoch after unauth publish")
	}
	if !authAckPayload.Success {
		testingObject.Fatalf("expected auth success after unauth publish, got=%s", authAckPayload.ErrorCode)
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

// TestServeControlChannelHandleConnectorHandshake 验证 Bridge 可完成 Hello/Auth 握手闭环。
func TestServeControlChannelHandleConnectorHandshake(testingObject *testing.T) {
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

	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	welcomePayload, authAckPayload := performConnectorHandshakeForTest(
		testingObject,
		readContext,
		clientControl,
		"agent-local",
		"dbt_agent-local.agent-dev-secret",
	)
	if welcomePayload.AssignedSessionEpoch == 0 {
		testingObject.Fatalf("expected assigned session epoch from welcome payload")
	}
	if welcomePayload.TLSMode != string(apptls.ModePlaintext) {
		testingObject.Fatalf("unexpected welcome tls_mode: got=%s want=%s", welcomePayload.TLSMode, apptls.ModePlaintext)
	}
	if !authAckPayload.Success {
		testingObject.Fatalf("expected auth success, got error=%s", authAckPayload.ErrorCode)
	}
	if authAckPayload.SessionEpoch != welcomePayload.AssignedSessionEpoch {
		testingObject.Fatalf(
			"unexpected auth ack epoch: got=%d want=%d",
			authAckPayload.SessionEpoch,
			welcomePayload.AssignedSessionEpoch,
		)
	}
	if strings.TrimSpace(authAckPayload.SessionID) == "" {
		testingObject.Fatalf("expected non-empty auth ack session id")
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

// TestServeControlChannelMarksSessionClosedOnPeerClose 验证 TCP 控制流正常断开不会被标记为 FAILED。
func TestServeControlChannelMarksSessionClosedOnPeerClose(testingObject *testing.T) {
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

	sessionRegistry := registry.NewSessionRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveControlChannelWithDispatcherAndPeerAddr(
			ctx,
			serverControl,
			dispatcher,
			"10.20.30.40:39080",
		)
	}()

	readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	_, authAckPayload := performConnectorHandshakeForTest(
		testingObject,
		readContext,
		clientControl,
		"agent-local",
		"dbt_agent-local.agent-dev-secret",
	)
	if !authAckPayload.Success {
		testingObject.Fatalf("expected auth success, got error=%s", authAckPayload.ErrorCode)
	}
	if strings.TrimSpace(authAckPayload.SessionID) == "" {
		testingObject.Fatalf("expected non-empty auth ack session id")
	}

	_ = clientControl.Close(context.Background())
	_ = clientConn.Close()

	select {
	case doneErr := <-serverDone:
		if doneErr != nil {
			testingObject.Fatalf("serve control channel stopped with error: %v", doneErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("serve control channel did not stop in time")
	}

	sessionSnapshot, exists := sessionRegistry.GetBySession(authAckPayload.SessionID)
	if !exists {
		testingObject.Fatalf("expected session snapshot exists")
	}
	if sessionSnapshot.State != registry.SessionClosed {
		testingObject.Fatalf("unexpected session state after peer close: got=%s want=%s", sessionSnapshot.State, registry.SessionClosed)
	}
}

// TestServeControlChannelRejectInvalidConnectorAuthMethod 验证非法 auth_method 会被拒绝。
func TestServeControlChannelRejectInvalidConnectorAuthMethod(testingObject *testing.T) {
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
	go func() {
		_ = serveControlChannelWithDispatcher(
			ctx,
			serverControl,
			newControlMessageDispatcher(controlMessageDispatcherOptions{}),
		)
	}()

	helloPayload := pb.ConnectorHello{
		ConnectorID: "connector-auth-invalid-method",
		NodeName:    "node-a",
	}
	encodedHelloPayload, err := json.Marshal(helloPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector hello payload failed: %v", err)
	}
	helloFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		ConnectorID:  helloPayload.ConnectorID,
		Payload:      encodedHelloPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector hello frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, helloFrame); err != nil {
		testingObject.Fatalf("write connector hello frame failed: %v", err)
	}
	if _, err := clientControl.ReadControlFrame(context.Background()); err != nil {
		testingObject.Fatalf("read connector welcome frame failed: %v", err)
	}

	authPayload := pb.ConnectorAuth{
		AuthMethod: "hmac",
		Token:      "dbt_connector-auth-invalid-method.secret-a",
	}
	encodedAuthPayload, err := json.Marshal(authPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector auth payload failed: %v", err)
	}
	authFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  helloPayload.ConnectorID,
		Payload:      encodedAuthPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector auth frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, authFrame); err != nil {
		testingObject.Fatalf("write connector auth frame failed: %v", err)
	}

	authAckFrame, err := clientControl.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read connector auth ack frame failed: %v", err)
	}
	authAckEnvelope, err := transport.DecodeBusinessControlEnvelopeFrame(authAckFrame)
	if err != nil {
		testingObject.Fatalf("decode connector auth ack envelope failed: %v", err)
	}
	var authAckPayload pb.ConnectorAuthAck
	if err := json.Unmarshal(authAckEnvelope.Payload, &authAckPayload); err != nil {
		testingObject.Fatalf("unmarshal connector auth ack payload failed: %v", err)
	}
	if authAckPayload.Success {
		testingObject.Fatalf("expected auth failure for invalid method")
	}
	if authAckPayload.ErrorCode != ltfperrors.CodeAuthInvalidMethod {
		testingObject.Fatalf("unexpected auth error code: got=%s want=%s", authAckPayload.ErrorCode, ltfperrors.CodeAuthInvalidMethod)
	}
}

// TestControlMessageDispatcherHelloRateLimitBySourceIP 验证 Hello 在 source_ip 维度可命中限流。
func TestControlMessageDispatcherHelloRateLimitBySourceIP(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		metrics: metrics,
		handshakeGuard: appauth.NewHandshakeGuard(appauth.HandshakeGuardOptions{
			HelloRateLimitBySource:    1,
			HelloRateLimitByConnector: 100,
		}),
	})
	firstState := newControlChannelSessionState("10.0.0.1:39080")
	firstReply, err := dispatcher.handleConnectorHelloEnvelope(buildHelloEnvelopeForTest(testingObject, "connector-a"), firstState)
	if err != nil {
		testingObject.Fatalf("first hello should pass, got err=%v", err)
	}
	if firstReply == nil || firstReply.MessageType != pb.ControlMessageConnectorWelcome {
		testingObject.Fatalf("expected first hello reply is ConnectorWelcome")
	}

	secondState := newControlChannelSessionState("10.0.0.1:39081")
	secondReply, err := dispatcher.handleConnectorHelloEnvelope(buildHelloEnvelopeForTest(testingObject, "connector-b"), secondState)
	if err != nil {
		testingObject.Fatalf("second hello should return auth ack payload, got err=%v", err)
	}
	authAckPayload := decodeConnectorAuthAckFromEnvelope(testingObject, secondReply)
	if authAckPayload.Success {
		testingObject.Fatalf("expected second hello rejected by source_ip rate limit")
	}
	if authAckPayload.ErrorCode != appauth.AuthErrorRateLimited {
		testingObject.Fatalf("unexpected hello rate-limit error code: got=%s", authAckPayload.ErrorCode)
	}
	if metrics.BridgeAuthRateLimitTotal() != 1 {
		testingObject.Fatalf("unexpected hello rate-limit metric: got=%d want=1", metrics.BridgeAuthRateLimitTotal())
	}
}

// TestControlMessageDispatcherHelloRateLimitByConnectorID 验证 Hello 在 connector_id 维度可命中限流。
func TestControlMessageDispatcherHelloRateLimitByConnectorID(testingObject *testing.T) {
	testingObject.Parallel()

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		handshakeGuard: appauth.NewHandshakeGuard(appauth.HandshakeGuardOptions{
			HelloRateLimitBySource:    100,
			HelloRateLimitByConnector: 1,
		}),
	})
	firstState := newControlChannelSessionState("10.0.0.2:39080")
	firstReply, err := dispatcher.handleConnectorHelloEnvelope(buildHelloEnvelopeForTest(testingObject, "connector-c"), firstState)
	if err != nil {
		testingObject.Fatalf("first hello should pass, got err=%v", err)
	}
	if firstReply == nil || firstReply.MessageType != pb.ControlMessageConnectorWelcome {
		testingObject.Fatalf("expected first hello reply is ConnectorWelcome")
	}

	secondState := newControlChannelSessionState("10.0.0.3:39080")
	secondReply, err := dispatcher.handleConnectorHelloEnvelope(buildHelloEnvelopeForTest(testingObject, "connector-c"), secondState)
	if err != nil {
		testingObject.Fatalf("second hello should return auth ack payload, got err=%v", err)
	}
	authAckPayload := decodeConnectorAuthAckFromEnvelope(testingObject, secondReply)
	if authAckPayload.Success {
		testingObject.Fatalf("expected second hello rejected by connector_id rate limit")
	}
	if authAckPayload.ErrorCode != appauth.AuthErrorRateLimited {
		testingObject.Fatalf("unexpected hello rate-limit error code: got=%s", authAckPayload.ErrorCode)
	}
}

// TestControlMessageDispatcherAuthFailureBanBySourceIP 验证认证失败会在 source_ip 维度触发短时封禁。
func TestControlMessageDispatcherAuthFailureBanBySourceIP(testingObject *testing.T) {
	testingObject.Parallel()

	guard := appauth.NewHandshakeGuard(appauth.HandshakeGuardOptions{
		AuthFailureLimitBySource:    1,
		AuthFailureLimitByConnector: 100,
		AuthFailureBanDuration:      time.Hour,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		handshakeGuard: guard,
	})

	firstState := newControlChannelSessionState("10.0.1.1:39080")
	firstState.setHelloContext("connector-ban-a", 1)
	firstAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-a", "dbt_missing.secret-a"),
		firstState,
	)
	if err != nil {
		testingObject.Fatalf("first auth should return auth ack payload, got err=%v", err)
	}
	firstAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, firstAuthReply)
	if firstAuthAck.ErrorCode != appauth.AuthErrorInvalidToken {
		testingObject.Fatalf("unexpected first auth error code: got=%s", firstAuthAck.ErrorCode)
	}

	secondState := newControlChannelSessionState("10.0.1.1:39081")
	secondState.setHelloContext("connector-ban-b", 1)
	secondAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-b", "dbt_missing.secret-b"),
		secondState,
	)
	if err != nil {
		testingObject.Fatalf("second auth should return auth ack payload, got err=%v", err)
	}
	secondAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, secondAuthReply)
	if secondAuthAck.ErrorCode != appauth.AuthErrorInvalidToken {
		testingObject.Fatalf("unexpected second auth error code: got=%s", secondAuthAck.ErrorCode)
	}

	thirdState := newControlChannelSessionState("10.0.1.1:39082")
	thirdState.setHelloContext("connector-ban-c", 1)
	thirdAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-c", "dbt_missing.secret-c"),
		thirdState,
	)
	if err != nil {
		testingObject.Fatalf("third auth should return auth ack payload, got err=%v", err)
	}
	thirdAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, thirdAuthReply)
	if thirdAuthAck.ErrorCode != appauth.AuthErrorRateLimited {
		testingObject.Fatalf("expected source_ip ban rate limit, got=%s", thirdAuthAck.ErrorCode)
	}
}

// TestControlMessageDispatcherAuthFailureBanByConnectorID 验证认证失败会在 connector_id 维度触发短时封禁。
func TestControlMessageDispatcherAuthFailureBanByConnectorID(testingObject *testing.T) {
	testingObject.Parallel()

	guard := appauth.NewHandshakeGuard(appauth.HandshakeGuardOptions{
		AuthFailureLimitBySource:    100,
		AuthFailureLimitByConnector: 1,
		AuthFailureBanDuration:      time.Hour,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		handshakeGuard: guard,
	})

	firstState := newControlChannelSessionState("10.0.2.1:39080")
	firstState.setHelloContext("connector-ban-same", 1)
	firstAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-same", "dbt_missing.secret-a"),
		firstState,
	)
	if err != nil {
		testingObject.Fatalf("first auth should return auth ack payload, got err=%v", err)
	}
	firstAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, firstAuthReply)
	if firstAuthAck.ErrorCode != appauth.AuthErrorInvalidToken {
		testingObject.Fatalf("unexpected first auth error code: got=%s", firstAuthAck.ErrorCode)
	}

	secondState := newControlChannelSessionState("10.0.2.2:39080")
	secondState.setHelloContext("connector-ban-same", 2)
	secondAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-same", "dbt_missing.secret-b"),
		secondState,
	)
	if err != nil {
		testingObject.Fatalf("second auth should return auth ack payload, got err=%v", err)
	}
	secondAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, secondAuthReply)
	if secondAuthAck.ErrorCode != appauth.AuthErrorInvalidToken {
		testingObject.Fatalf("unexpected second auth error code: got=%s", secondAuthAck.ErrorCode)
	}

	thirdState := newControlChannelSessionState("10.0.2.3:39080")
	thirdState.setHelloContext("connector-ban-same", 3)
	thirdAuthReply, err := dispatcher.handleConnectorAuthEnvelope(
		buildAuthEnvelopeForTest(testingObject, "connector-ban-same", "dbt_missing.secret-c"),
		thirdState,
	)
	if err != nil {
		testingObject.Fatalf("third auth should return auth ack payload, got err=%v", err)
	}
	thirdAuthAck := decodeConnectorAuthAckFromEnvelope(testingObject, thirdAuthReply)
	if thirdAuthAck.ErrorCode != appauth.AuthErrorRateLimited {
		testingObject.Fatalf("expected connector_id ban rate limit, got=%s", thirdAuthAck.ErrorCode)
	}
}

// TestControlMessageDispatcherAuthRejectNormalization 验证未知 connector/无效 token/吊销 token 对外口径统一。
func TestControlMessageDispatcherAuthRejectNormalization(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		authCoordinator: appauth.NewCoordinator(appauth.CoordinatorOptions{
			SessionRegistry: sessionRegistry,
			TokenStore: appauth.NewInMemoryTokenStore([]appauth.TokenRecord{
				{
					TokenID:         "valid-token",
					ConnectorID:     "connector-valid",
					TokenSecretHash: appauth.MustHashTokenSecretArgon2ID("valid-secret"),
					HashAlgorithm:   appauth.TokenHashAlgorithmArgon2ID,
					HashVersion:     appauth.TokenHashVersionV1,
					Status:          appauth.TokenStatusActive,
				},
				{
					TokenID:         "revoked-token",
					ConnectorID:     "connector-revoked",
					TokenSecretHash: appauth.MustHashTokenSecretArgon2ID("revoked-secret"),
					HashAlgorithm:   appauth.TokenHashAlgorithmArgon2ID,
					HashVersion:     appauth.TokenHashVersionV1,
					Status:          appauth.TokenStatusRevoked,
				},
			}),
		}),
	})
	testCases := []struct {
		name        string
		connectorID string
		token       string
	}{
		{
			name:        "unknown connector",
			connectorID: "connector-unknown",
			token:       "dbt_missing-token.secret",
		},
		{
			name:        "invalid token secret",
			connectorID: "connector-valid",
			token:       "dbt_valid-token.invalid-secret",
		},
		{
			name:        "revoked token",
			connectorID: "connector-revoked",
			token:       "dbt_revoked-token.revoked-secret",
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			sessionState := newControlChannelSessionState("10.0.3.1:39080")
			sessionState.setHelloContext(testCase.connectorID, 1)
			authReply, err := dispatcher.handleConnectorAuthEnvelope(
				buildAuthEnvelopeForTest(testingObject, testCase.connectorID, testCase.token),
				sessionState,
			)
			if err != nil {
				testingObject.Fatalf("auth should return auth ack payload, got err=%v", err)
			}
			authAck := decodeConnectorAuthAckFromEnvelope(testingObject, authReply)
			if authAck.Success {
				testingObject.Fatalf("expected auth reject")
			}
			if authAck.ErrorCode != appauth.AuthErrorInvalidToken {
				testingObject.Fatalf("expected normalized auth error code, got=%s", authAck.ErrorCode)
			}
			if authAck.ErrorMessage != "authentication rejected" {
				testingObject.Fatalf("expected normalized auth error message, got=%s", authAck.ErrorMessage)
			}
		})
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
	_, authAckPayload := performConnectorHandshakeForTest(
		testingObject,
		ctx,
		controlChannel,
		"agent-local",
		"dbt_agent-local.agent-dev-secret",
	)
	if !authAckPayload.Success {
		testingObject.Fatalf("expected auth success before grpc publish, got=%s", authAckPayload.ErrorCode)
	}

	publishPayload := pb.PublishService{
		ServiceID:   "svc-002",
		ServiceKey:  "pay-service/http",
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
		SessionID:       authAckPayload.SessionID,
		SessionEpoch:    authAckPayload.SessionEpoch,
		ConnectorID:     "agent-local",
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
		ServiceKey:      "order-service/http",
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
		ServiceKey:          "order-service/http",
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
	now := time.Now().UTC()
	tunnelRegistry := registry.NewTunnelRegistry()
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-3001", &controlPlaneLifecycleTestTunnel{tunnelID: "tunnel-3001-idle"}); err != nil {
		testingObject.Fatalf("upsert idle tunnel failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-3001", &controlPlaneLifecycleTestTunnel{tunnelID: "tunnel-3001-active"}); err != nil {
		testingObject.Fatalf("upsert active tunnel failed: %v", err)
	}
	acquiredTunnel, ok := tunnelRegistry.AcquireIdle(now, "connector-1")
	if !ok {
		testingObject.Fatalf("expected acquire idle tunnel success")
	}
	if err := tunnelRegistry.MarkActive(now, acquiredTunnel.TunnelID, "traffic-3001"); err != nil {
		testingObject.Fatalf("mark active tunnel failed: %v", err)
	}
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		tunnelRegistry:  tunnelRegistry,
	})

	reportPayload := pb.TunnelPoolReport{
		SessionID:       "session-3001",
		SessionEpoch:    7,
		IdleCount:       1,
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
	if refillRequest.Metadata["bridge_idle_count"] != "1" || refillRequest.Metadata["bridge_in_use_count"] != "1" {
		testingObject.Fatalf("unexpected bridge pool metadata: %+v", refillRequest.Metadata)
	}
	if refillRequest.Metadata["bridge_idle_recycled_count"] != "0" {
		testingObject.Fatalf("unexpected bridge recycled metadata: %+v", refillRequest.Metadata)
	}
}

// TestControlMessageDispatcherIgnoresTunnelDialAnnounce 验证硬切换后控制面 TunnelDialAnnounce 会被忽略。
func TestControlMessageDispatcherIgnoresTunnelDialAnnounce(testingObject *testing.T) {
	testingObject.Parallel()

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{})
	announcePayload := pb.TunnelDialAnnounce{
		SessionID:     "session-a",
		SessionEpoch:  9,
		TunnelID:      "tun-77",
		DialLocalAddr: "127.0.0.1:54321",
		TimestampUnix: time.Now().UTC().Unix(),
	}
	encodedPayload, err := json.Marshal(announcePayload)
	if err != nil {
		testingObject.Fatalf("marshal announce payload failed: %v", err)
	}
	replyEnvelope, err := dispatcher.dispatchEnvelope(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageTunnelDialAnnounce,
		SessionID:    "session-a",
		SessionEpoch: 9,
		Payload:      encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("dispatch tunnel dial announce failed: %v", err)
	}
	if replyEnvelope != nil {
		testingObject.Fatalf("tunnel dial announce should not produce reply envelope")
	}
}

func writeTCPTunnelHandshakeFrameForTest(connection net.Conn, handshake pb.TunnelDialAnnounce) error {
	if connection == nil {
		return errors.New("write tcp tunnel handshake frame for test: nil conn")
	}
	encodedPayload, err := json.Marshal(handshake)
	if err != nil {
		return err
	}
	frame := make([]byte, 4+len(encodedPayload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(encodedPayload)))
	copy(frame[4:], encodedPayload)
	writtenSize := 0
	for writtenSize < len(frame) {
		nextWrittenSize, writeErr := connection.Write(frame[writtenSize:])
		if writeErr != nil {
			return writeErr
		}
		if nextWrittenSize == 0 {
			return io.ErrUnexpectedEOF
		}
		writtenSize += nextWrittenSize
	}
	return nil
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
		ServiceKey:   "order-service/http",
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

// TestControlMessageDispatcherSessionTakeoverDoesNotDowngradeNewSessionInstance
// 验证 takeover 只收敛旧会话实例，不会把同 connector 下新会话实例一起摘流。
func TestControlMessageDispatcherSessionTakeoverDoesNotDowngradeNewSessionInstance(testingObject *testing.T) {
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
	serviceRegistry.UpsertWithRuntime(now, pb.Service{
		ServiceID:    "svc-takeover",
		ServiceKey:   "order-service/http",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-old")
	serviceRegistry.UpsertWithRuntime(now.Add(time.Second), pb.Service{
		ServiceID:    "svc-takeover",
		ServiceKey:   "order-service/http",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	}, "session-new")

	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
	})
	dispatcher.upsertSessionFromEnvelope(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageTunnelPoolReport,
		SessionID:       "session-new",
		SessionEpoch:    2,
		ConnectorID:     "connector-1",
		EventID:         "evt-takeover-scope",
		ResourceVersion: 9,
	})

	instances := serviceRegistry.ListInstancesByServiceID("svc-takeover")
	if len(instances) != 2 {
		testingObject.Fatalf("unexpected instance count: got=%d want=2", len(instances))
	}
	instanceStatusBySession := make(map[string]pb.ServiceStatus, len(instances))
	instanceHealthBySession := make(map[string]pb.HealthStatus, len(instances))
	for _, instance := range instances {
		instanceStatusBySession[instance.SessionID] = instance.Service.Status
		instanceHealthBySession[instance.SessionID] = instance.Service.HealthStatus
	}
	if instanceStatusBySession["session-old"] != pb.ServiceStatusInactive ||
		instanceHealthBySession["session-old"] != pb.HealthStatusUnknown {
		testingObject.Fatalf(
			"unexpected old session instance lifecycle after takeover: status=%s health=%s",
			instanceStatusBySession["session-old"],
			instanceHealthBySession["session-old"],
		)
	}
	if instanceStatusBySession["session-new"] != pb.ServiceStatusActive ||
		instanceHealthBySession["session-new"] != pb.HealthStatusHealthy {
		testingObject.Fatalf(
			"unexpected new session instance lifecycle after takeover: status=%s health=%s",
			instanceStatusBySession["session-new"],
			instanceHealthBySession["session-new"],
		)
	}
}

// TestControlMessageDispatcherEpochResetTakeoverFromStaleSession
// 验证旧会话已 STALE 时，低 epoch 新会话可在 Agent 重启后接管 connector。
func TestControlMessageDispatcherEpochResetTakeoverFromStaleSession(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-old",
		ConnectorID:   "connector-1",
		Epoch:         9,
		State:         registry.SessionStale,
		LastHeartbeat: now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-2 * time.Minute),
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:       "svc-epoch-reset",
		ServiceKey:      "order-service/http",
		ConnectorID:     "connector-1",
		Status:          pb.ServiceStatusStale,
		HealthStatus:    pb.HealthStatusUnknown,
		ResourceVersion: 10,
	})
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
	})

	encodedPayload, err := json.Marshal(pb.PublishService{
		ServiceID:   "svc-epoch-reset",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if err != nil {
		testingObject.Fatalf("marshal publish payload failed: %v", err)
	}

	replyEnvelope, err := dispatcher.dispatchEnvelope(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		RequestID:       "req-epoch-reset",
		SessionID:       "session-new",
		SessionEpoch:    1,
		ConnectorID:     "connector-1",
		ResourceType:    "service",
		ResourceID:      "svc-epoch-reset",
		EventID:         "evt-epoch-reset",
		ResourceVersion: 11,
		Payload:         encodedPayload,
	})
	if err != nil {
		testingObject.Fatalf("dispatch publish after epoch reset failed: %v", err)
	}
	if replyEnvelope == nil {
		testingObject.Fatalf("expected publish ack envelope")
	}
	var publishAck pb.PublishServiceAck
	if err := json.Unmarshal(replyEnvelope.Payload, &publishAck); err != nil {
		testingObject.Fatalf("unmarshal publish ack payload failed: %v", err)
	}
	if !publishAck.Accepted {
		testingObject.Fatalf("expected publish ack accepted, got error=%s", publishAck.ErrorCode)
	}

	newSession, exists := sessionRegistry.GetBySession("session-new")
	if !exists {
		testingObject.Fatalf("expected new session exists")
	}
	if newSession.State != registry.SessionActive {
		testingObject.Fatalf("unexpected new session state: got=%s want=%s", newSession.State, registry.SessionActive)
	}
	connectorSession, exists := sessionRegistry.GetByConnector("connector-1")
	if !exists {
		testingObject.Fatalf("expected connector session exists")
	}
	if connectorSession.SessionID != "session-new" {
		testingObject.Fatalf("unexpected connector session owner: got=%s want=%s", connectorSession.SessionID, "session-new")
	}
	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-epoch-reset")
	if !exists {
		testingObject.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusActive {
		testingObject.Fatalf("unexpected service status after reconnect publish: got=%s want=%s", serviceSnapshot.Status, pb.ServiceStatusActive)
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
		ServiceKey:   "order-service/http",
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
		ServiceKey:   "order-service/http",
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
		ServiceKey:      "order-service/http",
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
		ServiceKey:          "order-service/http",
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

// TestControlChannelSessionStateLifecycleTransition
// 验证控制连接生命周期状态机符合 connecting->connected->control_ready->authenticated->draining->closed。
func TestControlChannelSessionStateLifecycleTransition(testingObject *testing.T) {
	testingObject.Parallel()

	sessionState := newControlChannelSessionState("10.0.5.1:39080")
	if sessionState.lifecycle != controlChannelStateConnecting {
		testingObject.Fatalf("unexpected initial lifecycle: got=%s want=%s", sessionState.lifecycle, controlChannelStateConnecting)
	}
	sessionState.markConnected()
	if sessionState.lifecycle != controlChannelStateConnected {
		testingObject.Fatalf("unexpected lifecycle after connected: got=%s want=%s", sessionState.lifecycle, controlChannelStateConnected)
	}
	sessionState.markControlReady()
	if sessionState.lifecycle != controlChannelStateControlReady {
		testingObject.Fatalf("unexpected lifecycle after control_ready: got=%s want=%s", sessionState.lifecycle, controlChannelStateControlReady)
	}
	sessionState.markAuthenticated()
	if sessionState.lifecycle != controlChannelStateAuthenticated {
		testingObject.Fatalf("unexpected lifecycle after authenticated: got=%s want=%s", sessionState.lifecycle, controlChannelStateAuthenticated)
	}
	sessionState.markDraining()
	if sessionState.lifecycle != controlChannelStateDraining {
		testingObject.Fatalf("unexpected lifecycle after draining: got=%s want=%s", sessionState.lifecycle, controlChannelStateDraining)
	}
	sessionState.markClosed()
	if sessionState.lifecycle != controlChannelStateClosed {
		testingObject.Fatalf("unexpected lifecycle after closed: got=%s want=%s", sessionState.lifecycle, controlChannelStateClosed)
	}
}

// TestControlMessageDispatcherMarkSessionFailedPurgesTunnel
// 验证会话进入 FAILED 后会清理该会话下全部 tunnel 并降级服务状态。
func TestControlMessageDispatcherMarkSessionFailedPurgesTunnel(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-failed",
		ConnectorID:   "connector-1",
		Epoch:         7,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	serviceRegistry := registry.NewServiceRegistry()
	serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "svc-failed",
		ServiceKey:   "dev/demo/failover-service",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	failedTunnel := &controlPlaneLifecycleTestTunnel{tunnelID: "tunnel-failed"}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-failed", failedTunnel); err != nil {
		testingObject.Fatalf("upsert failed session tunnel failed: %v", err)
	}
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	sessionState := newControlChannelSessionState("10.0.5.2:39080")
	sessionState.setSession("session-failed", 7)
	sessionState.markAuthenticated()

	dispatcher.markSessionFailedFromState(now.Add(time.Second), sessionState, "unit_test_control_channel_failed")

	sessionSnapshot, exists := sessionRegistry.GetBySession("session-failed")
	if !exists {
		testingObject.Fatalf("expected failed session exists")
	}
	if sessionSnapshot.State != registry.SessionFailed {
		testingObject.Fatalf("unexpected session state after failed transition: got=%s want=%s", sessionSnapshot.State, registry.SessionFailed)
	}
	serviceSnapshot, exists := serviceRegistry.GetByServiceID("svc-failed")
	if !exists {
		testingObject.Fatalf("expected service snapshot exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusStale {
		testingObject.Fatalf("unexpected service status after failed transition: got=%s want=%s", serviceSnapshot.Status, pb.ServiceStatusStale)
	}
	if _, exists := tunnelRegistry.Get("tunnel-failed"); exists {
		testingObject.Fatalf("expected failed session tunnel purged")
	}
	if !failedTunnel.closed {
		testingObject.Fatalf("expected failed session tunnel closed")
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

// TestClassifyTCPInboundConnection 验证 TCP 首包判别可区分 control 与 tunnel 连接。
func TestClassifyTCPInboundConnection(testingObject *testing.T) {
	testingObject.Parallel()

	testingObject.Run("control_frame_prefix", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		go func() {
			header := make([]byte, 6)
			binary.BigEndian.PutUint16(header[0:2], transport.ControlFrameTypeHeartbeatPing)
			binary.BigEndian.PutUint32(header[2:6], 0)
			_, _ = clientConn.Write(header)
		}()

		classifiedConn, isControl, err := classifyTCPInboundConnection(serverConn)
		if err != nil {
			testingObject.Fatalf("classify control connection failed: %v", err)
		}
		if !isControl {
			testingObject.Fatalf("expected control connection")
		}
		readBackHeader := make([]byte, 6)
		if _, err := io.ReadFull(classifiedConn, readBackHeader); err != nil {
			testingObject.Fatalf("read classified control prefix failed: %v", err)
		}
		if binary.BigEndian.Uint16(readBackHeader[0:2]) != transport.ControlFrameTypeHeartbeatPing {
			testingObject.Fatalf("unexpected control frame type after prefix replay")
		}
	})

	testingObject.Run("unknown_prefix_treated_as_tunnel", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		go func() {
			header := make([]byte, 4)
			binary.BigEndian.PutUint32(header[0:4], 16)
			_, _ = clientConn.Write(header)
		}()

		classifiedConn, isControl, err := classifyTCPInboundConnection(serverConn)
		if err != nil {
			testingObject.Fatalf("classify unknown prefix tunnel connection failed: %v", err)
		}
		if isControl {
			testingObject.Fatalf("expected non-control for unknown prefix")
		}
		if classifiedConn == nil {
			testingObject.Fatalf("expected classified tunnel connection on unknown prefix")
		}
		replayedPrefix := make([]byte, 2)
		if _, readErr := io.ReadFull(classifiedConn, replayedPrefix); readErr != nil {
			testingObject.Fatalf("read classified tunnel prefix failed: %v", readErr)
		}
		if replayedPrefix[0] != 0x00 || replayedPrefix[1] != 0x00 {
			testingObject.Fatalf("unexpected replayed tunnel prefix: 0x%02x%02x", replayedPrefix[0], replayedPrefix[1])
		}
	})

	testingObject.Run("http_prefix_rejected", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		go func() {
			_, _ = clientConn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
		}()

		classifiedConn, isControl, err := classifyTCPInboundConnection(serverConn)
		if err == nil {
			testingObject.Fatalf("expected http prefix classify error")
		}
		if !strings.Contains(err.Error(), "non-ltfp protocol") {
			testingObject.Fatalf("unexpected classify error for http prefix: %v", err)
		}
		if isControl {
			testingObject.Fatalf("expected non-control for http prefix")
		}
		if classifiedConn != nil {
			testingObject.Fatalf("expected nil classified connection for http prefix")
		}
	})

	testingObject.Run("no_prefix_timeout_treated_as_tunnel", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		// 不写入任何首包，模拟 Agent 仅建立连接等待 Bridge 下发 traffic_open。
		classifiedConn, isControl, err := classifyTCPInboundConnection(serverConn)
		if err != nil {
			testingObject.Fatalf("classify idle tunnel connection failed: %v", err)
		}
		if isControl {
			testingObject.Fatalf("expected tunnel classification on no-prefix timeout")
		}
		if classifiedConn == nil {
			testingObject.Fatalf("expected passthrough connection for tunnel classification")
		}
	})
}

// TestConnForTransportOpen 验证 prefixed 连接在前缀消费完成后可回退到底层连接。
func TestConnForTransportOpen(testingObject *testing.T) {
	testingObject.Parallel()

	testingObject.Run("unwraps_prefixed_conn_when_prefix_drained", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		prefixedConn := &prefixedNetConn{Conn: serverConn}
		openConn := connForTransportOpen(prefixedConn)
		if openConn != serverConn {
			testingObject.Fatalf("expected underlying conn when prefix drained")
		}
	})

	testingObject.Run("keeps_prefixed_conn_when_prefix_pending", func(testingObject *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer func() {
			_ = serverConn.Close()
			_ = clientConn.Close()
		}()

		prefixedConn := &prefixedNetConn{
			Conn:   serverConn,
			prefix: []byte{0x00},
		}
		openConn := connForTransportOpen(prefixedConn)
		if openConn != prefixedConn {
			testingObject.Fatalf("expected prefixed conn preserved when prefix pending")
		}
	})
}

// TestRegisterAcceptedTunnelSingleActiveSession 验证入站 tunnel 可登记到唯一 ACTIVE session。
func TestRegisterAcceptedTunnelSingleActiveSession(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	now := time.Now().UTC()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-local-1",
		ConnectorID:   "agent-local",
		Epoch:         8,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	server := &controlPlaneServer{
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: sessionRegistry,
			tunnelRegistry:  tunnelRegistry,
		}),
	}

	rawTunnel := newControlPlaneInboundTestTunnel("inbound-tunnel-1")
	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeTCPFramed); err != nil {
		testingObject.Fatalf("register accepted tunnel failed: %v", err)
	}

	runtimeSnapshot, exists := tunnelRegistry.Get("inbound-tunnel-1")
	if !exists {
		testingObject.Fatalf("expected tunnel registered")
	}
	if runtimeSnapshot.State != registry.TunnelStateIdle {
		testingObject.Fatalf("unexpected tunnel state: got=%s want=%s", runtimeSnapshot.State, registry.TunnelStateIdle)
	}
	if runtimeSnapshot.ConnectorID != "agent-local" || runtimeSnapshot.SessionID != "session-local-1" {
		testingObject.Fatalf(
			"unexpected tunnel owner: connector=%s session=%s",
			runtimeSnapshot.ConnectorID,
			runtimeSnapshot.SessionID,
		)
	}

	_ = rawTunnel.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, exists := tunnelRegistry.Get("inbound-tunnel-1"); !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	testingObject.Fatalf("expected idle tunnel removed after close")
}

// TestHandleAcceptedTCPTunnelUsesHandshakeTunnelID 验证 TCP 入站从 tunnel 首帧握手读取 tunnel_id 并登记。
func TestHandleAcceptedTCPTunnelUsesHandshakeTunnelID(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	now := time.Now().UTC()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-tcp-handshake-1",
		ConnectorID:   "agent-local",
		Epoch:         21,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	tcpTransport, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new tcp transport failed: %v", err)
	}
	server := &controlPlaneServer{
		tcpTransport: tcpTransport,
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: sessionRegistry,
			tunnelRegistry:  tunnelRegistry,
		}),
	}

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = clientConn.Close()
	}()
	go func() {
		_ = writeTCPTunnelHandshakeFrameForTest(clientConn, pb.TunnelDialAnnounce{
			SessionID:     "session-tcp-handshake-1",
			SessionEpoch:  21,
			TunnelID:      "tun-agent-21",
			DialLocalAddr: "127.0.0.1:54321",
			TimestampUnix: time.Now().UTC().Unix(),
		})
	}()
	if err := server.handleAcceptedTCPTunnel(serverConn); err != nil {
		testingObject.Fatalf("handle accepted tcp tunnel failed: %v", err)
	}

	runtimeList := tunnelRegistry.List()
	if len(runtimeList) != 1 {
		testingObject.Fatalf("expected one tunnel registered, got=%d", len(runtimeList))
	}
	runtimeSnapshot := runtimeList[0]
	if runtimeSnapshot.ConnectorID != "agent-local" || runtimeSnapshot.SessionID != "session-tcp-handshake-1" {
		testingObject.Fatalf(
			"unexpected handshake tunnel owner: connector=%s session=%s",
			runtimeSnapshot.ConnectorID,
			runtimeSnapshot.SessionID,
		)
	}
	if runtimeSnapshot.TunnelID != "tun-agent-21" {
		testingObject.Fatalf("unexpected registered tunnel id: got=%s want=%s", runtimeSnapshot.TunnelID, "tun-agent-21")
	}
	if runtimeSnapshot.Tunnel == nil {
		testingObject.Fatalf("expected runtime tunnel instance")
	}

	_ = runtimeSnapshot.Tunnel.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if tunnelRegistry.Snapshot().TotalCount == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	testingObject.Fatalf("expected handshake tunnel removed after close")
}

// TestRegisterAcceptedTunnelDuplicateActiveSameConnector
// 验证同 connector 存在重复 ACTIVE session 时，仍可按当前映射会话接收入站 tunnel。
func TestRegisterAcceptedTunnelDuplicateActiveSameConnector(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	now := time.Now().UTC()
	sessionRegistry.Upsert(now.Add(-time.Second), registry.SessionRuntime{
		SessionID:     "session-old",
		ConnectorID:   "agent-local",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now.Add(-time.Second),
		UpdatedAt:     now.Add(-time.Second),
	})
	// 快速重启场景：同 connector、新 session，但 epoch 与旧会话相同。
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-new",
		ConnectorID:   "agent-local",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	server := &controlPlaneServer{
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: sessionRegistry,
			tunnelRegistry:  tunnelRegistry,
		}),
	}

	rawTunnel := newControlPlaneInboundTestTunnel("inbound-tunnel-dup-active")
	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeTCPFramed); err != nil {
		testingObject.Fatalf("register accepted tunnel failed: %v", err)
	}

	runtimeSnapshot, exists := tunnelRegistry.Get("inbound-tunnel-dup-active")
	if !exists {
		testingObject.Fatalf("expected tunnel registered")
	}
	if runtimeSnapshot.State != registry.TunnelStateIdle {
		testingObject.Fatalf("unexpected tunnel state: got=%s want=%s", runtimeSnapshot.State, registry.TunnelStateIdle)
	}
	if runtimeSnapshot.ConnectorID != "agent-local" || runtimeSnapshot.SessionID != "session-new" {
		testingObject.Fatalf(
			"unexpected tunnel owner under duplicate active session: connector=%s session=%s",
			runtimeSnapshot.ConnectorID,
			runtimeSnapshot.SessionID,
		)
	}
}

// TestRegisterAcceptedTunnelLifecycleProbeRemoteClose 验证 idle tunnel 在 Done 未触发时也会被探活回收。
func TestRegisterAcceptedTunnelLifecycleProbeRemoteClose(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	now := time.Now().UTC()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-local-probe",
		ConnectorID:   "agent-local",
		Epoch:         9,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	server := &controlPlaneServer{
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: sessionRegistry,
			tunnelRegistry:  tunnelRegistry,
		}),
	}

	rawTunnel := newControlPlaneInboundTestTunnel("inbound-tunnel-probe")
	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeTCPFramed); err != nil {
		testingObject.Fatalf("register accepted tunnel failed: %v", err)
	}

	rawTunnel.simulateRemoteCloseWithoutDone()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, exists := tunnelRegistry.Get("inbound-tunnel-probe"); !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	testingObject.Fatalf("expected idle tunnel removed after lifecycle probe close")
}

// TestRegisterAcceptedTunnelUsesStreamMetadataTunnelIDForGRPC 验证 gRPC tunnel_id 必须来源于 stream metadata。
func TestRegisterAcceptedTunnelUsesStreamMetadataTunnelIDForGRPC(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-grpc-meta-1",
		ConnectorID:   "agent-grpc",
		Epoch:         12,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	tunnelRegistry := registry.NewTunnelRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	server := &controlPlaneServer{dispatcher: dispatcher}

	rawTunnel := newControlPlaneInboundTestTunnel("tun-64")
	rawTunnel.meta.Labels = map[string]string{
		grpcbinding.TunnelMetaLabelTunnelIDSource: grpcbinding.TunnelIDSourceStreamMetadata,
	}
	defer func() {
		_ = rawTunnel.Close()
	}()

	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeGRPCH2); err != nil {
		testingObject.Fatalf("register accepted grpc tunnel failed: %v", err)
	}

	runtimeSnapshot, exists := tunnelRegistry.Get("tun-64")
	if !exists {
		testingObject.Fatalf("expected grpc tunnel registered with metadata tunnel id")
	}
	if runtimeSnapshot.ConnectorID != "agent-grpc" || runtimeSnapshot.SessionID != "session-grpc-meta-1" {
		testingObject.Fatalf(
			"unexpected grpc tunnel owner: connector=%s session=%s",
			runtimeSnapshot.ConnectorID,
			runtimeSnapshot.SessionID,
		)
	}
	if rawTunnel.closed() {
		testingObject.Fatalf("expected grpc tunnel to remain open after successful registration")
	}
}

// TestRegisterAcceptedTunnelDropsWhenClientTunnelIDMissingForGRPC 验证 gRPC 缺少客户端 tunnel_id 上报时会拒绝登记。
func TestRegisterAcceptedTunnelDropsWhenClientTunnelIDMissingForGRPC(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-grpc-raw-1",
		ConnectorID:   "agent-grpc",
		Epoch:         13,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	tunnelRegistry := registry.NewTunnelRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	server := &controlPlaneServer{dispatcher: dispatcher}

	rawTunnel := newControlPlaneInboundTestTunnel("grpc-raw-88")
	defer func() {
		_ = rawTunnel.Close()
	}()

	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeGRPCH2); err != nil {
		testingObject.Fatalf("register accepted grpc tunnel should not hard fail when tunnel id missing: %v", err)
	}
	if tunnelRegistry.Snapshot().TotalCount != 0 {
		testingObject.Fatalf("expected no grpc tunnel registered when client tunnel id is missing")
	}
	if !rawTunnel.closed() {
		testingObject.Fatalf("expected grpc tunnel closed when client tunnel id is missing")
	}
}

// TestRegisterAcceptedTunnelUsesSessionMetadataOwnerForGRPC
// 验证 gRPC 在 owner 歧义场景下可按 session metadata 精确归属 tunnel。
func TestRegisterAcceptedTunnelUsesSessionMetadataOwnerForGRPC(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-a",
		ConnectorID:   "agent-a",
		Epoch:         3,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-b",
		ConnectorID:   "agent-b",
		Epoch:         6,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	tunnelRegistry := registry.NewTunnelRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	server := &controlPlaneServer{dispatcher: dispatcher}

	rawTunnel := newControlPlaneInboundTestTunnel("tun-grpc-owner-1")
	rawTunnel.meta.SessionID = "session-b"
	rawTunnel.meta.SessionEpoch = 6
	rawTunnel.meta.Labels = map[string]string{
		grpcbinding.TunnelMetaLabelTunnelIDSource: grpcbinding.TunnelIDSourceStreamMetadata,
	}
	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeGRPCH2); err != nil {
		testingObject.Fatalf("register accepted grpc tunnel failed: %v", err)
	}

	runtimeSnapshot, exists := tunnelRegistry.Get("tun-grpc-owner-1")
	if !exists {
		testingObject.Fatalf("expected grpc tunnel registered")
	}
	if runtimeSnapshot.ConnectorID != "agent-b" || runtimeSnapshot.SessionID != "session-b" {
		testingObject.Fatalf(
			"unexpected grpc tunnel owner with session metadata: connector=%s session=%s",
			runtimeSnapshot.ConnectorID,
			runtimeSnapshot.SessionID,
		)
	}
	if rawTunnel.closed() {
		testingObject.Fatalf("expected grpc tunnel open after successful registration")
	}
}

// TestResolveAcceptedTunnelOwnerRejectsFallbackWhenGRPCMetadataUnresolved
// 验证 gRPC 已携带 session metadata 但 owner 无法解析时，不会回退到单活会话归属。
func TestResolveAcceptedTunnelOwnerRejectsFallbackWhenGRPCMetadataUnresolved(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-active",
		ConnectorID:   "agent-active",
		Epoch:         5,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})

	tunnelRegistry := registry.NewTunnelRegistry()
	dispatcher := newControlMessageDispatcher(controlMessageDispatcherOptions{
		sessionRegistry: sessionRegistry,
		tunnelRegistry:  tunnelRegistry,
	})
	server := &controlPlaneServer{dispatcher: dispatcher}

	rawTunnel := newControlPlaneInboundTestTunnel("tun-grpc-owner-unresolved")
	rawTunnel.meta.SessionID = "session-missing"
	rawTunnel.meta.SessionEpoch = 9

	connectorID, sessionID, sessionEpoch, ok := server.resolveAcceptedTunnelOwner(
		rawTunnel,
		transport.BindingTypeGRPCH2,
		20*time.Millisecond,
	)
	if ok {
		testingObject.Fatalf(
			"expected unresolved grpc metadata owner, got connector=%s session=%s epoch=%d",
			connectorID,
			sessionID,
			sessionEpoch,
		)
	}
}

// TestRegisterAcceptedTunnelAmbiguousOwner 验证 owner 不唯一时入站 tunnel 不会被登记。
func TestRegisterAcceptedTunnelAmbiguousOwner(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	tunnelRegistry := registry.NewTunnelRegistry()
	now := time.Now().UTC()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-a",
		ConnectorID:   "agent-a",
		Epoch:         3,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-b",
		ConnectorID:   "agent-b",
		Epoch:         6,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	server := &controlPlaneServer{
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: sessionRegistry,
			tunnelRegistry:  tunnelRegistry,
		}),
	}

	rawTunnel := newControlPlaneInboundTestTunnel("inbound-tunnel-2")
	if err := server.registerAcceptedTunnel(rawTunnel, transport.BindingTypeTCPFramed); err != nil {
		testingObject.Fatalf("register accepted tunnel should not hard fail on ambiguous owner: %v", err)
	}
	if tunnelRegistry.Snapshot().TotalCount != 0 {
		testingObject.Fatalf("expected no tunnel registered when owner is ambiguous")
	}
	if !rawTunnel.closed() {
		testingObject.Fatalf("expected ambiguous tunnel closed immediately")
	}
}

// controlPlaneInboundTestTunnel 是 registerAcceptedTunnel 用的最小 transport.Tunnel 假实现。
type controlPlaneInboundTestTunnel struct {
	meta transport.TunnelMeta

	doneOnce sync.Once
	doneChan chan struct{}

	mu       sync.Mutex
	lastErr  error
	probeErr error
	closedV  bool
}

// newControlPlaneInboundTestTunnel 创建可手动关闭的测试 tunnel。
func newControlPlaneInboundTestTunnel(tunnelID string) *controlPlaneInboundTestTunnel {
	return &controlPlaneInboundTestTunnel{
		meta: transport.TunnelMeta{
			TunnelID:  tunnelID,
			CreatedAt: time.Now().UTC(),
		},
		doneChan: make(chan struct{}),
	}
}

// ID 返回稳定 tunnel_id。
func (tunnel *controlPlaneInboundTestTunnel) ID() string {
	if tunnel == nil {
		return ""
	}
	return strings.TrimSpace(tunnel.meta.TunnelID)
}

// Meta 返回最小 tunnel 元数据。
func (tunnel *controlPlaneInboundTestTunnel) Meta() transport.TunnelMeta {
	if tunnel == nil {
		return transport.TunnelMeta{}
	}
	meta := tunnel.meta
	if strings.TrimSpace(meta.TunnelID) == "" {
		meta.TunnelID = tunnel.ID()
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = time.Now().UTC()
	}
	return meta
}

// State 返回 idle，占位满足接口约束。
func (tunnel *controlPlaneInboundTestTunnel) State() transport.TunnelState {
	return transport.TunnelStateIdle
}

// BindingInfo 返回 tcp_framed 占位绑定信息。
func (tunnel *controlPlaneInboundTestTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: transport.BindingTypeTCPFramed}
}

// Read 在该测试替身中不应被调用。
func (tunnel *controlPlaneInboundTestTunnel) Read(payload []byte) (int, error) {
	_ = payload
	return 0, transport.ErrClosed
}

// Write 在该测试替身中不应被调用。
func (tunnel *controlPlaneInboundTestTunnel) Write(payload []byte) (int, error) {
	_ = payload
	return 0, transport.ErrClosed
}

// Close 关闭 tunnel 并触发 Done。
func (tunnel *controlPlaneInboundTestTunnel) Close() error {
	if tunnel == nil {
		return nil
	}
	tunnel.mu.Lock()
	tunnel.closedV = true
	tunnel.lastErr = transport.ErrClosed
	tunnel.mu.Unlock()
	tunnel.doneOnce.Do(func() { close(tunnel.doneChan) })
	return nil
}

// CloseWrite 测试替身不提供半关闭能力。
func (tunnel *controlPlaneInboundTestTunnel) CloseWrite() error {
	return transport.ErrUnsupported
}

// Reset 以 broken 原因关闭 tunnel。
func (tunnel *controlPlaneInboundTestTunnel) Reset(cause error) error {
	if tunnel == nil {
		return nil
	}
	tunnel.mu.Lock()
	if cause == nil {
		cause = transport.ErrTunnelBroken
	}
	tunnel.closedV = true
	tunnel.lastErr = cause
	tunnel.mu.Unlock()
	tunnel.doneOnce.Do(func() { close(tunnel.doneChan) })
	return nil
}

// SetDeadline 测试替身不需要实际 deadline 行为。
func (tunnel *controlPlaneInboundTestTunnel) SetDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

// SetReadDeadline 测试替身不需要实际 deadline 行为。
func (tunnel *controlPlaneInboundTestTunnel) SetReadDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

// SetWriteDeadline 测试替身不需要实际 deadline 行为。
func (tunnel *controlPlaneInboundTestTunnel) SetWriteDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

// Flush 测试替身默认无脏缓存。
func (tunnel *controlPlaneInboundTestTunnel) Flush() error {
	return nil
}

// ReuseCount 测试替身固定返回 0。
func (tunnel *controlPlaneInboundTestTunnel) ReuseCount() int {
	return 0
}

// Recyclable 返回当前 tunnel 是否处于可回收状态。
func (tunnel *controlPlaneInboundTestTunnel) Recyclable() bool {
	if tunnel == nil {
		return false
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return !tunnel.closedV
}

// Done 返回 tunnel 关闭信号。
func (tunnel *controlPlaneInboundTestTunnel) Done() <-chan struct{} {
	if tunnel == nil {
		closedChan := make(chan struct{})
		close(closedChan)
		return closedChan
	}
	return tunnel.doneChan
}

// Err 返回最近错误。
func (tunnel *controlPlaneInboundTestTunnel) Err() error {
	if tunnel == nil {
		return transport.ErrInvalidArgument
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.lastErr
}

// Probe 返回预设探活错误，用于模拟远端静默断开。
func (tunnel *controlPlaneInboundTestTunnel) Probe(ctx context.Context) error {
	_ = ctx
	if tunnel == nil {
		return transport.ErrInvalidArgument
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.probeErr
}

// closed 返回 tunnel 是否已关闭，供测试断言。
func (tunnel *controlPlaneInboundTestTunnel) closed() bool {
	if tunnel == nil {
		return true
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return tunnel.closedV
}

func (tunnel *controlPlaneInboundTestTunnel) simulateRemoteCloseWithoutDone() {
	if tunnel == nil {
		return
	}
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	tunnel.closedV = true
	tunnel.lastErr = transport.ErrClosed
	tunnel.probeErr = transport.ErrClosed
}
