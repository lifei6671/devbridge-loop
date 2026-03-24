package quicbinding

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const defaultTunnelAcceptorQueueSize = 128

// TunnelAcceptorConfig 描述 QUIC tunnel acceptor 配置。
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

// TunnelAcceptor 实现 Server 侧接收 QUIC tunnel 的能力。
type TunnelAcceptor struct {
	conn *Conn

	identityConfig TunnelIdentityConfig
	pendingTunnels chan transport.Tunnel
	doneChannel    chan struct{}
	doneOnce       sync.Once

	stateMutex sync.Mutex
	lastError  error
}

var _ transport.TunnelAcceptor = (*TunnelAcceptor)(nil)

// NewTunnelAcceptor 创建 tunnel acceptor，并启动底层 stream 分发循环。
func NewTunnelAcceptor(conn *Conn, config TunnelAcceptorConfig) (*TunnelAcceptor, error) {
	if conn == nil {
		return nil, fmt.Errorf("new quic tunnel acceptor: %w: nil conn", transport.ErrInvalidArgument)
	}
	normalizedConfig := config.normalized()
	acceptor := &TunnelAcceptor{
		conn:           conn,
		identityConfig: normalizedConfig.IdentityConfig,
		pendingTunnels: make(chan transport.Tunnel, normalizedConfig.QueueSize),
		doneChannel:    make(chan struct{}),
	}
	go acceptor.runAcceptLoop()
	return acceptor, nil
}

// AcceptTunnel 返回下一条接收到的 tunnel。
func (acceptor *TunnelAcceptor) AcceptTunnel(ctx context.Context) (transport.Tunnel, error) {
	if acceptor == nil {
		return nil, fmt.Errorf("accept quic tunnel: %w: nil acceptor", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	closedErr := fmt.Errorf("accept quic tunnel: %w", transport.ErrClosed)
	for {
		select {
		case <-acceptor.doneChannel:
			return nil, closedErr
		case <-ctx.Done():
			return nil, fmt.Errorf("accept quic tunnel: %w", ctx.Err())
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
				continue
			}
			return tunnel, nil
		}
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

// Err 返回最近错误。
func (acceptor *TunnelAcceptor) Err() error {
	if acceptor == nil {
		return transport.ErrInvalidArgument
	}
	acceptor.stateMutex.Lock()
	defer acceptor.stateMutex.Unlock()
	return acceptor.lastError
}

func (acceptor *TunnelAcceptor) runAcceptLoop() {
	for {
		stream, err := acceptor.conn.acceptTunnelStream(context.Background())
		if err != nil {
			acceptor.Close(err)
			return
		}
		handshake, err := readTunnelHandshake(stream, defaultTunnelHandshakeMaxPayloadSize)
		if err != nil {
			acceptor.conn.resetStream(stream)
			continue
		}
		tunnelMeta := tunnelMetaFromHandshake(handshake)
		if tunnelMeta.TunnelID == "" {
			acceptor.conn.resetStream(stream)
			continue
		}
		if tunnelMeta.SessionID == "" {
			tunnelMeta.SessionID = strings.TrimSpace(acceptor.identityConfig.SessionID)
		}
		if tunnelMeta.SessionEpoch == 0 {
			tunnelMeta.SessionEpoch = acceptor.identityConfig.SessionEpoch
		}
		if len(acceptor.identityConfig.Labels) > 0 {
			if tunnelMeta.Labels == nil {
				tunnelMeta.Labels = make(map[string]string, len(acceptor.identityConfig.Labels))
			}
			for key, value := range acceptor.identityConfig.Labels {
				tunnelMeta.Labels[key] = value
			}
		}
		acceptedTunnel, err := NewQUICTunnel(stream, tunnelMeta)
		if err != nil {
			acceptor.conn.resetStream(stream)
			continue
		}
		select {
		case <-acceptor.doneChannel:
			_ = acceptedTunnel.Close()
			return
		case acceptor.pendingTunnels <- acceptedTunnel:
		}
	}
}
