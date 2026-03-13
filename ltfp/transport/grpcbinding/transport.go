package grpcbinding

import (
	"context"
	"fmt"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	// defaultMaxCallMessageBytes 为 grpc_h2 首版保守消息大小上限。
	defaultMaxCallMessageBytes = 4 * 1024 * 1024
	// defaultClientKeepAliveTime 为客户端 keepalive ping 周期。
	defaultClientKeepAliveTime = 30 * time.Second
	// defaultClientKeepAliveTimeout 为客户端 keepalive 响应超时。
	defaultClientKeepAliveTimeout = 10 * time.Second
	// defaultServerKeepAliveTime 为服务端 keepalive ping 周期。
	defaultServerKeepAliveTime = 30 * time.Second
	// defaultServerKeepAliveTimeout 为服务端 keepalive 响应超时。
	defaultServerKeepAliveTimeout = 10 * time.Second
	// defaultServerMinPingInterval 为服务端允许客户端 ping 的最小间隔。
	defaultServerMinPingInterval = 15 * time.Second
)

// TransportConfig 描述 grpc_h2 binding 的基础参数。
type TransportConfig struct {
	MaxCallRecvMsgSize int
	MaxCallSendMsgSize int

	// ClientKeepAliveTime 控制客户端侧探活周期。
	ClientKeepAliveTime time.Duration
	// ClientKeepAliveTimeout 控制客户端等待 keepalive ack 的超时。
	ClientKeepAliveTimeout time.Duration
	// ClientPermitWithoutStream 控制无活跃流时是否允许 keepalive。
	ClientPermitWithoutStream bool

	// ServerKeepAliveTime 控制服务端探活周期。
	ServerKeepAliveTime time.Duration
	// ServerKeepAliveTimeout 控制服务端等待 keepalive ack 的超时。
	ServerKeepAliveTimeout time.Duration
	// ServerMinPingInterval 控制服务端允许客户端 ping 的最小间隔。
	ServerMinPingInterval time.Duration
	// ServerPermitWithoutStream 控制服务端是否允许无活跃流的客户端 ping。
	ServerPermitWithoutStream bool

	// DisableReadPayloadFastPath 关闭 TunnelStream 读路径的零拷贝快路径。
	DisableReadPayloadFastPath bool
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
	if normalizedConfig.ClientKeepAliveTime < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize grpc transport config: %w: client_keepalive_time=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.ClientKeepAliveTime,
		)
	}
	if normalizedConfig.ClientKeepAliveTimeout < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize grpc transport config: %w: client_keepalive_timeout=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.ClientKeepAliveTimeout,
		)
	}
	if normalizedConfig.ServerKeepAliveTime < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize grpc transport config: %w: server_keepalive_time=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.ServerKeepAliveTime,
		)
	}
	if normalizedConfig.ServerKeepAliveTimeout < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize grpc transport config: %w: server_keepalive_timeout=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.ServerKeepAliveTimeout,
		)
	}
	if normalizedConfig.ServerMinPingInterval < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize grpc transport config: %w: server_min_ping_interval=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.ServerMinPingInterval,
		)
	}
	if normalizedConfig.ClientKeepAliveTime == 0 {
		normalizedConfig.ClientKeepAliveTime = defaultClientKeepAliveTime
	}
	if normalizedConfig.ClientKeepAliveTimeout == 0 {
		normalizedConfig.ClientKeepAliveTimeout = defaultClientKeepAliveTimeout
	}
	if normalizedConfig.ServerKeepAliveTime == 0 {
		normalizedConfig.ServerKeepAliveTime = defaultServerKeepAliveTime
	}
	if normalizedConfig.ServerKeepAliveTimeout == 0 {
		normalizedConfig.ServerKeepAliveTimeout = defaultServerKeepAliveTimeout
	}
	if normalizedConfig.ServerMinPingInterval == 0 {
		normalizedConfig.ServerMinPingInterval = defaultServerMinPingInterval
	}
	return normalizedConfig, nil
}

// DefaultTransportConfig 返回默认配置。
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		MaxCallRecvMsgSize:        defaultMaxCallMessageBytes,
		MaxCallSendMsgSize:        defaultMaxCallMessageBytes,
		ClientKeepAliveTime:       defaultClientKeepAliveTime,
		ClientKeepAliveTimeout:    defaultClientKeepAliveTimeout,
		ClientPermitWithoutStream: true,
		ServerKeepAliveTime:       defaultServerKeepAliveTime,
		ServerKeepAliveTimeout:    defaultServerKeepAliveTimeout,
		ServerMinPingInterval:     defaultServerMinPingInterval,
		ServerPermitWithoutStream: true,
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
	tunnelStream, err := newGRPCH2TunnelStreamWithOptions(
		stream,
		cancel,
		tunnelStreamAdapterOptions{
			enableReadPayloadFastPath: !binding.Config().DisableReadPayloadFastPath,
		},
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open grpc tunnel stream: %w", err)
	}
	return tunnelStream, nil
}

// DialOptions 返回创建 gRPC ClientConn 时可复用的默认选项。
func (binding *Transport) DialOptions() []grpc.DialOption {
	config := binding.Config()
	return []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(config.MaxCallRecvMsgSize),
			grpc.MaxCallSendMsgSize(config.MaxCallSendMsgSize),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                config.ClientKeepAliveTime,
			Timeout:             config.ClientKeepAliveTimeout,
			PermitWithoutStream: config.ClientPermitWithoutStream,
		}),
	}
}

// ServerOptions 返回创建 gRPC Server 时可复用的默认选项。
func (binding *Transport) ServerOptions() []grpc.ServerOption {
	config := binding.Config()
	return []grpc.ServerOption{
		grpc.MaxRecvMsgSize(config.MaxCallRecvMsgSize),
		grpc.MaxSendMsgSize(config.MaxCallSendMsgSize),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    config.ServerKeepAliveTime,
			Timeout: config.ServerKeepAliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             config.ServerMinPingInterval,
			PermitWithoutStream: config.ServerPermitWithoutStream,
		}),
	}
}

func (binding *Transport) callOptions() []grpc.CallOption {
	config := binding.Config()
	return []grpc.CallOption{
		grpc.MaxCallRecvMsgSize(config.MaxCallRecvMsgSize),
		grpc.MaxCallSendMsgSize(config.MaxCallSendMsgSize),
	}
}
