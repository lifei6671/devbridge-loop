package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

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
}
