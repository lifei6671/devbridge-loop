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
		return nil, fmt.Errorf("build bridge client tls config: append root ca pem failed")
	}
	serverName, err := resolveBridgeTLSServerName(bridgeTLSConfig, bridgeAddr)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		// 控制面固定要求 TLS 1.3；Go 标准库未实现 0-RTT，因此 Early Data 默认不可用。
		MinVersion: tls.VersionTLS13,
		RootCAs:    rootCAs,
		ServerName: serverName,
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
