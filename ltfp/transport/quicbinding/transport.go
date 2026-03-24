package quicbinding

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	quic "github.com/quic-go/quic-go"
)

const (
	// defaultHandshakeIdleTimeout 是 QUIC 握手阶段默认空闲超时。
	defaultHandshakeIdleTimeout = 10 * time.Second
	// defaultMaxIdleTimeout 是 QUIC 连接默认空闲超时。
	defaultMaxIdleTimeout = 30 * time.Second
	// defaultKeepAlivePeriod 是默认保活周期，用于降低中间设备回收空闲 UDP 映射的概率。
	defaultKeepAlivePeriod = 15 * time.Second
	// defaultStreamOpenTimeout 是默认 stream 打开超时。
	defaultStreamOpenTimeout = 5 * time.Second
	// defaultMaxIncomingStreams 是默认最大并发入站 stream 数。
	defaultMaxIncomingStreams = int64(256)
	// defaultMaxControlFramePayloadSize 是控制面单帧默认上限。
	defaultMaxControlFramePayloadSize = 4 * 1024 * 1024
	// defaultMaxTunnelFramePayloadSize 是数据面单帧默认上限。
	defaultMaxTunnelFramePayloadSize = 4 * 1024 * 1024
	// defaultALPN 是 quic_native 首版默认协商标识。
	defaultALPN = "devbridge-ltfp-quic/v1"
)

// TransportConfig 描述 quic_native binding 的基础参数。
type TransportConfig struct {
	HandshakeIdleTimeout       time.Duration
	MaxIdleTimeout             time.Duration
	KeepAlivePeriod            time.Duration
	StreamOpenTimeout          time.Duration
	MaxIncomingStreams         int64
	MaxControlFramePayloadSize int
	MaxTunnelFramePayloadSize  int
}

// NormalizeAndValidate 归一化并校验配置。
func (config TransportConfig) NormalizeAndValidate() (TransportConfig, error) {
	normalizedConfig := config
	if normalizedConfig.HandshakeIdleTimeout < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: handshake_idle_timeout=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.HandshakeIdleTimeout,
		)
	}
	if normalizedConfig.MaxIdleTimeout < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: max_idle_timeout=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxIdleTimeout,
		)
	}
	if normalizedConfig.KeepAlivePeriod < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: keepalive_period=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.KeepAlivePeriod,
		)
	}
	if normalizedConfig.StreamOpenTimeout < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: stream_open_timeout=%s",
			transport.ErrInvalidArgument,
			normalizedConfig.StreamOpenTimeout,
		)
	}
	if normalizedConfig.HandshakeIdleTimeout == 0 {
		normalizedConfig.HandshakeIdleTimeout = defaultHandshakeIdleTimeout
	}
	if normalizedConfig.MaxIdleTimeout == 0 {
		normalizedConfig.MaxIdleTimeout = defaultMaxIdleTimeout
	}
	if normalizedConfig.KeepAlivePeriod == 0 {
		normalizedConfig.KeepAlivePeriod = defaultKeepAlivePeriod
	}
	if normalizedConfig.StreamOpenTimeout == 0 {
		normalizedConfig.StreamOpenTimeout = defaultStreamOpenTimeout
	}
	if normalizedConfig.MaxIncomingStreams < 0 {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: max_incoming_streams=%d",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxIncomingStreams,
		)
	}
	if normalizedConfig.MaxIncomingStreams == 0 {
		normalizedConfig.MaxIncomingStreams = defaultMaxIncomingStreams
	}
	if normalizedConfig.MaxControlFramePayloadSize <= 0 {
		normalizedConfig.MaxControlFramePayloadSize = defaultMaxControlFramePayloadSize
	}
	if normalizedConfig.MaxTunnelFramePayloadSize <= 0 {
		normalizedConfig.MaxTunnelFramePayloadSize = defaultMaxTunnelFramePayloadSize
	}
	if normalizedConfig.MaxControlFramePayloadSize > int(^uint32(0)) {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: max_control_frame_payload_size=%d",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxControlFramePayloadSize,
		)
	}
	if normalizedConfig.MaxTunnelFramePayloadSize > int(^uint32(0)) {
		return TransportConfig{}, fmt.Errorf(
			"normalize quic transport config: %w: max_tunnel_frame_payload_size=%d",
			transport.ErrInvalidArgument,
			normalizedConfig.MaxTunnelFramePayloadSize,
		)
	}
	return normalizedConfig, nil
}

// DefaultTransportConfig 返回默认配置。
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		HandshakeIdleTimeout:       defaultHandshakeIdleTimeout,
		MaxIdleTimeout:             defaultMaxIdleTimeout,
		KeepAlivePeriod:            defaultKeepAlivePeriod,
		StreamOpenTimeout:          defaultStreamOpenTimeout,
		MaxIncomingStreams:         defaultMaxIncomingStreams,
		MaxControlFramePayloadSize: defaultMaxControlFramePayloadSize,
		MaxTunnelFramePayloadSize:  defaultMaxTunnelFramePayloadSize,
	}
}

// Transport 封装 quic_native 的配置快照和连接创建逻辑。
type Transport struct {
	config TransportConfig
}

// NewTransport 使用默认配置创建实例。
func NewTransport() *Transport {
	return &Transport{config: DefaultTransportConfig()}
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

// QUICConfig 返回可传给 quic-go 的配置副本。
func (binding *Transport) QUICConfig() *quic.Config {
	config := binding.Config()
	return &quic.Config{
		HandshakeIdleTimeout: config.HandshakeIdleTimeout,
		MaxIdleTimeout:       config.MaxIdleTimeout,
		KeepAlivePeriod:      config.KeepAlivePeriod,
		MaxIncomingStreams:   config.MaxIncomingStreams,
		// 首版只使用双向 stream，主动关掉 uni stream，减少协议面分叉。
		MaxIncomingUniStreams: -1,
	}
}

// ListenAddr 在指定 UDP 地址上监听 QUIC 连接。
func (binding *Transport) ListenAddr(address string, tlsConfig *tls.Config) (*Listener, error) {
	normalizedAddress := strings.TrimSpace(address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("listen quic addr: %w: empty address", transport.ErrInvalidArgument)
	}
	normalizedTLSConfig := normalizeTLSConfig(tlsConfig)
	listener, err := quic.ListenAddr(normalizedAddress, normalizedTLSConfig, binding.QUICConfig())
	if err != nil {
		return nil, fmt.Errorf("listen quic addr: %w", err)
	}
	return &Listener{
		binding:  binding,
		listener: listener,
	}, nil
}

// Dial 在指定地址上建立一条 QUIC 连接。
func (binding *Transport) Dial(ctx context.Context, address string, tlsConfig *tls.Config) (*Conn, error) {
	normalizedAddress := strings.TrimSpace(address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("dial quic conn: %w: empty address", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	connContext := ctx
	if _, hasDeadline := connContext.Deadline(); !hasDeadline && binding.Config().StreamOpenTimeout > 0 {
		timedContext, cancel := context.WithTimeout(connContext, binding.Config().StreamOpenTimeout)
		defer cancel()
		connContext = timedContext
	}
	quicConn, err := quic.DialAddr(connContext, normalizedAddress, normalizeTLSConfig(tlsConfig), binding.QUICConfig())
	if err != nil {
		return nil, fmt.Errorf("dial quic conn: %w", err)
	}
	return newClientConn(binding, quicConn), nil
}

func normalizeTLSConfig(tlsConfig *tls.Config) *tls.Config {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	clonedConfig := tlsConfig.Clone()
	if clonedConfig.MinVersion == 0 || clonedConfig.MinVersion < tls.VersionTLS13 {
		clonedConfig.MinVersion = tls.VersionTLS13
	}
	if len(clonedConfig.NextProtos) == 0 {
		clonedConfig.NextProtos = []string{defaultALPN}
	}
	return clonedConfig
}
