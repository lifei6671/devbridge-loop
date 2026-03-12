package grpcbinding

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"google.golang.org/grpc"
)

const defaultTunnelAcceptorQueueSize = 128

// TunnelAcceptorConfig 描述 grpc tunnel acceptor 配置。
type TunnelAcceptorConfig struct {
	IdentityConfig TunnelIdentityConfig
	QueueSize      int
}

func (config TunnelAcceptorConfig) normalized() TunnelAcceptorConfig {
	normalizedConfig := config
	normalizedConfig.IdentityConfig = config.IdentityConfig.normalized()
	if normalizedConfig.QueueSize <= 0 {
		normalizedConfig.QueueSize = defaultTunnelAcceptorQueueSize
	}
	return normalizedConfig
}

// TunnelAcceptor 实现 Server 侧接收 TunnelStream 的能力。
type TunnelAcceptor struct {
	identityConfig TunnelIdentityConfig
	idGenerator    *tunnelIDGenerator

	pendingTunnels chan transport.Tunnel
	doneChannel    chan struct{}
	doneOnce       sync.Once

	stateMutex sync.Mutex
	lastError  error
}

var _ transport.TunnelAcceptor = (*TunnelAcceptor)(nil)

// NewTunnelAcceptor 创建 tunnel acceptor。
func NewTunnelAcceptor(config TunnelAcceptorConfig) *TunnelAcceptor {
	normalizedConfig := config.normalized()
	return &TunnelAcceptor{
		identityConfig: normalizedConfig.IdentityConfig,
		idGenerator:    newTunnelIDGenerator(normalizedConfig.IdentityConfig.TunnelIDPrefix),
		pendingTunnels: make(chan transport.Tunnel, normalizedConfig.QueueSize),
		doneChannel:    make(chan struct{}),
	}
}

// AcceptTunnel 返回下一条接收到的 tunnel。
func (acceptor *TunnelAcceptor) AcceptTunnel(ctx context.Context) (transport.Tunnel, error) {
	if acceptor == nil {
		return nil, fmt.Errorf("accept grpc tunnel: %w: nil acceptor", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	closedErr := fmt.Errorf("accept grpc tunnel: %w", transport.ErrClosed)
	for {
		select {
		case <-acceptor.doneChannel:
			return nil, closedErr
		case <-ctx.Done():
			return nil, fmt.Errorf("accept grpc tunnel: %w", ctx.Err())
		case tunnel := <-acceptor.pendingTunnels:
			if tunnel == nil {
				continue
			}
			select {
			case <-acceptor.doneChannel:
				_ = tunnel.Reset(transport.ErrClosed)
				return nil, closedErr
			default:
			}
			// 过滤已终止 tunnel，避免把 broken/closed tunnel 交给调用方。
			if tunnel.State().IsTerminal() {
				continue
			}
			return tunnel, nil
		}
	}
}

// AcceptTunnelToPool 接收一条 tunnel 并写入指定 idle pool。
func (acceptor *TunnelAcceptor) AcceptTunnelToPool(
	ctx context.Context,
	pool transport.TunnelPool,
) (transport.Tunnel, error) {
	if pool == nil {
		return nil, fmt.Errorf("accept grpc tunnel to pool: %w: nil pool", transport.ErrInvalidArgument)
	}
	tunnel, err := acceptor.AcceptTunnel(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept grpc tunnel to pool: %w", err)
	}
	if err := pool.PutIdle(tunnel); err != nil {
		_ = tunnel.Reset(err)
		return nil, fmt.Errorf("accept grpc tunnel to pool: %w", err)
	}
	return tunnel, nil
}

// HandleTunnelStream 处理单条 gRPC TunnelStream，并将其入队给 AcceptTunnel 消费。
func (acceptor *TunnelAcceptor) HandleTunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	if acceptor == nil {
		return fmt.Errorf("handle grpc tunnel stream: %w: nil acceptor", transport.ErrInvalidArgument)
	}
	if stream == nil {
		return fmt.Errorf("handle grpc tunnel stream: %w: nil stream", transport.ErrInvalidArgument)
	}
	tunnelStream, err := newGRPCH2TunnelStream(&serverTunnelStreamAdapter{stream: stream})
	if err != nil {
		return fmt.Errorf("handle grpc tunnel stream: %w", err)
	}
	tunnelID := acceptor.idGenerator.Next()
	tunnelMeta := buildTunnelMeta(acceptor.identityConfig, tunnelID, time.Now().UTC())
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, tunnelMeta)
	if err != nil {
		_ = tunnelStream.Close(context.Background())
		return fmt.Errorf("handle grpc tunnel stream: %w", err)
	}
	select {
	case <-acceptor.doneChannel:
		_ = tunnel.Close()
		return fmt.Errorf("handle grpc tunnel stream: %w", transport.ErrClosed)
	case <-stream.Context().Done():
		_ = tunnel.Reset(stream.Context().Err())
		return stream.Context().Err()
	case acceptor.pendingTunnels <- tunnel:
	}
	select {
	case <-acceptor.doneChannel:
		_ = tunnel.Close()
		return fmt.Errorf("handle grpc tunnel stream: %w", transport.ErrClosed)
	case <-stream.Context().Done():
		_ = tunnel.Reset(stream.Context().Err())
		return stream.Context().Err()
	case <-tunnel.Done():
		if tunnelErr := tunnel.Err(); tunnelErr != nil && !errors.Is(tunnelErr, transport.ErrClosed) {
			return fmt.Errorf("handle grpc tunnel stream: %w", tunnelErr)
		}
		return nil
	}
}

// Close 停止 acceptor，后续 AcceptTunnel 将返回 closed 错误。
func (acceptor *TunnelAcceptor) Close(cause error) {
	if acceptor == nil {
		return
	}
	acceptor.doneOnce.Do(func() {
		acceptor.stateMutex.Lock()
		if cause == nil {
			acceptor.lastError = transport.ErrClosed
		} else {
			acceptor.lastError = cause
		}
		acceptor.stateMutex.Unlock()
		close(acceptor.doneChannel)
	})
}

// Done 返回 acceptor 关闭信号。
func (acceptor *TunnelAcceptor) Done() <-chan struct{} {
	if acceptor == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return acceptor.doneChannel
}

// Err 返回 acceptor 最近错误。
func (acceptor *TunnelAcceptor) Err() error {
	if acceptor == nil {
		return transport.ErrInvalidArgument
	}
	acceptor.stateMutex.Lock()
	defer acceptor.stateMutex.Unlock()
	return acceptor.lastError
}

type serverTunnelStreamAdapter struct {
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope]
}

func (adapter *serverTunnelStreamAdapter) Send(frame *transportgen.TunnelEnvelope) error {
	return adapter.stream.Send(frame)
}

func (adapter *serverTunnelStreamAdapter) Recv() (*transportgen.TunnelEnvelope, error) {
	return adapter.stream.Recv()
}

func (adapter *serverTunnelStreamAdapter) Context() context.Context {
	return adapter.stream.Context()
}
