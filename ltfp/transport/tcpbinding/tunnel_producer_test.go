package tcpbinding

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTunnelProducerAndAcceptorServe 验证 producer 与 acceptor 能围绕真实 TCP listener 建立 tunnel。
func TestTunnelProducerAndAcceptorServe(testingObject *testing.T) {
	listener, err := newTCPTestListener()
	if err != nil {
		testingObject.Fatalf("create listener failed: %v", err)
	}
	defer listener.Close()

	acceptor, err := NewTunnelAcceptor(listener, TunnelAcceptorConfig{
		IdentityConfig: TunnelIdentityConfig{
			SessionID:      "server-session",
			SessionEpoch:   12,
			TunnelIDPrefix: "server-tunnel",
		},
		QueueSize: 1,
	})
	if err != nil {
		testingObject.Fatalf("create tunnel acceptor failed: %v", err)
	}

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveResultChannel := make(chan error, 1)
	go func() {
		serveResultChannel <- acceptor.Serve(serveContext)
	}()

	producer, err := NewTunnelProducer(listener.Addr().String(), TunnelIdentityConfig{
		SessionID:      "agent-session",
		SessionEpoch:   8,
		TunnelIDPrefix: "agent-tunnel",
	})
	if err != nil {
		testingObject.Fatalf("create tunnel producer failed: %v", err)
	}

	producerTunnel, err := producer.OpenTunnel(context.Background())
	if err != nil {
		testingObject.Fatalf("open producer tunnel failed: %v", err)
	}
	acceptedTunnel, err := acceptor.AcceptTunnel(context.Background())
	if err != nil {
		testingObject.Fatalf("accept tunnel failed: %v", err)
	}

	if acceptedTunnel.BindingInfo().Type != transport.BindingTypeTCPFramed {
		testingObject.Fatalf("expected tcp_framed binding, got %s", acceptedTunnel.BindingInfo().Type)
	}
	meta := acceptedTunnel.Meta()
	if meta.SessionID != "server-session" || meta.SessionEpoch != 12 {
		testingObject.Fatalf("unexpected accepted tunnel meta: %+v", meta)
	}
	if !strings.HasPrefix(meta.TunnelID, "server-tunnel-") {
		testingObject.Fatalf("unexpected tunnel id: %s", meta.TunnelID)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		_, writeErr := acceptedTunnel.Write([]byte("from-server"))
		writeResultChannel <- writeErr
	}()

	readBuffer := make([]byte, 32)
	readSize, err := producerTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("producer tunnel read failed: %v", err)
	}
	if string(readBuffer[:readSize]) != "from-server" {
		testingObject.Fatalf("unexpected payload from accepted tunnel: %q", string(readBuffer[:readSize]))
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("accepted tunnel write failed: %v", writeErr)
	}

	cancelServe()
	select {
	case serveErr := <-serveResultChannel:
		if serveErr != nil && serveErr != context.Canceled {
			testingObject.Fatalf("unexpected serve result: %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("acceptor serve did not exit")
	}
}

// TestTunnelAcceptorCloseDrainsPendingTunnels 验证关闭 acceptor 时会回收尚未消费的 pending tunnel。
func TestTunnelAcceptorCloseDrainsPendingTunnels(testingObject *testing.T) {
	listener, err := newTCPTestListener()
	if err != nil {
		testingObject.Fatalf("create listener failed: %v", err)
	}
	defer listener.Close()

	acceptor, err := NewTunnelAcceptor(listener, TunnelAcceptorConfig{QueueSize: 1})
	if err != nil {
		testingObject.Fatalf("create tunnel acceptor failed: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	tunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "pending-close"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create tunnel failed: %v", err)
	}
	acceptor.pendingTunnels <- tunnel

	acceptor.Close(transport.ErrClosed)
	select {
	case <-tunnel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected pending tunnel done after acceptor close")
	}
	if tunnel.State() != transport.TunnelStateClosed {
		testingObject.Fatalf("expected pending tunnel closed, got %s", tunnel.State())
	}
}
