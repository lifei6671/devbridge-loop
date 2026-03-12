package tcpbinding

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const (
	// defaultDialTimeout 是 tcp_framed 默认拨号超时。
	defaultDialTimeout = 5 * time.Second

	// defaultKeepAliveInterval 是默认 TCP keepalive 周期。
	defaultKeepAliveInterval = 30 * time.Second

	// defaultAcceptPollInterval 是 listener Accept 轮询间隔。
	defaultAcceptPollInterval = 200 * time.Millisecond

	// defaultMaxControlFramePayloadSize 是默认控制面单帧上限。
	defaultMaxControlFramePayloadSize = 4 * 1024 * 1024

	// defaultMaxTunnelFramePayloadSize 是默认数据面单帧上限。
	defaultMaxTunnelFramePayloadSize = 4 * 1024 * 1024
)

// TransportConfig 描述 tcp_framed binding 的基础参数。
type TransportConfig struct {
	DialTimeout                time.Duration
	KeepAliveInterval          time.Duration
	AcceptPollInterval         time.Duration
	MaxControlFramePayloadSize int
	MaxTunnelFramePayloadSize  int
	NoDelay                    bool
	// NoDelaySet 标记是否显式设置了 NoDelay；未设置时默认启用 true。
	NoDelaySet bool
}

// NormalizeAndValidate 归一化并校验配置。
func (config TransportConfig) NormalizeAndValidate() (TransportConfig, error) {
	normalizedConfig := config
	if normalizedConfig.DialTimeout <= 0 {
		// 未配置拨号超时时使用保守默认值。
		normalizedConfig.DialTimeout = defaultDialTimeout
	}
	if normalizedConfig.KeepAliveInterval <= 0 {
		// 默认启用 TCP keepalive，降低空闲连接被中间设备回收的概率。
		normalizedConfig.KeepAliveInterval = defaultKeepAliveInterval
	}
	if normalizedConfig.AcceptPollInterval <= 0 {
		// 轮询间隔用于让 Accept 能响应 ctx 取消或 close。
		normalizedConfig.AcceptPollInterval = defaultAcceptPollInterval
	}
	if normalizedConfig.MaxControlFramePayloadSize <= 0 {
		// 控制面默认允许 4MiB 单帧，超大消息应走 transport 分块。
		normalizedConfig.MaxControlFramePayloadSize = defaultMaxControlFramePayloadSize
	}
	if normalizedConfig.MaxTunnelFramePayloadSize <= 0 {
		// 数据面默认允许 4MiB 单帧，真正大流量应由 runtime 继续拆帧。
		normalizedConfig.MaxTunnelFramePayloadSize = defaultMaxTunnelFramePayloadSize
	}
	if normalizedConfig.MaxControlFramePayloadSize > int(^uint32(0)) {
		return TransportConfig{}, fmt.Errorf(
			"normalize tcp transport config: %w: max_control_frame_payload_size=%d",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxControlFramePayloadSize,
		)
	}
	if normalizedConfig.MaxTunnelFramePayloadSize > int(^uint32(0)) {
		return TransportConfig{}, fmt.Errorf(
			"normalize tcp transport config: %w: max_tunnel_frame_payload_size=%d",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxTunnelFramePayloadSize,
		)
	}
	if !normalizedConfig.NoDelaySet {
		// 未显式配置时默认关闭 Nagle，降低控制面小消息延迟。
		normalizedConfig.NoDelay = true
		normalizedConfig.NoDelaySet = true
	}
	return normalizedConfig, nil
}

// DefaultTransportConfig 返回默认配置。
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		DialTimeout:                defaultDialTimeout,
		KeepAliveInterval:          defaultKeepAliveInterval,
		AcceptPollInterval:         defaultAcceptPollInterval,
		MaxControlFramePayloadSize: defaultMaxControlFramePayloadSize,
		MaxTunnelFramePayloadSize:  defaultMaxTunnelFramePayloadSize,
		NoDelay:                    true,
		NoDelaySet:                 true,
	}
}

// Transport 封装 tcp_framed 的拨号、accept 和连接包装逻辑。
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

// OpenControlChannel 将现有 TCP 连接包装为 transport.ControlChannel。
func (binding *Transport) OpenControlChannel(conn net.Conn) (transport.ControlChannel, error) {
	if conn == nil {
		return nil, fmt.Errorf("open tcp control channel: %w: nil conn", transport.ErrInvalidArgument)
	}
	if err := binding.prepareConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open tcp control channel: %w", err)
	}
	controlChannel, err := NewTCPControlChannel(conn, binding.Config())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open tcp control channel: %w", err)
	}
	return controlChannel, nil
}

// DialControlChannel 拨号并建立一条长期控制连接。
func (binding *Transport) DialControlChannel(ctx context.Context, address string) (transport.ControlChannel, error) {
	conn, err := binding.dialConn(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("dial tcp control channel: %w", err)
	}
	controlChannel, err := binding.OpenControlChannel(conn)
	if err != nil {
		return nil, fmt.Errorf("dial tcp control channel: %w", err)
	}
	return controlChannel, nil
}

// AcceptControlChannel 从 listener 接受一条控制连接并包装为 ControlChannel。
func (binding *Transport) AcceptControlChannel(ctx context.Context, listener net.Listener) (transport.ControlChannel, error) {
	conn, err := binding.acceptConn(ctx, listener)
	if err != nil {
		return nil, fmt.Errorf("accept tcp control channel: %w", err)
	}
	controlChannel, err := binding.OpenControlChannel(conn)
	if err != nil {
		return nil, fmt.Errorf("accept tcp control channel: %w", err)
	}
	return controlChannel, nil
}

// OpenTunnel 将现有 TCP 连接包装为 transport.Tunnel。
func (binding *Transport) OpenTunnel(conn net.Conn, meta transport.TunnelMeta) (*TCPTunnel, error) {
	if conn == nil {
		return nil, fmt.Errorf("open tcp tunnel: %w: nil conn", transport.ErrInvalidArgument)
	}
	if err := binding.prepareConn(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open tcp tunnel: %w", err)
	}
	tunnel, err := NewTCPTunnel(conn, meta, binding.Config())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open tcp tunnel: %w", err)
	}
	return tunnel, nil
}

// DialTunnel 拨号并建立一条数据面 tunnel。
func (binding *Transport) DialTunnel(ctx context.Context, address string, meta transport.TunnelMeta) (*TCPTunnel, error) {
	conn, err := binding.dialConn(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("dial tcp tunnel: %w", err)
	}
	tunnel, err := binding.OpenTunnel(conn, meta)
	if err != nil {
		return nil, fmt.Errorf("dial tcp tunnel: %w", err)
	}
	return tunnel, nil
}

// AcceptTunnel 从 listener 接受一条 TCP 连接并包装为 tunnel。
func (binding *Transport) AcceptTunnel(ctx context.Context, listener net.Listener, meta transport.TunnelMeta) (*TCPTunnel, error) {
	conn, err := binding.acceptConn(ctx, listener)
	if err != nil {
		return nil, fmt.Errorf("accept tcp tunnel: %w", err)
	}
	tunnel, err := binding.OpenTunnel(conn, meta)
	if err != nil {
		return nil, fmt.Errorf("accept tcp tunnel: %w", err)
	}
	return tunnel, nil
}

// dialConn 使用配置好的 net.Dialer 建立 TCP 连接。
func (binding *Transport) dialConn(ctx context.Context, address string) (net.Conn, error) {
	if ctx == nil {
		// 调用方未传 ctx 时回退到后台上下文。
		ctx = context.Background()
	}
	if address == "" {
		return nil, fmt.Errorf("dial tcp conn: %w: empty address", transport.ErrInvalidArgument)
	}
	config := binding.Config()
	dialer := &net.Dialer{
		Timeout:   config.DialTimeout,
		KeepAlive: config.KeepAliveInterval,
	}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// acceptConn 在支持 deadline 的 listener 上轮询 Accept，以响应 ctx 取消。
func (binding *Transport) acceptConn(ctx context.Context, listener net.Listener) (net.Conn, error) {
	if ctx == nil {
		// accept 逻辑统一依赖非 nil ctx，避免后续 select 判空。
		ctx = context.Background()
	}
	if listener == nil {
		return nil, fmt.Errorf("accept tcp conn: %w: nil listener", transport.ErrInvalidArgument)
	}
	deadlineCapableListener, supportsDeadline := listener.(interface{ SetDeadline(time.Time) error })
	if !supportsDeadline {
		// 非 TCP listener 无法优雅响应 ctx，直接执行一次阻塞 Accept。
		return listener.Accept()
	}

	config := binding.Config()
	for {
		acceptDeadline := time.Now().Add(config.AcceptPollInterval)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(acceptDeadline) {
			// 上层 deadline 更早时优先使用上层约束。
			acceptDeadline = ctxDeadline
		}
		if err := deadlineCapableListener.SetDeadline(acceptDeadline); err != nil {
			return nil, fmt.Errorf("accept tcp conn: %w", err)
		}
		conn, err := listener.Accept()
		if err == nil {
			_ = deadlineCapableListener.SetDeadline(time.Time{})
			return conn, nil
		}
		if ctx.Err() != nil {
			_ = deadlineCapableListener.SetDeadline(time.Time{})
			return nil, ctx.Err()
		}
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			// 超时只用于轮询 ctx 和 close 状态，不视为真正 accept 失败。
			continue
		}
		return nil, err
	}
}

// prepareConn 对真实 TCP 连接应用 keepalive 和 no-delay 配置。
func (binding *Transport) prepareConn(conn net.Conn) error {
	if conn == nil {
		return fmt.Errorf("prepare tcp conn: %w: nil conn", transport.ErrInvalidArgument)
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		// 非 TCPConn 场景保留默认行为，便于 net.Pipe 等测试替身工作。
		return nil
	}
	config := binding.Config()
	if err := tcpConn.SetNoDelay(config.NoDelay); err != nil {
		return fmt.Errorf("prepare tcp conn set no delay: %w", err)
	}
	if err := tcpConn.SetKeepAlive(true); err != nil {
		return fmt.Errorf("prepare tcp conn set keepalive: %w", err)
	}
	if err := tcpConn.SetKeepAlivePeriod(config.KeepAliveInterval); err != nil {
		return fmt.Errorf("prepare tcp conn set keepalive period: %w", err)
	}
	return nil
}
