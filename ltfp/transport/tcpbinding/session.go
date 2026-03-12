package tcpbinding

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// SessionRole 描述 tcp_framed session 的运行角色。
type SessionRole string

const (
	// SessionRoleAgent 表示 Agent 侧角色。
	SessionRoleAgent SessionRole = "agent"
	// SessionRoleServer 表示 Server 侧角色。
	SessionRoleServer SessionRole = "server"
)

// SessionConfig 描述 tcp_framed session 构造参数。
type SessionConfig struct {
	Meta transport.SessionMeta

	ControlChannel transport.ControlChannel
	TunnelProducer transport.TunnelProducer
	TunnelAcceptor transport.TunnelAcceptor
	TunnelPool     transport.TunnelPool
}

// NewSession 按角色创建 tcp_framed 的 transport.Session 聚合根。
func NewSession(role SessionRole, config SessionConfig) (transport.Session, error) {
	normalizedSessionID := strings.TrimSpace(config.Meta.SessionID)
	if normalizedSessionID == "" {
		return nil, fmt.Errorf("new tcp session: %w: empty session id", transport.ErrInvalidArgument)
	}
	normalizedMeta := config.Meta
	normalizedMeta.SessionID = normalizedSessionID

	switch role {
	case SessionRoleAgent:
		if config.TunnelProducer == nil {
			return nil, fmt.Errorf("new tcp session: %w: nil tunnel producer", transport.ErrInvalidArgument)
		}
	case SessionRoleServer:
		if config.TunnelAcceptor == nil {
			return nil, fmt.Errorf("new tcp session: %w: nil tunnel acceptor", transport.ErrInvalidArgument)
		}
		if config.TunnelPool == nil {
			return nil, fmt.Errorf("new tcp session: %w: nil tunnel pool", transport.ErrInvalidArgument)
		}
	default:
		return nil, fmt.Errorf("new tcp session: %w: unknown role=%s", transport.ErrInvalidArgument, role)
	}

	return transport.NewInMemorySession(
		normalizedMeta,
		transport.BindingInfo{Type: transport.BindingTypeTCPFramed},
		transport.SessionCapabilities{
			ControlChannel: config.ControlChannel,
			Producer:       config.TunnelProducer,
			Acceptor:       config.TunnelAcceptor,
			Pool:           config.TunnelPool,
		},
	), nil
}
