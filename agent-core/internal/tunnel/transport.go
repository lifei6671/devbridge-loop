package tunnel

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

const (
	tunnelSyncProtocolHTTP   = "http"
	tunnelSyncProtocolMasque = "masque"
)

// EventClient 抽象 tunnel 事件传输层，便于后续扩展更多协议实现。
type EventClient interface {
	SendEvent(ctx context.Context, message domain.TunnelMessage) (domain.TunnelReply, error)
	Close() error
}

// NewEventClient 按配置创建 tunnel 事件传输客户端。
func NewEventClient(cfg config.Config) (EventClient, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Tunnel.SyncProtocol)) {
	case tunnelSyncProtocolMasque:
		// MASQUE 模式下，组装 CONNECT-UDP 所需参数并创建客户端。
		options := MasqueClientOptions{
			ProxyURL:       resolveMasqueProxyURL(cfg.Tunnel.BridgeAddress, cfg.Tunnel.MasqueProxyURL),
			TargetAddr:     cfg.Tunnel.MasqueTargetAddr,
			AuthMode:       cfg.Tunnel.MasqueAuthMode,
			PSK:            cfg.Tunnel.MasquePSK,
			RequestTimeout: cfg.Tunnel.RequestTimeout,
		}
		return NewMasqueBridgeClient(options)
	default:
		return NewBridgeClient(cfg.Tunnel.BridgeAddress, cfg.Tunnel.RequestTimeout), nil
	}
}

func resolveMasqueProxyURL(bridgeAddress, configured string) string {
	// 优先使用显式配置，方便后续切到独立 MASQUE 网关地址。
	if normalized := strings.TrimSpace(configured); normalized != "" {
		return normalized
	}

	address := strings.TrimSpace(bridgeAddress)
	if address == "" {
		address = "http://127.0.0.1:38080"
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	// 从 bridgeAddress 推导 H3 的 URI 模板，保留 host:port 并强制 https scheme。
	parsed, err := url.Parse(address)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return "https://127.0.0.1:38080/masque/udp/{target_host}/{target_port}"
	}
	return fmt.Sprintf("https://%s/masque/udp/{target_host}/{target_port}", parsed.Host)
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 5 * time.Second
	}
	return timeout
}
