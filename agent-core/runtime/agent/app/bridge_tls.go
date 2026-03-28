package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc/credentials"
)

// buildBridgeClientTLSConfig 构造 Agent 访问 Bridge 时使用的 TLS 配置。
func buildBridgeClientTLSConfig(bridgeTLSConfig BridgeTLSConfig, bridgeAddr string) (*tls.Config, error) {
	if !bridgeTLSConfig.Enabled {
		return nil, nil
	}
	rootCAFile := strings.TrimSpace(bridgeTLSConfig.RootCAFile)
	if rootCAFile == "" {
		return nil, fmt.Errorf("build bridge client tls config: empty root ca file")
	}
	rootCAPEM, err := os.ReadFile(rootCAFile)
	if err != nil {
		return nil, fmt.Errorf("build bridge client tls config: read root ca file: %w", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(rootCAPEM) {
		return nil, fmt.Errorf(
			"build bridge client tls config: append root ca pem failed: root_ca_file=%s; expected a PEM certificate (for Bridge managed_ca use ca_cert_file/root-ca.crt, not ca_key_file/root-ca.key)",
			rootCAFile,
		)
	}
	serverName, err := resolveBridgeTLSServerName(bridgeTLSConfig, bridgeAddr)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		// 控制面固定要求 TLS 1.3，避免协商回落到更低版本。
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		// 显式关闭 session ticket/PSK 恢复路径，保持“不启用 0-RTT”的安全基线。
		SessionTicketsDisabled: true,
		// 客户端不缓存会话，确保不会尝试 TLS 1.3 session resumption。
		ClientSessionCache: nil,
		RootCAs:            rootCAs,
		ServerName:         serverName,
		// gRPC over TLS 需要 h2；tcp_framed 即使未协商 ALPN 也可正常传输。
		NextProtos: []string{"h2"},
	}, nil
}

// resolveBridgeTLSServerName 解析 TLS 校验使用的服务端名称。
func resolveBridgeTLSServerName(bridgeTLSConfig BridgeTLSConfig, bridgeAddr string) (string, error) {
	if configuredServerName := strings.TrimSpace(bridgeTLSConfig.ServerName); configuredServerName != "" {
		return configuredServerName, nil
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(bridgeAddr))
	if err != nil {
		return "", fmt.Errorf("build bridge client tls config: resolve server name: %w", err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("build bridge client tls config: empty server name")
	}
	return host, nil
}

// buildBridgeGRPCTransportCredentials 构造 gRPC Dial 使用的 transport credentials。
func buildBridgeGRPCTransportCredentials(bridgeTLSConfig BridgeTLSConfig, bridgeAddr string) (credentials.TransportCredentials, error) {
	tlsConfig, err := buildBridgeClientTLSConfig(bridgeTLSConfig, bridgeAddr)
	if err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return nil, nil
	}
	return credentials.NewTLS(tlsConfig), nil
}

// buildBridgeQUICClientTLSConfig 构造 quic_native 拨号使用的 TLS 配置。
func buildBridgeQUICClientTLSConfig(bridgeTLSConfig BridgeTLSConfig, bridgeAddr string) (*tls.Config, error) {
	tlsConfig, err := buildBridgeClientTLSConfig(bridgeTLSConfig, bridgeAddr)
	if err != nil {
		return nil, err
	}
	if tlsConfig == nil {
		return nil, fmt.Errorf("build bridge quic tls config: bridge tls is disabled")
	}
	// QUIC 需要由 quicbinding 注入自身 ALPN，不能沿用 gRPC 的 h2。
	tlsConfig.NextProtos = nil
	return tlsConfig, nil
}

// dialBridgeTCPConn 按当前配置拨号 Bridge TCP 连接，并在需要时完成 TLS 握手。
func dialBridgeTCPConn(
	ctx context.Context,
	address string,
	dialTimeout time.Duration,
	bridgeTLSConfig BridgeTLSConfig,
) (net.Conn, error) {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	normalizedAddress := strings.TrimSpace(address)
	if normalizedAddress == "" {
		return nil, fmt.Errorf("dial bridge tcp conn: empty address")
	}
	effectiveDialTimeout := dialTimeout
	if effectiveDialTimeout <= 0 {
		effectiveDialTimeout = 5 * time.Second
	}
	netDialer := &net.Dialer{
		Timeout:   effectiveDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	if !bridgeTLSConfig.Enabled {
		return netDialer.DialContext(normalizedContext, "tcp", normalizedAddress)
	}
	tlsConfig, err := buildBridgeClientTLSConfig(bridgeTLSConfig, normalizedAddress)
	if err != nil {
		return nil, err
	}
	tlsDialer := &tls.Dialer{
		NetDialer: netDialer,
		Config:    tlsConfig,
	}
	return tlsDialer.DialContext(normalizedContext, "tcp", normalizedAddress)
}
