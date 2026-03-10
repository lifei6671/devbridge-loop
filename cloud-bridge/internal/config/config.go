package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 包含 cloud-bridge 运行配置。
type Config struct {
	HTTPAddr            string
	TunnelSyncProtocol  string
	TunnelSyncProtocols []string
	MasqueAddr          string
	MasqueTunnelUDPAddr string
	MasqueAuthMode      string
	MasquePSK           string
	RouteExtractorOrder []string
	BridgePublicHost    string
	BridgePublicPort    int
	FallbackBackflowURL string
	IngressTimeout      time.Duration
}

// LoadFromEnv 按环境变量加载配置，并提供默认值。
func LoadFromEnv() Config {
	httpAddr := getenv("DEVLOOP_BRIDGE_HTTP_ADDR", "0.0.0.0:38080")
	rawTunnelSyncProtocols := getenv("DEVLOOP_TUNNEL_SYNC_PROTOCOL", "")
	tunnelSyncProtocols := normalizeTunnelSyncProtocols(rawTunnelSyncProtocols)
	order := strings.Split(getenv("DEVLOOP_ROUTE_EXTRACT_ORDER", "host,header,sni"), ",")
	for i := range order {
		order[i] = strings.TrimSpace(order[i])
	}

	return Config{
		HTTPAddr:            httpAddr,
		TunnelSyncProtocol:  tunnelSyncProtocols[0],
		TunnelSyncProtocols: tunnelSyncProtocols,
		MasqueAddr:          getenv("DEVLOOP_BRIDGE_MASQUE_ADDR", httpAddr),
		MasqueTunnelUDPAddr: getenv("DEVLOOP_BRIDGE_MASQUE_TUNNEL_UDP_ADDR", "127.0.0.1:39081"),
		MasqueAuthMode:      normalizeMasqueAuthMode(getenv("DEVLOOP_TUNNEL_MASQUE_AUTH_MODE", "psk")),
		MasquePSK:           getenv("DEVLOOP_TUNNEL_MASQUE_PSK", "devloop-masque-default-psk"),
		RouteExtractorOrder: order,
		BridgePublicHost:    getenv("DEVLOOP_BRIDGE_PUBLIC_HOST", "bridge.example.internal"),
		BridgePublicPort:    getenvInt("DEVLOOP_BRIDGE_PUBLIC_PORT", 443),
		FallbackBackflowURL: getenv("DEVLOOP_BRIDGE_FALLBACK_BACKFLOW_URL", "http://127.0.0.1:39090"),
		IngressTimeout:      time.Duration(getenvInt("DEVLOOP_BRIDGE_INGRESS_TIMEOUT_SEC", 10)) * time.Second,
	}
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := strings.TrimSpace(getenv(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func normalizeTunnelSyncProtocols(value string) []string {
	parts := strings.Split(value, ",")
	protocols := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, 2)

	// bridge 支持同时启用多个 tunnel 协议，按用户配置顺序去重保留。
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "http", "masque":
			key := strings.ToLower(strings.TrimSpace(part))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			protocols = append(protocols, key)
		}
	}

	// 若未配置或配置无效，默认仅启用 MASQUE。
	if len(protocols) == 0 {
		return []string{"masque"}
	}
	return protocols
}

func normalizeMasqueAuthMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ecdh":
		return "ecdh"
	default:
		return "psk"
	}
}
