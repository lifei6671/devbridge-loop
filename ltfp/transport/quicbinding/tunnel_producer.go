package quicbinding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TunnelProducer 实现 Agent 侧主动打开 QUIC tunnel stream 的能力。
type TunnelProducer struct {
	conn *Conn

	identityConfig TunnelIdentityConfig
	idGenerator    *tunnelIDGenerator
}

var _ transport.TunnelProducer = (*TunnelProducer)(nil)

// NewTunnelProducer 创建 QUIC tunnel producer。
func NewTunnelProducer(conn *Conn, identityConfig TunnelIdentityConfig) (*TunnelProducer, error) {
	if conn == nil {
		return nil, fmt.Errorf("new quic tunnel producer: %w: nil conn", transport.ErrInvalidArgument)
	}
	normalizedIdentityConfig := identityConfig.normalized()
	return &TunnelProducer{
		conn:           conn,
		identityConfig: normalizedIdentityConfig,
		idGenerator:    newTunnelIDGenerator(normalizedIdentityConfig.TunnelIDPrefix),
	}, nil
}

// OpenTunnel 主动打开一条 QUIC tunnel stream，并先写入 stream 级握手头。
func (producer *TunnelProducer) OpenTunnel(ctx context.Context) (transport.Tunnel, error) {
	if producer == nil {
		return nil, fmt.Errorf("open quic tunnel: %w: nil producer", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stream, err := producer.conn.openTunnelStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open quic tunnel: %w", err)
	}
	tunnelID := producer.idGenerator.Next()
	tunnelMeta := buildTunnelMeta(producer.identityConfig, tunnelID, time.Now().UTC())
	dialLocalAddr := ""
	if producer.conn != nil && producer.conn.LocalAddr() != nil {
		dialLocalAddr = strings.TrimSpace(producer.conn.LocalAddr().String())
	}
	if err := writeTunnelHandshake(stream, tunnelMeta, dialLocalAddr); err != nil {
		producer.conn.resetStream(stream)
		return nil, fmt.Errorf("open quic tunnel: %w", err)
	}
	quicTunnel, err := NewQUICTunnel(stream, tunnelMeta)
	if err != nil {
		producer.conn.resetStream(stream)
		return nil, fmt.Errorf("open quic tunnel: %w", err)
	}
	return quicTunnel, nil
}
