package quicbinding

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// SessionRole 描述 quic_native session 的运行角色。
type SessionRole string

const (
	// SessionRoleAgent 表示 Agent 侧角色。
	SessionRoleAgent SessionRole = "agent"
	// SessionRoleServer 表示 Server 侧角色。
	SessionRoleServer SessionRole = "server"
)

// SessionConfig 描述 quic_native session 构造参数。
type SessionConfig struct {
	Meta transport.SessionMeta

	ControlChannel transport.ControlChannel
	TunnelProducer transport.TunnelProducer
	TunnelAcceptor transport.TunnelAcceptor
	TunnelPool     transport.TunnelPool
}

// NewSession 按角色创建 quic_native 的 transport.Session 聚合根。
func NewSession(role SessionRole, config SessionConfig) (transport.Session, error) {
	normalizedSessionID := strings.TrimSpace(config.Meta.SessionID)
	if normalizedSessionID == "" {
		return nil, fmt.Errorf("new quic session: %w: empty session id", transport.ErrInvalidArgument)
	}
	normalizedMeta := config.Meta
	normalizedMeta.SessionID = normalizedSessionID

	switch role {
	case SessionRoleAgent:
		if config.TunnelProducer == nil {
			return nil, fmt.Errorf("new quic session: %w: nil tunnel producer", transport.ErrInvalidArgument)
		}
	case SessionRoleServer:
		if config.TunnelAcceptor == nil {
			return nil, fmt.Errorf("new quic session: %w: nil tunnel acceptor", transport.ErrInvalidArgument)
		}
		if config.TunnelPool == nil {
			return nil, fmt.Errorf("new quic session: %w: nil tunnel pool", transport.ErrInvalidArgument)
		}
	default:
		return nil, fmt.Errorf("new quic session: %w: unknown role=%s", transport.ErrInvalidArgument, role)
	}

	// 首批实现先把 QUIC session 能力挂到统一聚合根上，后续再把 control/tunnel
	// 的真实 quic-go 连接与 stream 生命周期接进来。
	return transport.NewInMemorySession(
		normalizedMeta,
		transport.NewBindingInfo(transport.BindingTypeQUICNative),
		transport.SessionCapabilities{
			ControlChannel: config.ControlChannel,
			Producer:       config.TunnelProducer,
			Acceptor:       config.TunnelAcceptor,
			Pool:           config.TunnelPool,
		},
	), nil
}
