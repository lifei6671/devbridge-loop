package grpcbinding

import (
	"context"
	"fmt"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"google.golang.org/grpc"
)

const (
	// defaultMaxCallMessageBytes 为 grpc_h2 首版保守消息大小上限。
	defaultMaxCallMessageBytes = 4 * 1024 * 1024
)

// TransportConfig 描述 grpc_h2 binding 的基础参数。
type TransportConfig struct {
	MaxCallRecvMsgSize int
	MaxCallSendMsgSize int
}

// NormalizeAndValidate 归一化并校验配置。
func (config TransportConfig) NormalizeAndValidate() (TransportConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MaxCallRecvMsgSize <= 0 {
		normalizedConfig.MaxCallRecvMsgSize = defaultMaxCallMessageBytes
	}
	if normalizedConfig.MaxCallSendMsgSize <= 0 {
		normalizedConfig.MaxCallSendMsgSize = defaultMaxCallMessageBytes
	}
	return normalizedConfig, nil
}

// DefaultTransportConfig 返回默认配置。
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		MaxCallRecvMsgSize: defaultMaxCallMessageBytes,
		MaxCallSendMsgSize: defaultMaxCallMessageBytes,
	}
}

// Transport 封装 grpc_h2 transport stream 的打开逻辑。
type Transport struct {
	config TransportConfig
}

// NewTransport 使用默认配置创建实例。
func NewTransport() *Transport {
	return &Transport{
		config: DefaultTransportConfig(),
	}
}

// NewTransportWithConfig 使用指定配置创建实例。
func NewTransportWithConfig(config TransportConfig) (*Transport, error) {
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &Transport{config: normalizedConfig}, nil
}

// Config 返回配置快照。
func (binding *Transport) Config() TransportConfig {
	if binding == nil {
		return DefaultTransportConfig()
	}
	return binding.config
}

// OpenControlChannel 打开 grpc bidi 控制流并适配为 transport.ControlChannel。
func (binding *Transport) OpenControlChannel(
	ctx context.Context,
	client transportgen.GRPCH2TransportServiceClient,
) (transport.ControlChannel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return nil, fmt.Errorf("open grpc control channel: %w: nil client", transport.ErrInvalidArgument)
	}
	options := binding.callOptions()
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := client.ControlChannel(streamCtx, options...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open grpc control channel: %w", err)
	}
	controlChannel, err := newGRPCH2ControlChannelWithCancel(stream, cancel)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open grpc control channel: %w", err)
	}
	return controlChannel, nil
}

// OpenTunnelStream 打开 grpc bidi 数据流。
func (binding *Transport) OpenTunnelStream(
	ctx context.Context,
	client transportgen.GRPCH2TransportServiceClient,
) (*GRPCH2TunnelStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		return nil, fmt.Errorf("open grpc tunnel stream: %w: nil client", transport.ErrInvalidArgument)
	}
	options := binding.callOptions()
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := client.TunnelStream(streamCtx, options...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open grpc tunnel stream: %w", err)
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(stream, cancel)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open grpc tunnel stream: %w", err)
	}
	return tunnelStream, nil
}

func (binding *Transport) callOptions() []grpc.CallOption {
	config := binding.Config()
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(config.MaxCallRecvMsgSize),
		grpc.MaxCallSendMsgSize(config.MaxCallSendMsgSize),
	}
}
