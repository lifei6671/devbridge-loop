package app

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
)

const (
	// controlPlaneTLSHandshakeTimeout 控制服务端 TLS 握手最长等待时间。
	controlPlaneTLSHandshakeTimeout = 5 * time.Second
)

var (
	// errControlPlaneTLSRejected 用于标识被 tls_mode 主动拒绝的连接。
	errControlPlaneTLSRejected = errors.New("control plane tls connection rejected by tls_mode")
	// errControlPlaneTLSRejectPlaintextOnRequired 标识 required 模式拒绝明文连接。
	errControlPlaneTLSRejectPlaintextOnRequired = errors.New("control plane tls required rejects plaintext")
	// errControlPlaneTLSRejectTLSOnPlaintext 标识 plaintext 模式拒绝 TLS 连接。
	errControlPlaneTLSRejectTLSOnPlaintext = errors.New("control plane plaintext rejects tls")
)

// controlPlaneTLSMode 定义 Bridge 控制面的 TLS 接入模式。
type controlPlaneTLSMode string

const (
	controlPlaneTLSModeRequired  controlPlaneTLSMode = "required"
	controlPlaneTLSModeOptional  controlPlaneTLSMode = "optional"
	controlPlaneTLSModePlaintext controlPlaneTLSMode = "plaintext"
)

// normalizeControlPlaneTLSMode 归一化并校验 TLS 模式。
func normalizeControlPlaneTLSMode(rawMode string) (controlPlaneTLSMode, error) {
	switch strings.ToLower(strings.TrimSpace(rawMode)) {
	case "", string(controlPlaneTLSModePlaintext):
		return controlPlaneTLSModePlaintext, nil
	case string(controlPlaneTLSModeRequired):
		return controlPlaneTLSModeRequired, nil
	case string(controlPlaneTLSModeOptional):
		return controlPlaneTLSModeOptional, nil
	default:
		return "", fmt.Errorf("unsupported control_plane.tls_mode=%s", rawMode)
	}
}

// loadControlPlaneServerTLSConfig 加载 Bridge 服务端 TLS 配置。
func loadControlPlaneServerTLSConfig(certFile string, keyFile string) (*tls.Config, error) {
	normalizedCertFile := strings.TrimSpace(certFile)
	normalizedKeyFile := strings.TrimSpace(keyFile)
	if normalizedCertFile == "" && normalizedKeyFile == "" {
		return nil, nil
	}
	certificate, err := tls.LoadX509KeyPair(normalizedCertFile, normalizedKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load control plane tls certificate: %w", err)
	}
	return &tls.Config{
		// 控制面固定收敛到 TLS 1.3，避免不同版本混用。
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		// 显式关闭 session ticket/PSK 恢复路径，避免未来演进时偏离“禁用 0-RTT”的约束。
		SessionTicketsDisabled: true,
		Certificates: []tls.Certificate{
			certificate,
		},
		// gRPC over TLS 需要 h2；tcp_framed 即使未协商 ALPN 也可正常工作。
		NextProtos: []string{"h2"},
	}, nil
}

// acceptControlPlaneConnWithTLS 根据 tls_mode 判定入站连接是否需要 TLS 握手。
func acceptControlPlaneConnWithTLS(
	rawConn net.Conn,
	tlsMode controlPlaneTLSMode,
	serverTLSConfig *tls.Config,
	metrics *obs.Metrics,
) (net.Conn, bool, error) {
	if rawConn == nil {
		return nil, false, errors.New("accept control plane tls conn: nil conn")
	}
	prefixedConn, isTLSClientHello, err := detectTLSClientHello(rawConn, tcpConnectionClassifierReadTimeout)
	if err != nil {
		return nil, false, err
	}
	switch tlsMode {
	case controlPlaneTLSModeRequired:
		if !isTLSClientHello {
			if metrics != nil {
				metrics.IncBridgeTLSRejectPlaintextOnRequiredTotal()
			}
			return nil, false, fmt.Errorf("%w: %w: plaintext connection is not allowed", errControlPlaneTLSRejected, errControlPlaneTLSRejectPlaintextOnRequired)
		}
	case controlPlaneTLSModePlaintext:
		if isTLSClientHello {
			if metrics != nil {
				metrics.IncBridgeTLSRejectTLSOnPlaintextTotal()
			}
			return nil, false, fmt.Errorf("%w: %w: tls connection is not allowed", errControlPlaneTLSRejected, errControlPlaneTLSRejectTLSOnPlaintext)
		}
	case controlPlaneTLSModeOptional:
		// optional 模式同时接受 TLS 和明文，无需额外分支。
	default:
		return nil, false, fmt.Errorf("accept control plane tls conn: unsupported tls mode=%s", tlsMode)
	}
	if !isTLSClientHello {
		return prefixedConn, false, nil
	}
	if serverTLSConfig == nil {
		return nil, false, errors.New("accept control plane tls conn: tls config is nil")
	}
	if err := prefixedConn.SetDeadline(time.Now().UTC().Add(controlPlaneTLSHandshakeTimeout)); err != nil {
		return nil, false, fmt.Errorf("accept control plane tls conn: set handshake deadline: %w", err)
	}
	tlsConn := tls.Server(prefixedConn, serverTLSConfig.Clone())
	if err := tlsConn.Handshake(); err != nil {
		_ = prefixedConn.SetDeadline(time.Time{})
		return nil, false, fmt.Errorf("accept control plane tls conn: tls handshake failed: %w", err)
	}
	if err := prefixedConn.SetDeadline(time.Time{}); err != nil {
		_ = tlsConn.Close()
		return nil, false, fmt.Errorf("accept control plane tls conn: clear handshake deadline: %w", err)
	}
	return tlsConn, true, nil
}

// detectTLSClientHello 通过首 3 字节判定连接是否以 TLS record 开头，并保留已读前缀。
func detectTLSClientHello(rawConn net.Conn, timeout time.Duration) (net.Conn, bool, error) {
	if rawConn == nil {
		return nil, false, errors.New("detect tls client hello: nil conn")
	}
	peekBuffer := make([]byte, 3)
	readSize := 0
	for readSize < len(peekBuffer) {
		if timeout > 0 {
			if err := rawConn.SetReadDeadline(time.Now().UTC().Add(timeout)); err != nil {
				return nil, false, fmt.Errorf("detect tls client hello: set read deadline: %w", err)
			}
		}
		chunkSize, readErr := rawConn.Read(peekBuffer[readSize:])
		_ = rawConn.SetReadDeadline(time.Time{})
		if chunkSize > 0 {
			readSize += chunkSize
		}
		if readErr == nil {
			continue
		}
		if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
			if readSize == 0 {
				return rawConn, false, nil
			}
			return nil, false, fmt.Errorf("detect tls client hello: incomplete prefix before timeout")
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return &prefixedNetConn{
				Conn:   rawConn,
				prefix: append([]byte(nil), peekBuffer[:readSize]...),
			}, false, nil
		}
		return nil, false, fmt.Errorf("detect tls client hello: read prefix: %w", readErr)
	}
	prefixedConn := &prefixedNetConn{
		Conn:   rawConn,
		prefix: append([]byte(nil), peekBuffer...),
	}
	return prefixedConn, looksLikeTLSClientHello(peekBuffer), nil
}

// looksLikeTLSClientHello 判断首包是否满足 TLS record header 形态。
func looksLikeTLSClientHello(prefix []byte) bool {
	if len(prefix) < 3 {
		return false
	}
	if prefix[0] != 0x16 {
		return false
	}
	if prefix[1] != 0x03 {
		return false
	}
	return prefix[2] >= 0x01 && prefix[2] <= 0x04
}

// controlPlaneTLSAwareListener 在 grpc server Accept 之前完成 tls_mode 判定与握手。
type controlPlaneTLSAwareListener struct {
	net.Listener
	tlsMode       controlPlaneTLSMode
	serverTLSConf *tls.Config
	metrics       *obs.Metrics
}

// newControlPlaneTLSAwareListener 创建带 tls_mode 判定能力的 listener 包装器。
func newControlPlaneTLSAwareListener(
	listener net.Listener,
	tlsMode controlPlaneTLSMode,
	serverTLSConfig *tls.Config,
	metrics *obs.Metrics,
) net.Listener {
	if listener == nil {
		return nil
	}
	return &controlPlaneTLSAwareListener{
		Listener:      listener,
		tlsMode:       tlsMode,
		serverTLSConf: serverTLSConfig,
		metrics:       metrics,
	}
}

// Accept 接收下一个允许进入 gRPC 处理链路的连接。
func (listener *controlPlaneTLSAwareListener) Accept() (net.Conn, error) {
	if listener == nil || listener.Listener == nil {
		return nil, net.ErrClosed
	}
	for {
		rawConn, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		acceptedConn, tlsEnabled, wrapErr := acceptControlPlaneConnWithTLS(rawConn, listener.tlsMode, listener.serverTLSConf, listener.metrics)
		if wrapErr != nil {
			slog.Warn(
				"reject grpc control connection by tls mode",
				"tls_mode", string(listener.tlsMode),
				"peer_addr", remoteAddrString(rawConn),
				"error", wrapErr.Error(),
			)
			_ = rawConn.Close()
			continue
		}
		slog.Debug(
			"accept grpc control connection",
			"tls_mode", string(listener.tlsMode),
			"tls_enabled", tlsEnabled,
			"peer_addr", remoteAddrString(rawConn),
		)
		return acceptedConn, nil
	}
}

// remoteAddrString 返回连接对端地址字符串，供日志使用。
func remoteAddrString(rawConn net.Conn) string {
	if rawConn == nil || rawConn.RemoteAddr() == nil {
		return ""
	}
	return strings.TrimSpace(rawConn.RemoteAddr().String())
}
