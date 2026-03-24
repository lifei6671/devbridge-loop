package quicbinding

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

func TestTransportDialAndControlChannelRoundTrip(testingObject *testing.T) {
	binding, err := NewTransportWithConfig(TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new quic transport failed: %v", err)
	}
	serverTLSConfig, clientTLSConfig := newTestTLSConfigs(testingObject)
	listener, err := binding.ListenAddr("127.0.0.1:0", serverTLSConfig)
	if err != nil {
		testingObject.Fatalf("listen quic transport failed: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

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
		testingObject.Fatalf("dial quic transport failed: %v", err)
	}
	defer func() {
		_ = clientConn.Close(nil)
	}()

	serverAccepted := <-serverConnResult
	if serverAccepted.err != nil {
		testingObject.Fatalf("accept quic transport failed: %v", serverAccepted.err)
	}
	serverConn := serverAccepted.conn
	defer func() {
		_ = serverConn.Close(nil)
	}()

	serverControlResult := make(chan acceptControlResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		controlChannel, acceptErr := serverConn.AcceptControlChannel(ctx)
		serverControlResult <- acceptControlResult{control: controlChannel, err: acceptErr}
	}()

	controlContext, cancelControl := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelControl()
	clientControl, err := clientConn.OpenControlChannel(controlContext)
	if err != nil {
		testingObject.Fatalf("open quic control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()

	expectedFrame := testControlFrame()
	writeContext, cancelWrite := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWrite()
	if err := clientControl.WriteControlFrame(writeContext, expectedFrame); err != nil {
		testingObject.Fatalf("write quic control frame failed: %v", err)
	}

	serverControlAccepted := <-serverControlResult
	if serverControlAccepted.err != nil {
		testingObject.Fatalf("accept quic control channel failed: %v", serverControlAccepted.err)
	}
	serverControl := serverControlAccepted.control
	defer func() {
		_ = serverControl.Close(context.Background())
	}()

	readContext, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	serverFrame, err := serverControl.ReadControlFrame(readContext)
	if err != nil {
		testingObject.Fatalf("read quic control frame failed: %v", err)
	}
	if serverFrame.Type != expectedFrame.Type || string(serverFrame.Payload) != string(expectedFrame.Payload) {
		testingObject.Fatalf("unexpected control frame: got=%+v want=%+v", serverFrame, expectedFrame)
	}
}

func TestTunnelProducerAndAcceptorRoundTrip(testingObject *testing.T) {
	binding, err := NewTransportWithConfig(TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new quic transport failed: %v", err)
	}
	serverTLSConfig, clientTLSConfig := newTestTLSConfigs(testingObject)
	listener, err := binding.ListenAddr("127.0.0.1:0", serverTLSConfig)
	if err != nil {
		testingObject.Fatalf("listen quic transport failed: %v", err)
	}
	defer func() {
		_ = listener.Close()
	}()

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
		testingObject.Fatalf("dial quic transport failed: %v", err)
	}
	defer func() {
		_ = clientConn.Close(nil)
	}()

	serverAccepted := <-serverConnResult
	if serverAccepted.err != nil {
		testingObject.Fatalf("accept quic transport failed: %v", serverAccepted.err)
	}
	serverConn := serverAccepted.conn
	defer func() {
		_ = serverConn.Close(nil)
	}()

	serverControlResult := make(chan acceptControlResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		controlChannel, acceptErr := serverConn.AcceptControlChannel(ctx)
		serverControlResult <- acceptControlResult{control: controlChannel, err: acceptErr}
	}()
	clientControl, err := clientConn.OpenControlChannel(context.Background())
	if err != nil {
		testingObject.Fatalf("open quic control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()
	if err := clientControl.WriteControlFrame(context.Background(), testControlFrame()); err != nil {
		testingObject.Fatalf("write bootstrap quic control frame failed: %v", err)
	}
	serverControlAccepted := <-serverControlResult
	if serverControlAccepted.err != nil {
		testingObject.Fatalf("accept quic control channel failed: %v", serverControlAccepted.err)
	}
	serverControl := serverControlAccepted.control
	defer func() {
		_ = serverControl.Close(context.Background())
	}()
	if _, err := serverControl.ReadControlFrame(context.Background()); err != nil {
		testingObject.Fatalf("read bootstrap quic control frame failed: %v", err)
	}

	producer, err := NewTunnelProducer(clientConn, TunnelIdentityConfig{
		SessionID:      "session-quic-1",
		SessionEpoch:   7,
		TunnelIDPrefix: "quic-test",
	})
	if err != nil {
		testingObject.Fatalf("new quic tunnel producer failed: %v", err)
	}
	acceptor, err := NewTunnelAcceptor(serverConn, TunnelAcceptorConfig{})
	if err != nil {
		testingObject.Fatalf("new quic tunnel acceptor failed: %v", err)
	}
	defer acceptor.Close(nil)

	serverTunnelResult := make(chan acceptTunnelResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tunnel, acceptErr := acceptor.AcceptTunnel(ctx)
		serverTunnelResult <- acceptTunnelResult{tunnel: tunnel, err: acceptErr}
	}()

	clientTunnel, err := producer.OpenTunnel(context.Background())
	if err != nil {
		testingObject.Fatalf("open quic tunnel failed: %v", err)
	}
	defer func() {
		_ = clientTunnel.Close()
	}()

	serverAcceptedTunnel := <-serverTunnelResult
	if serverAcceptedTunnel.err != nil {
		testingObject.Fatalf("accept quic tunnel failed: %v", serverAcceptedTunnel.err)
	}
	serverTunnel := serverAcceptedTunnel.tunnel
	defer func() {
		_ = serverTunnel.Close()
	}()

	serverMeta := serverTunnel.Meta()
	if serverMeta.SessionID != "session-quic-1" || serverMeta.SessionEpoch != 7 {
		testingObject.Fatalf("unexpected tunnel meta: %+v", serverMeta)
	}
	if serverTunnel.ID() == "" {
		testingObject.Fatalf("expected non-empty tunnel id")
	}

	clientPayload := []byte("hello over quic tunnel")
	if _, err := clientTunnel.Write(clientPayload); err != nil {
		testingObject.Fatalf("client write quic tunnel failed: %v", err)
	}
	readBuffer := make([]byte, len(clientPayload))
	readSize, err := serverTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("server read quic tunnel failed: %v", err)
	}
	if string(readBuffer[:readSize]) != string(clientPayload) {
		testingObject.Fatalf("unexpected client payload: got=%q want=%q", string(readBuffer[:readSize]), string(clientPayload))
	}

	serverPayload := []byte("pong from bridge")
	if _, err := serverTunnel.Write(serverPayload); err != nil {
		testingObject.Fatalf("server write quic tunnel failed: %v", err)
	}
	clientBuffer := make([]byte, len(serverPayload))
	clientReadSize, err := clientTunnel.Read(clientBuffer)
	if err != nil {
		testingObject.Fatalf("client read quic tunnel failed: %v", err)
	}
	if string(clientBuffer[:clientReadSize]) != string(serverPayload) {
		testingObject.Fatalf("unexpected server payload: got=%q want=%q", string(clientBuffer[:clientReadSize]), string(serverPayload))
	}
}

type acceptConnResult struct {
	conn *Conn
	err  error
}

type acceptControlResult struct {
	control transport.ControlChannel
	err     error
}

type acceptTunnelResult struct {
	tunnel transport.Tunnel
	err    error
}

func testControlFrame() transport.ControlFrame {
	return transport.ControlFrame{
		Type:    0x1001,
		Payload: []byte("quic-control-frame"),
	}
}

func newTestTLSConfigs(testingObject *testing.T) (*tls.Config, *tls.Config) {
	testingObject.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		testingObject.Fatalf("generate test ed25519 key failed: %v", err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
		},
		DNSNames:              []string{"localhost"},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, publicKey, privateKey)
	if err != nil {
		testingObject.Fatalf("create test certificate failed: %v", err)
	}
	tlsCertificate := tls.Certificate{
		Certificate: [][]byte{certificateDER},
		PrivateKey:  privateKey,
	}
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCertificate},
		NextProtos:   []string{defaultALPN},
	}
	clientTLSConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{defaultALPN},
		ServerName:         "127.0.0.1",
	}
	return serverTLSConfig, clientTLSConfig
}
