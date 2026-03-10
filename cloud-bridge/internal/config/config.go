package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultBridgeHTTPAddr            = "0.0.0.0:38080"
	defaultBridgeMasqueTunnelUDPAddr = "127.0.0.1:39081"
	defaultBridgeMasquePSK           = "devloop-masque-default-psk"
	defaultBridgePublicHost          = "bridge.example.internal"
	defaultBridgePublicPort          = 443
	defaultBridgeFallbackBackflowURL = "http://127.0.0.1:39090"
	defaultBridgeIngressTimeoutSec   = 10
	defaultDiscoveryTimeoutMs        = 2000
	defaultDiscoveryEtcdKeyPrefix    = "/devloop/services"
	defaultBridgeConfigEnv           = "DEVLOOP_BRIDGE_CONFIG_FILE"
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
	DiscoveryBackends   []string
	DiscoveryTimeout    time.Duration
	DiscoveryLocalFile  string
	NacosDiscovery      NacosDiscoveryConfig
	EtcdDiscovery       EtcdDiscoveryConfig
	ConsulDiscovery     ConsulDiscoveryConfig
	BridgePublicHost    string
	BridgePublicPort    int
	FallbackBackflowURL string
	IngressTimeout      time.Duration
}

// NacosDiscoveryConfig 定义 Nacos 服务发现参数。
type NacosDiscoveryConfig struct {
	ServerAddr     string
	Namespace      string
	Group          string
	ServicePattern string
	Username       string
	Password       string
}

// EtcdDiscoveryConfig 定义 etcd 服务发现参数。
type EtcdDiscoveryConfig struct {
	Endpoints []string
	KeyPrefix string
}

// ConsulDiscoveryConfig 定义 Consul 服务发现参数。
type ConsulDiscoveryConfig struct {
	Addr           string
	Datacenter     string
	ServicePattern string
}

type bridgeConfigFile struct {
	HTTPAddr            string                    `yaml:"httpAddr"`
	TunnelSyncProtocol  string                    `yaml:"tunnelSyncProtocol"`
	TunnelSyncProtocols []string                  `yaml:"tunnelSyncProtocols"`
	MasqueAddr          string                    `yaml:"masqueAddr"`
	MasqueTunnelUDPAddr string                    `yaml:"masqueTunnelUdpAddr"`
	MasqueAuthMode      string                    `yaml:"masqueAuthMode"`
	MasquePSK           string                    `yaml:"masquePsk"`
	RouteExtractorOrder []string                  `yaml:"routeExtractorOrder"`
	BridgePublicHost    string                    `yaml:"bridgePublicHost"`
	BridgePublicPort    int                       `yaml:"bridgePublicPort"`
	FallbackBackflowURL string                    `yaml:"fallbackBackflowUrl"`
	IngressTimeoutSec   int                       `yaml:"ingressTimeoutSec"`
	Discovery           bridgeDiscoveryConfigFile `yaml:"discovery"`
	Routes              []bridgeRouteFile         `yaml:"routes"`
}

type bridgeDiscoveryConfigFile struct {
	Backends  []string               `yaml:"backends"`
	TimeoutMs int                    `yaml:"timeoutMs"`
	LocalFile string                 `yaml:"localFile"`
	Nacos     bridgeNacosConfigFile  `yaml:"nacos"`
	Etcd      bridgeEtcdConfigFile   `yaml:"etcd"`
	Consul    bridgeConsulConfigFile `yaml:"consul"`
	Local     bridgeLocalConfigFile  `yaml:"local"`
}

type bridgeNacosConfigFile struct {
	Addr           string `yaml:"addr"`
	Namespace      string `yaml:"namespace"`
	Group          string `yaml:"group"`
	ServicePattern string `yaml:"servicePattern"`
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
}

type bridgeEtcdConfigFile struct {
	Endpoints []string `yaml:"endpoints"`
	KeyPrefix string   `yaml:"keyPrefix"`
}

type bridgeConsulConfigFile struct {
	Addr           string `yaml:"addr"`
	Datacenter     string `yaml:"datacenter"`
	ServicePattern string `yaml:"servicePattern"`
}

type bridgeLocalConfigFile struct {
	File   string            `yaml:"file"`
	Routes []bridgeRouteFile `yaml:"routes"`
}

type bridgeRouteFile struct {
	Env         string `yaml:"env"`
	ServiceName string `yaml:"serviceName"`
	Protocol    string `yaml:"protocol"`
	Host        string `yaml:"host"`
	Address     string `yaml:"address"`
	Port        int    `yaml:"port"`
}

// LoadFromEnv 按 “默认值 -> YAML 配置文件 -> 环境变量覆盖” 的顺序加载配置。
func LoadFromEnv() Config {
	cfg := defaultConfig()

	configFilePath := strings.TrimSpace(getenv(defaultBridgeConfigEnv, ""))
	if configFilePath != "" {
		fileCfg, err := loadConfigFile(configFilePath)
		if err != nil {
			slog.Warn("load bridge config file failed, fallback to env/default",
				"file", configFilePath,
				"error", err,
			)
		} else {
			cfg = mergeConfigFile(cfg, fileCfg, configFilePath)
		}
	}

	httpAddr := getenv("DEVLOOP_BRIDGE_HTTP_ADDR", cfg.HTTPAddr)
	rawTunnelSyncProtocols := getenv("DEVLOOP_TUNNEL_SYNC_PROTOCOL", strings.Join(cfg.TunnelSyncProtocols, ","))
	tunnelSyncProtocols := normalizeTunnelSyncProtocols(rawTunnelSyncProtocols)

	order := splitCommaValues(getenv("DEVLOOP_ROUTE_EXTRACT_ORDER", strings.Join(cfg.RouteExtractorOrder, ",")))
	if len(order) == 0 {
		order = cfg.RouteExtractorOrder
	}

	discoveryBackends := normalizeDiscoveryBackends(getenv("DEVLOOP_BRIDGE_DISCOVERY_BACKENDS", strings.Join(cfg.DiscoveryBackends, ",")))
	discoveryTimeoutMs := getenvInt("DEVLOOP_BRIDGE_DISCOVERY_TIMEOUT_MS", maxInt(int(cfg.DiscoveryTimeout/time.Millisecond), defaultDiscoveryTimeoutMs))
	if discoveryTimeoutMs <= 0 {
		discoveryTimeoutMs = defaultDiscoveryTimeoutMs
	}
	discoveryTimeout := time.Duration(discoveryTimeoutMs) * time.Millisecond

	ingressTimeoutSec := getenvInt("DEVLOOP_BRIDGE_INGRESS_TIMEOUT_SEC", maxInt(int(cfg.IngressTimeout/time.Second), defaultBridgeIngressTimeoutSec))
	if ingressTimeoutSec <= 0 {
		ingressTimeoutSec = defaultBridgeIngressTimeoutSec
	}

	bridgePublicPort := getenvInt("DEVLOOP_BRIDGE_PUBLIC_PORT", cfg.BridgePublicPort)
	if bridgePublicPort <= 0 {
		bridgePublicPort = defaultBridgePublicPort
	}

	return Config{
		HTTPAddr:            httpAddr,
		TunnelSyncProtocol:  tunnelSyncProtocols[0],
		TunnelSyncProtocols: tunnelSyncProtocols,
		MasqueAddr:          getenv("DEVLOOP_BRIDGE_MASQUE_ADDR", cfg.MasqueAddr),
		MasqueTunnelUDPAddr: getenv("DEVLOOP_BRIDGE_MASQUE_TUNNEL_UDP_ADDR", cfg.MasqueTunnelUDPAddr),
		MasqueAuthMode:      normalizeMasqueAuthMode(getenv("DEVLOOP_TUNNEL_MASQUE_AUTH_MODE", cfg.MasqueAuthMode)),
		MasquePSK:           getenv("DEVLOOP_TUNNEL_MASQUE_PSK", cfg.MasquePSK),
		RouteExtractorOrder: order,
		DiscoveryBackends:   discoveryBackends,
		DiscoveryTimeout:    discoveryTimeout,
		DiscoveryLocalFile:  strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_LOCAL_FILE", cfg.DiscoveryLocalFile)),
		NacosDiscovery: NacosDiscoveryConfig{
			ServerAddr:     strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_ADDR", cfg.NacosDiscovery.ServerAddr)),
			Namespace:      strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_NAMESPACE", cfg.NacosDiscovery.Namespace)),
			Group:          strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_GROUP", cfg.NacosDiscovery.Group)),
			ServicePattern: strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_SERVICE_PATTERN", cfg.NacosDiscovery.ServicePattern)),
			Username:       strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_USERNAME", cfg.NacosDiscovery.Username)),
			Password:       strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_NACOS_PASSWORD", cfg.NacosDiscovery.Password)),
		},
		EtcdDiscovery: EtcdDiscoveryConfig{
			Endpoints: splitCommaValues(getenv("DEVLOOP_BRIDGE_DISCOVERY_ETCD_ENDPOINTS", strings.Join(cfg.EtcdDiscovery.Endpoints, ","))),
			KeyPrefix: strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_ETCD_KEY_PREFIX", cfg.EtcdDiscovery.KeyPrefix)),
		},
		ConsulDiscovery: ConsulDiscoveryConfig{
			Addr:           strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_CONSUL_ADDR", cfg.ConsulDiscovery.Addr)),
			Datacenter:     strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_CONSUL_DC", cfg.ConsulDiscovery.Datacenter)),
			ServicePattern: strings.TrimSpace(getenv("DEVLOOP_BRIDGE_DISCOVERY_CONSUL_SERVICE_PATTERN", cfg.ConsulDiscovery.ServicePattern)),
		},
		BridgePublicHost:    getenv("DEVLOOP_BRIDGE_PUBLIC_HOST", cfg.BridgePublicHost),
		BridgePublicPort:    bridgePublicPort,
		FallbackBackflowURL: getenv("DEVLOOP_BRIDGE_FALLBACK_BACKFLOW_URL", cfg.FallbackBackflowURL),
		IngressTimeout:      time.Duration(ingressTimeoutSec) * time.Second,
	}
}

func defaultConfig() Config {
	tunnelSyncProtocols := []string{"masque"}
	routeOrder := []string{"host", "header", "sni"}
	discoveryBackends := []string{"local", "nacos", "etcd", "consul"}
	cfg := Config{
		HTTPAddr:            defaultBridgeHTTPAddr,
		TunnelSyncProtocol:  tunnelSyncProtocols[0],
		TunnelSyncProtocols: tunnelSyncProtocols,
		MasqueTunnelUDPAddr: defaultBridgeMasqueTunnelUDPAddr,
		MasqueAuthMode:      "psk",
		MasquePSK:           defaultBridgeMasquePSK,
		RouteExtractorOrder: routeOrder,
		DiscoveryBackends:   discoveryBackends,
		DiscoveryTimeout:    defaultDiscoveryTimeoutMs * time.Millisecond,
		DiscoveryLocalFile:  "",
		NacosDiscovery: NacosDiscoveryConfig{
			ServerAddr:     "",
			Namespace:      "",
			Group:          "DEFAULT_GROUP",
			ServicePattern: "${service}",
			Username:       "",
			Password:       "",
		},
		EtcdDiscovery: EtcdDiscoveryConfig{
			Endpoints: []string{},
			KeyPrefix: defaultDiscoveryEtcdKeyPrefix,
		},
		ConsulDiscovery: ConsulDiscoveryConfig{
			Addr:           "",
			Datacenter:     "",
			ServicePattern: "${service}",
		},
		BridgePublicHost:    defaultBridgePublicHost,
		BridgePublicPort:    defaultBridgePublicPort,
		FallbackBackflowURL: defaultBridgeFallbackBackflowURL,
		IngressTimeout:      defaultBridgeIngressTimeoutSec * time.Second,
	}
	cfg.MasqueAddr = cfg.HTTPAddr
	return cfg
}

func loadConfigFile(path string) (bridgeConfigFile, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return bridgeConfigFile{}, fmt.Errorf("read file failed: %w", err)
	}
	var fileCfg bridgeConfigFile
	if err := yaml.Unmarshal(content, &fileCfg); err != nil {
		return bridgeConfigFile{}, fmt.Errorf("decode yaml failed: %w", err)
	}
	return fileCfg, nil
}

func mergeConfigFile(base Config, fileCfg bridgeConfigFile, filePath string) Config {
	cfg := base

	if value := strings.TrimSpace(fileCfg.HTTPAddr); value != "" {
		cfg.HTTPAddr = value
	}

	switch {
	case len(fileCfg.TunnelSyncProtocols) > 0:
		cfg.TunnelSyncProtocols = normalizeTunnelSyncProtocols(strings.Join(fileCfg.TunnelSyncProtocols, ","))
		cfg.TunnelSyncProtocol = cfg.TunnelSyncProtocols[0]
	case strings.TrimSpace(fileCfg.TunnelSyncProtocol) != "":
		cfg.TunnelSyncProtocols = normalizeTunnelSyncProtocols(fileCfg.TunnelSyncProtocol)
		cfg.TunnelSyncProtocol = cfg.TunnelSyncProtocols[0]
	}

	if value := strings.TrimSpace(fileCfg.MasqueAddr); value != "" {
		cfg.MasqueAddr = value
	}
	if value := strings.TrimSpace(fileCfg.MasqueTunnelUDPAddr); value != "" {
		cfg.MasqueTunnelUDPAddr = value
	}
	if value := strings.TrimSpace(fileCfg.MasqueAuthMode); value != "" {
		cfg.MasqueAuthMode = normalizeMasqueAuthMode(value)
	}
	if value := strings.TrimSpace(fileCfg.MasquePSK); value != "" {
		cfg.MasquePSK = value
	}

	if len(fileCfg.RouteExtractorOrder) > 0 {
		order := sanitizeStringList(fileCfg.RouteExtractorOrder)
		if len(order) > 0 {
			cfg.RouteExtractorOrder = order
		}
	}

	if value := strings.TrimSpace(fileCfg.BridgePublicHost); value != "" {
		cfg.BridgePublicHost = value
	}
	if fileCfg.BridgePublicPort > 0 {
		cfg.BridgePublicPort = fileCfg.BridgePublicPort
	}
	if value := strings.TrimSpace(fileCfg.FallbackBackflowURL); value != "" {
		cfg.FallbackBackflowURL = value
	}
	if fileCfg.IngressTimeoutSec > 0 {
		cfg.IngressTimeout = time.Duration(fileCfg.IngressTimeoutSec) * time.Second
	}

	if len(fileCfg.Discovery.Backends) > 0 {
		cfg.DiscoveryBackends = normalizeDiscoveryBackends(strings.Join(fileCfg.Discovery.Backends, ","))
	}
	if fileCfg.Discovery.TimeoutMs > 0 {
		cfg.DiscoveryTimeout = time.Duration(fileCfg.Discovery.TimeoutMs) * time.Millisecond
	}
	if value := strings.TrimSpace(fileCfg.Discovery.LocalFile); value != "" {
		cfg.DiscoveryLocalFile = resolveConfigRelativePath(filePath, value)
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Local.File); value != "" {
		cfg.DiscoveryLocalFile = resolveConfigRelativePath(filePath, value)
	}

	// 若 YAML 内直接内联 routes 且未显式指定 localFile，则默认使用当前配置文件作为本地发现文件。
	if cfg.DiscoveryLocalFile == "" && (len(fileCfg.Routes) > 0 || len(fileCfg.Discovery.Local.Routes) > 0) {
		cfg.DiscoveryLocalFile = strings.TrimSpace(filePath)
	}

	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.Addr); value != "" {
		cfg.NacosDiscovery.ServerAddr = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.Namespace); value != "" {
		cfg.NacosDiscovery.Namespace = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.Group); value != "" {
		cfg.NacosDiscovery.Group = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.ServicePattern); value != "" {
		cfg.NacosDiscovery.ServicePattern = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.Username); value != "" {
		cfg.NacosDiscovery.Username = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Nacos.Password); value != "" {
		cfg.NacosDiscovery.Password = value
	}

	if len(fileCfg.Discovery.Etcd.Endpoints) > 0 {
		cfg.EtcdDiscovery.Endpoints = sanitizeStringList(fileCfg.Discovery.Etcd.Endpoints)
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Etcd.KeyPrefix); value != "" {
		cfg.EtcdDiscovery.KeyPrefix = value
	}

	if value := strings.TrimSpace(fileCfg.Discovery.Consul.Addr); value != "" {
		cfg.ConsulDiscovery.Addr = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Consul.Datacenter); value != "" {
		cfg.ConsulDiscovery.Datacenter = value
	}
	if value := strings.TrimSpace(fileCfg.Discovery.Consul.ServicePattern); value != "" {
		cfg.ConsulDiscovery.ServicePattern = value
	}

	if cfg.MasqueAddr == "" {
		cfg.MasqueAddr = cfg.HTTPAddr
	}
	return cfg
}

func resolveConfigRelativePath(configFilePath string, rawPath string) string {
	trimmedPath := strings.TrimSpace(rawPath)
	if trimmedPath == "" {
		return ""
	}
	if filepath.IsAbs(trimmedPath) {
		return trimmedPath
	}
	configDir := filepath.Dir(strings.TrimSpace(configFilePath))
	if strings.TrimSpace(configDir) == "" || configDir == "." {
		return trimmedPath
	}
	return filepath.Join(configDir, trimmedPath)
}

func sanitizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
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

func normalizeDiscoveryBackends(value string) []string {
	parts := splitCommaValues(value)
	backends := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, 4)
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "local", "nacos", "etcd", "consul":
			key := strings.ToLower(strings.TrimSpace(part))
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			backends = append(backends, key)
		}
	}
	// 未配置或配置无效时，默认启用全部发现器，保证功能开箱即用。
	if len(backends) == 0 {
		return []string{"local", "nacos", "etcd", "consul"}
	}
	return backends
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}
