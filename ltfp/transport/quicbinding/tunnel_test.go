package quicbinding

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type testQUICTunnelPair struct {
	listener      *Listener
	clientConn    *Conn
	serverConn    *Conn
	clientControl transport.ControlChannel
	serverControl transport.ControlChannel
	acceptor      *TunnelAcceptor
	clientTunnel  *QUICTunnel
	serverTunnel  *QUICTunnel
}

func TestQUICTunnelReadDeadlineDoesNotBreakTunnel(testingObject *testing.T) {
	tunnelPair := newTestQUICTunnelPair(testingObject)
	defer tunnelPair.Close()

	if err := tunnelPair.clientTunnel.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		testingObject.Fatalf("set quic read deadline failed: %v", err)
	}
	_, err := tunnelPair.clientTunnel.Read(make([]byte, 16))
	if !errors.Is(err, transport.ErrTimeout) {
		testingObject.Fatalf("expected ErrTimeout, got %v", err)
	}
	if tunnelPair.clientTunnel.State() != transport.TunnelStateIdle {
		testingObject.Fatalf("expected idle tunnel after read timeout, got %s", tunnelPair.clientTunnel.State())
	}

	if err := tunnelPair.clientTunnel.SetReadDeadline(time.Time{}); err != nil {
		testingObject.Fatalf("clear quic read deadline failed: %v", err)
	}
	go func() {
		_, _ = tunnelPair.serverTunnel.Write([]byte("after-timeout"))
	}()

	readBuffer := make([]byte, 32)
	readSize, err := tunnelPair.clientTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("read after quic timeout failed: %v", err)
	}
	if string(readBuffer[:readSize]) != "after-timeout" {
		testingObject.Fatalf("unexpected payload after quic timeout: %q", string(readBuffer[:readSize]))
	}
}

func TestQUICTunnelResetMarksBroken(testingObject *testing.T) {
	tunnelPair := newTestQUICTunnelPair(testingObject)
	defer tunnelPair.Close()

	if err := tunnelPair.clientTunnel.Reset(errors.New("agent canceled")); err != nil {
		testingObject.Fatalf("reset quic tunnel failed: %v", err)
	}
	if tunnelPair.clientTunnel.State() != transport.TunnelStateBroken {
		testingObject.Fatalf("expected broken state after reset, got %s", tunnelPair.clientTunnel.State())
	}
	if tunnelPair.clientTunnel.Err() == nil || !strings.Contains(tunnelPair.clientTunnel.Err().Error(), "agent canceled") {
		testingObject.Fatalf("expected reset cause in quic tunnel err, got %v", tunnelPair.clientTunnel.Err())
	}
	if tunnelPair.clientTunnel.Recyclable() {
		testingObject.Fatalf("expected reset quic tunnel not recyclable")
	}
	select {
	case <-tunnelPair.clientTunnel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected quic tunnel done after reset")
	}
}

func TestQUICTunnelPeerResetConvergesBroken(testingObject *testing.T) {
	tunnelPair := newTestQUICTunnelPair(testingObject)
	defer tunnelPair.Close()

	if err := tunnelPair.serverTunnel.Reset(errors.New("bridge reset")); err != nil {
		testingObject.Fatalf("reset server quic tunnel failed: %v", err)
	}
	_, err := tunnelPair.clientTunnel.Read(make([]byte, 16))
	if !errors.Is(err, transport.ErrTunnelBroken) {
		testingObject.Fatalf("expected ErrTunnelBroken after peer reset, got %v", err)
	}
	if tunnelPair.clientTunnel.State() != transport.TunnelStateBroken {
		testingObject.Fatalf("expected broken state after peer reset, got %s", tunnelPair.clientTunnel.State())
	}
	if tunnelPair.clientTunnel.Recyclable() {
		testingObject.Fatalf("expected peer-reset quic tunnel not recyclable")
	}
	select {
	case <-tunnelPair.clientTunnel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected peer-reset quic tunnel done")
	}
}

func TestQUICTunnelIdlePeerCloseConvergesClosed(testingObject *testing.T) {
	tunnelPair := newTestQUICTunnelPair(testingObject)
	defer tunnelPair.Close()

	if err := tunnelPair.serverConn.Close(nil); err != nil {
		testingObject.Fatalf("close server quic conn failed: %v", err)
	}
	select {
	case <-tunnelPair.clientTunnel.Done():
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("expected idle quic tunnel cleanup after peer close")
	}
	if tunnelPair.clientTunnel.State() != transport.TunnelStateClosed {
		testingObject.Fatalf("expected closed state after idle peer close, got %s", tunnelPair.clientTunnel.State())
	}
	if tunnelPair.clientTunnel.Recyclable() {
		testingObject.Fatalf("expected closed quic tunnel not recyclable")
	}
}

func TestQUICTunnelCloseWriteDeliversEOF(testingObject *testing.T) {
	tunnelPair := newTestQUICTunnelPair(testingObject)
	defer tunnelPair.Close()

	if err := tunnelPair.clientTunnel.CloseWrite(); err != nil {
		testingObject.Fatalf("close quic tunnel write failed: %v", err)
	}
	_, err := tunnelPair.serverTunnel.Read(make([]byte, 16))
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF after quic close write, got %v", err)
	}
}

func newTestQUICTunnelPair(testingObject *testing.T) *testQUICTunnelPair {
	testingObject.Helper()

	binding, err := NewTransportWithConfig(TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new quic transport failed: %v", err)
	}
	serverTLSConfig, clientTLSConfig := newTestTLSConfigs(testingObject)
	listener, err := binding.ListenAddr("127.0.0.1:0", serverTLSConfig)
	if err != nil {
		testingObject.Fatalf("listen quic transport failed: %v", err)
	}

	serverConnResult := make(chan acceptConnResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		serverConn, acceptErr := listener.Accept(ctx)
		serverConnResult <- acceptConnResult{conn: serverConn, err: acceptErr}
	}()

	clientContext, cancelClient := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelClient()
	clientConn, err := binding.Dial(clientContext, listener.Addr().String(), clientTLSConfig)
	if err != nil {
		_ = listener.Close()
		testingObject.Fatalf("dial quic transport failed: %v", err)
	}

	serverAccepted := <-serverConnResult
	if serverAccepted.err != nil {
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("accept quic transport failed: %v", serverAccepted.err)
	}
	serverConn := serverAccepted.conn

	serverControlResult := make(chan acceptControlResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		controlChannel, acceptErr := serverConn.AcceptControlChannel(ctx)
		serverControlResult <- acceptControlResult{control: controlChannel, err: acceptErr}
	}()

	clientControl, err := clientConn.OpenControlChannel(context.Background())
	if err != nil {
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("open quic control channel failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(context.Background(), testControlFrame()); err != nil {
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("write bootstrap quic control frame failed: %v", err)
	}

	serverControlAccepted := <-serverControlResult
	if serverControlAccepted.err != nil {
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("accept quic control channel failed: %v", serverControlAccepted.err)
	}
	serverControl := serverControlAccepted.control
	if _, err := serverControl.ReadControlFrame(context.Background()); err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("read bootstrap quic control frame failed: %v", err)
	}

	producer, err := NewTunnelProducer(clientConn, TunnelIdentityConfig{
		SessionID:      "session-quic-tunnel-test",
		SessionEpoch:   9,
		TunnelIDPrefix: "quic-tunnel-test",
	})
	if err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("new quic tunnel producer failed: %v", err)
	}
	acceptor, err := NewTunnelAcceptor(serverConn, TunnelAcceptorConfig{})
	if err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("new quic tunnel acceptor failed: %v", err)
	}

	serverTunnelResult := make(chan acceptTunnelResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tunnel, acceptErr := acceptor.AcceptTunnel(ctx)
		serverTunnelResult <- acceptTunnelResult{tunnel: tunnel, err: acceptErr}
	}()

	clientTunnel, err := producer.OpenTunnel(context.Background())
	if err != nil {
		acceptor.Close(nil)
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("open quic tunnel failed: %v", err)
	}

	serverAcceptedTunnel := <-serverTunnelResult
	if serverAcceptedTunnel.err != nil {
		_ = clientTunnel.Close()
		acceptor.Close(nil)
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("accept quic tunnel failed: %v", serverAcceptedTunnel.err)
	}

	serverTunnel, ok := serverAcceptedTunnel.tunnel.(*QUICTunnel)
	if !ok {
		_ = clientTunnel.Close()
		acceptor.Close(nil)
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("expected *QUICTunnel from acceptor, got %T", serverAcceptedTunnel.tunnel)
	}
	clientQUICTunnel, ok := clientTunnel.(*QUICTunnel)
	if !ok {
		_ = clientTunnel.Close()
		_ = serverTunnel.Close()
		acceptor.Close(nil)
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("expected *QUICTunnel from producer, got %T", clientTunnel)
	}

	return &testQUICTunnelPair{
		listener:      listener,
		clientConn:    clientConn,
		serverConn:    serverConn,
		clientControl: clientControl,
		serverControl: serverControl,
		acceptor:      acceptor,
		clientTunnel:  clientQUICTunnel,
		serverTunnel:  serverTunnel,
	}
}

func (pair *testQUICTunnelPair) Close() {
	if pair == nil {
		return
	}
	if pair.clientTunnel != nil {
		_ = pair.clientTunnel.Close()
	}
	if pair.serverTunnel != nil {
		_ = pair.serverTunnel.Close()
	}
	if pair.acceptor != nil {
		pair.acceptor.Close(nil)
	}
	if pair.clientControl != nil {
		_ = pair.clientControl.Close(context.Background())
	}
	if pair.serverControl != nil {
		_ = pair.serverControl.Close(context.Background())
	}
	if pair.clientConn != nil {
		_ = pair.clientConn.Close(nil)
	}
	if pair.serverConn != nil {
		_ = pair.serverConn.Close(nil)
	}
	if pair.listener != nil {
		_ = pair.listener.Close()
	}
}
