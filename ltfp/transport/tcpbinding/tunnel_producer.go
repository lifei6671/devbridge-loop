package tcpbinding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TunnelProducer 实现 Agent 侧主动建立 TCP tunnel 的能力。
type TunnelProducer struct {
	binding *Transport
	address string

	identityConfig TunnelIdentityConfig
	idGenerator    *tunnelIDGenerator
}

var _ transport.TunnelProducer = (*TunnelProducer)(nil)

// NewTunnelProducer 使用默认 transport 配置创建 producer。
func NewTunnelProducer(address string, identityConfig TunnelIdentityConfig) (*TunnelProducer, error) {
	return NewTunnelProducerWithTransport(nil, address, identityConfig)
}

// NewTunnelProducerWithTransport 使用指定 transport 配置创建 producer。
func NewTunnelProducerWithTransport(
	binding *Transport,
	address string,
	identityConfig TunnelIdentityConfig,
) (*TunnelProducer, error) {
	normalizedAddress := strings.TrimSpace(address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("new tcp tunnel producer: %w: empty address", transport.ErrInvalidArgument)
	}
	normalizedIdentityConfig := identityConfig.normalized()
	if binding == nil {
		binding = NewTransport()
	}
	return &TunnelProducer{
		binding:        binding,
		address:        normalizedAddress,
		identityConfig: normalizedIdentityConfig,
		idGenerator:    newTunnelIDGenerator(normalizedIdentityConfig.TunnelIDPrefix),
	}, nil
}

// OpenTunnel 主动拨号并创建一条 TCP tunnel。
func (producer *TunnelProducer) OpenTunnel(ctx context.Context) (transport.Tunnel, error) {
	if producer == nil {
		return nil, fmt.Errorf("open tcp tunnel: %w: nil producer", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		// producer 允许调用方传 nil ctx，内部统一回退到背景上下文。
		ctx = context.Background()
	}
	tunnelID := producer.idGenerator.Next()
	tunnelMeta := buildTunnelMeta(producer.identityConfig, tunnelID, time.Now().UTC())
	tunnel, err := producer.binding.DialTunnel(ctx, producer.address, tunnelMeta)
	if err != nil {
		return nil, fmt.Errorf("open tcp tunnel: %w", err)
	}
	return tunnel, nil
}
