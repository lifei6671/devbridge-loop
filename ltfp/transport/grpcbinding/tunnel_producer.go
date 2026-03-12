package grpcbinding

import (
	"context"
	"fmt"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TunnelProducer 实现 Agent 侧主动打开 TunnelStream 的能力。
type TunnelProducer struct {
	binding *Transport
	client  transportgen.GRPCH2TransportServiceClient

	identityConfig TunnelIdentityConfig
	idGenerator    *tunnelIDGenerator
}

var _ transport.TunnelProducer = (*TunnelProducer)(nil)

// NewTunnelProducer 使用默认 transport 配置创建 producer。
func NewTunnelProducer(
	client transportgen.GRPCH2TransportServiceClient,
	identityConfig TunnelIdentityConfig,
) (*TunnelProducer, error) {
	return NewTunnelProducerWithTransport(nil, client, identityConfig)
}

// NewTunnelProducerWithTransport 使用指定 transport 配置创建 producer。
func NewTunnelProducerWithTransport(
	binding *Transport,
	client transportgen.GRPCH2TransportServiceClient,
	identityConfig TunnelIdentityConfig,
) (*TunnelProducer, error) {
	if client == nil {
		return nil, fmt.Errorf("new grpc tunnel producer: %w: nil client", transport.ErrInvalidArgument)
	}
	normalizedIdentityConfig := identityConfig.normalized()
	if binding == nil {
		binding = NewTransport()
	}
	return &TunnelProducer{
		binding:        binding,
		client:         client,
		identityConfig: normalizedIdentityConfig,
		idGenerator:    newTunnelIDGenerator(normalizedIdentityConfig.TunnelIDPrefix),
	}, nil
}

// OpenTunnel 主动打开一条 grpc TunnelStream 并适配为 transport.Tunnel。
func (producer *TunnelProducer) OpenTunnel(ctx context.Context) (transport.Tunnel, error) {
	if producer == nil {
		return nil, fmt.Errorf("open grpc tunnel: %w: nil producer", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stream, err := producer.binding.OpenTunnelStream(ctx, producer.client)
	if err != nil {
		return nil, fmt.Errorf("open grpc tunnel: %w", err)
	}
	tunnelID := producer.idGenerator.Next()
	tunnelMeta := buildTunnelMeta(producer.identityConfig, tunnelID, time.Now().UTC())
	tunnel, err := NewGRPCH2Tunnel(stream, tunnelMeta)
	if err != nil {
		_ = stream.Close(context.Background())
		return nil, fmt.Errorf("open grpc tunnel: %w", err)
	}
	return tunnel, nil
}
