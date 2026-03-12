package tcpbinding

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const defaultTunnelAcceptorQueueSize = 128

// TunnelAcceptorConfig 描述 TCP tunnel acceptor 配置。
type TunnelAcceptorConfig struct {
	IdentityConfig TunnelIdentityConfig
	QueueSize      int
}

// normalized 返回标准化后的 acceptor 配置。
func (config TunnelAcceptorConfig) normalized() TunnelAcceptorConfig {
	normalizedConfig := config
	normalizedConfig.IdentityConfig = config.IdentityConfig.normalized()
	if normalizedConfig.QueueSize <= 0 {
		// 未配置队列长度时使用默认值，避免 accept loop 与消费方完全串行。
		normalizedConfig.QueueSize = defaultTunnelAcceptorQueueSize
	}
	return normalizedConfig
}

// TunnelAcceptor 实现 Server 侧接收 TCP tunnel 的能力。
type TunnelAcceptor struct {
	binding  *Transport
	listener net.Listener

	identityConfig TunnelIdentityConfig
	idGenerator    *tunnelIDGenerator

	pendingTunnels chan transport.Tunnel
	doneChannel    chan struct{}
	doneOnce       sync.Once

	stateMutex sync.Mutex
	lastError  error
}

var _ transport.TunnelAcceptor = (*TunnelAcceptor)(nil)

// NewTunnelAcceptor 使用默认 transport 配置创建 acceptor。
func NewTunnelAcceptor(listener net.Listener, config TunnelAcceptorConfig) (*TunnelAcceptor, error) {
	return NewTunnelAcceptorWithTransport(nil, listener, config)
}

// NewTunnelAcceptorWithTransport 使用指定 transport 配置创建 acceptor。
func NewTunnelAcceptorWithTransport(
	binding *Transport,
	listener net.Listener,
	config TunnelAcceptorConfig,
) (*TunnelAcceptor, error) {
	if listener == nil {
		return nil, fmt.Errorf("new tcp tunnel acceptor: %w: nil listener", transport.ErrInvalidArgument)
	}
	normalizedConfig := config.normalized()
	if binding == nil {
		binding = NewTransport()
	}
	return &TunnelAcceptor{
		binding:        binding,
		listener:       listener,
		identityConfig: normalizedConfig.IdentityConfig,
		idGenerator:    newTunnelIDGenerator(normalizedConfig.IdentityConfig.TunnelIDPrefix),
		pendingTunnels: make(chan transport.Tunnel, normalizedConfig.QueueSize),
		doneChannel:    make(chan struct{}),
	}, nil
}

// Serve 持续接收新的 TCP tunnel 并放入 pending 队列。
func (acceptor *TunnelAcceptor) Serve(ctx context.Context) error {
	if acceptor == nil {
		return fmt.Errorf("serve tcp tunnel acceptor: %w: nil acceptor", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		// accept loop 内部统一要求非 nil ctx。
		ctx = context.Background()
	}
	for {
		select {
		case <-acceptor.doneChannel:
			return fmt.Errorf("serve tcp tunnel acceptor: %w", transport.ErrClosed)
		default:
		}

		tunnelID := acceptor.idGenerator.Next()
		tunnelMeta := buildTunnelMeta(acceptor.identityConfig, tunnelID, time.Now().UTC())
		tunnel, err := acceptor.binding.AcceptTunnel(ctx, acceptor.listener, tunnelMeta)
		if err != nil {
			if ctx.Err() != nil {
				acceptor.Close(ctx.Err())
				return ctx.Err()
			}
			if errors.Is(err, transport.ErrClosed) || errors.Is(err, net.ErrClosed) {
				acceptor.Close(transport.ErrClosed)
				return fmt.Errorf("serve tcp tunnel acceptor: %w", transport.ErrClosed)
			}
			acceptor.Close(err)
			return fmt.Errorf("serve tcp tunnel acceptor: %w", err)
		}
		select {
		case <-acceptor.doneChannel:
			_ = tunnel.Close()
			return fmt.Errorf("serve tcp tunnel acceptor: %w", transport.ErrClosed)
		case <-ctx.Done():
			_ = tunnel.Close()
			acceptor.Close(ctx.Err())
			return ctx.Err()
		case acceptor.pendingTunnels <- tunnel:
			// 新接入的 tunnel 进入 pending 队列，由上层按需消费。
			select {
			case <-acceptor.doneChannel:
				acceptor.drainPendingTunnels()
				return fmt.Errorf("serve tcp tunnel acceptor: %w", transport.ErrClosed)
			default:
			}
			if ctx.Err() != nil {
				acceptor.Close(ctx.Err())
				return ctx.Err()
			}
		}
	}
}

// AcceptTunnel 返回下一条可用的 tunnel。
func (acceptor *TunnelAcceptor) AcceptTunnel(ctx context.Context) (transport.Tunnel, error) {
	if acceptor == nil {
		return nil, fmt.Errorf("accept tcp tunnel: %w: nil acceptor", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		// AcceptTunnel 的 ctx 允许为 nil，内部回退到背景上下文。
		ctx = context.Background()
	}
	closedErr := fmt.Errorf("accept tcp tunnel: %w", transport.ErrClosed)
	for {
		select {
		case <-acceptor.doneChannel:
			return nil, closedErr
		case <-ctx.Done():
			return nil, fmt.Errorf("accept tcp tunnel: %w", ctx.Err())
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
			if tunnel.State().IsTerminal() {
				// 已经 broken/closed 的 tunnel 不再交给上层。
				continue
			}
			return tunnel, nil
		}
	}
}

// AcceptTunnelToPool 接收一条 tunnel 并写入 idle pool。
func (acceptor *TunnelAcceptor) AcceptTunnelToPool(
	ctx context.Context,
	pool transport.TunnelPool,
) (transport.Tunnel, error) {
	if pool == nil {
		return nil, fmt.Errorf("accept tcp tunnel to pool: %w: nil pool", transport.ErrInvalidArgument)
	}
	tunnel, err := acceptor.AcceptTunnel(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept tcp tunnel to pool: %w", err)
	}
	if err := pool.PutIdle(tunnel); err != nil {
		_ = tunnel.Reset(err)
		return nil, fmt.Errorf("accept tcp tunnel to pool: %w", err)
	}
	return tunnel, nil
}

// Close 停止 acceptor，并关闭底层 listener。
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
		_ = acceptor.listener.Close()
		close(acceptor.doneChannel)
		acceptor.drainPendingTunnels()
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

// Err 返回最近错误。
func (acceptor *TunnelAcceptor) Err() error {
	if acceptor == nil {
		return transport.ErrInvalidArgument
	}
	acceptor.stateMutex.Lock()
	defer acceptor.stateMutex.Unlock()
	return acceptor.lastError
}

func (acceptor *TunnelAcceptor) drainPendingTunnels() {
	for {
		select {
		case tunnel := <-acceptor.pendingTunnels:
			if tunnel == nil {
				continue
			}
			// 关闭尚未交付给上层的 tunnel，避免关闭 acceptor 后遗留 fd。
			_ = tunnel.Close()
		default:
			return
		}
	}
}
