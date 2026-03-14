package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
)

const defaultHeartbeatReplyTimeout = 2 * time.Second

type controlPlaneServer struct {
	tcpListenAddr  string
	grpcListenAddr string

	tcpTransport  *tcpbinding.Transport
	grpcTransport *grpcbinding.Transport

	mu           sync.Mutex
	tcpListener  net.Listener
	grpcListener net.Listener
	grpcServer   *grpc.Server
}

func newControlPlaneServer(config ControlPlaneConfig) (*controlPlaneServer, error) {
	tcpTransport, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		return nil, fmt.Errorf("new control plane tcp transport: %w", err)
	}
	grpcTransport, err := grpcbinding.NewTransportWithConfig(grpcbinding.TransportConfig{})
	if err != nil {
		return nil, fmt.Errorf("new control plane grpc transport: %w", err)
	}
	return &controlPlaneServer{
		tcpListenAddr:  strings.TrimSpace(config.ListenAddr),
		grpcListenAddr: strings.TrimSpace(config.GRPCH2ListenAddr),
		tcpTransport:   tcpTransport,
		grpcTransport:  grpcTransport,
	}, nil
}

func (server *controlPlaneServer) run(ctx context.Context) error {
	if server == nil {
		return errors.New("control plane server is nil")
	}
	runners := []func(context.Context) error{server.runTCP}
	if server.grpcListenAddr != "" {
		runners = append(runners, server.runGRPC)
	}
	serverErrChan := make(chan error, len(runners))
	var serverWaitGroup sync.WaitGroup
	for _, run := range runners {
		serverWaitGroup.Add(1)
		go func(runFn func(context.Context) error) {
			defer serverWaitGroup.Done()
			serverErrChan <- runFn(ctx)
		}(run)
	}
	defer serverWaitGroup.Wait()

	var firstErr error
	for range runners {
		runErr := <-serverErrChan
		if runErr != nil && firstErr == nil {
			firstErr = runErr
			_ = server.shutdown()
		}
	}
	return firstErr
}

func (server *controlPlaneServer) runTCP(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.tcpListenAddr)
	if err != nil {
		return fmt.Errorf("listen tcp control plane: %w", err)
	}
	server.mu.Lock()
	server.tcpListener = listener
	server.mu.Unlock()
	defer func() {
		_ = listener.Close()
		server.mu.Lock()
		if server.tcpListener == listener {
			server.tcpListener = nil
		}
		server.mu.Unlock()
	}()

	var channelWaitGroup sync.WaitGroup
	defer channelWaitGroup.Wait()

	for {
		controlChannel, acceptErr := server.tcpTransport.AcceptControlChannel(ctx, listener)
		if acceptErr != nil {
			if isControlPlaneStopError(acceptErr) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept tcp control channel: %w", acceptErr)
		}
		channelWaitGroup.Add(1)
		go func(channel transport.ControlChannel) {
			defer channelWaitGroup.Done()
			if err := serveControlChannel(ctx, channel); err != nil && !isControlChannelClosedError(err) {
				_ = channel.Close(context.Background())
			}
		}(controlChannel)
	}
}

func (server *controlPlaneServer) runGRPC(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.grpcListenAddr)
	if err != nil {
		return fmt.Errorf("listen grpc_h2 control plane: %w", err)
	}
	grpcServer := grpc.NewServer(server.grpcTransport.ServerOptions()...)
	transportgen.RegisterGRPCH2TransportServiceServer(grpcServer, &grpcControlPlaneService{})

	server.mu.Lock()
	server.grpcListener = listener
	server.grpcServer = grpcServer
	server.mu.Unlock()
	defer func() {
		_ = listener.Close()
		server.mu.Lock()
		if server.grpcListener == listener {
			server.grpcListener = nil
		}
		if server.grpcServer == grpcServer {
			server.grpcServer = nil
		}
		server.mu.Unlock()
	}()

	serveErrChan := make(chan error, 1)
	go func() {
		serveErrChan <- grpcServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		serveErr := <-serveErrChan
		if serveErr != nil && !isGRPCServerStopError(serveErr) {
			return fmt.Errorf("serve grpc_h2 control plane: %w", serveErr)
		}
		return nil
	case serveErr := <-serveErrChan:
		if isGRPCServerStopError(serveErr) || ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("serve grpc_h2 control plane: %w", serveErr)
	}
}

func (server *controlPlaneServer) shutdown() error {
	if server == nil {
		return nil
	}
	server.mu.Lock()
	defer server.mu.Unlock()

	var firstErr error
	if server.tcpListener != nil {
		if err := server.tcpListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
		server.tcpListener = nil
	}
	if server.grpcServer != nil {
		server.grpcServer.Stop()
		server.grpcServer = nil
	}
	if server.grpcListener != nil {
		if err := server.grpcListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
		server.grpcListener = nil
	}
	return firstErr
}

type grpcControlPlaneService struct {
	transportgen.UnimplementedGRPCH2TransportServiceServer
}

func (service *grpcControlPlaneService) ControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	return serveGRPCControlChannel(stream)
}

func (service *grpcControlPlaneService) TunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	return serveGRPCTunnelStream(stream)
}

func serveGRPCControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	for {
		frameEnvelope, err := stream.Recv()
		if err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return err
		}
		if frameEnvelope == nil {
			continue
		}
		if frameEnvelope.FrameType != uint32(transport.ControlFrameTypeHeartbeatPing) {
			continue
		}
		replyEnvelope := &transportgen.ControlFrameEnvelope{
			FrameType: uint32(transport.ControlFrameTypeHeartbeatPong),
		}
		if err := stream.Send(replyEnvelope); err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return fmt.Errorf("write heartbeat pong: %w", err)
		}
	}
}

func serveGRPCTunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	for {
		_, err := stream.Recv()
		if err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return err
		}
	}
}

func serveControlChannel(ctx context.Context, controlChannel transport.ControlChannel) error {
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	defer func() {
		_ = controlChannel.Close(context.Background())
	}()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := controlChannel.ReadControlFrame(ctx)
		if err != nil {
			return err
		}
		if frame.Type != transport.ControlFrameTypeHeartbeatPing {
			continue
		}
		replyContext, cancel := context.WithTimeout(ctx, defaultHeartbeatReplyTimeout)
		replyErr := writeControlFrameWithPriority(
			replyContext,
			controlChannel,
			transport.ControlFrame{Type: transport.ControlFrameTypeHeartbeatPong},
			transport.ControlMessagePriorityHigh,
		)
		cancel()
		if replyErr != nil {
			return fmt.Errorf("write heartbeat pong: %w", replyErr)
		}
	}
}

func writeControlFrameWithPriority(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	frame transport.ControlFrame,
	priority transport.ControlMessagePriority,
) error {
	if prioritizedControlChannel, ok := controlChannel.(transport.PrioritizedControlChannel); ok {
		return prioritizedControlChannel.WritePrioritizedControlFrame(ctx, transport.PrioritizedControlFrame{
			Priority: priority,
			Frame:    frame,
		})
	}
	return controlChannel.WriteControlFrame(ctx, frame)
}

func isControlPlaneStopError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}

func isControlChannelClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, transport.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed")
}

func isGRPCServerStopError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed")
}

func isGRPCStreamClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	lowerMessage := strings.ToLower(err.Error())
	return strings.Contains(lowerMessage, "closed") || strings.Contains(lowerMessage, "canceled")
}
