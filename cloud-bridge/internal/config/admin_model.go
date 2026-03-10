package config

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AdminConfigModel 是 bridge 管理页使用的结构化配置模型。
type AdminConfigModel struct {
	HTTPAddr            string                  `json:"httpAddr"`
	TunnelSyncProtocols []string                `json:"tunnelSyncProtocols"`
	RouteExtractorOrder []string                `json:"routeExtractorOrder"`
	Masque              AdminMasqueConfig       `json:"masque"`
	AdminAuth           AdminAuthModel          `json:"adminAuth"`
	BridgePublic        AdminBridgePublicConfig `json:"bridgePublic"`
	Ingress             AdminIngressConfig      `json:"ingress"`
	Discovery           AdminDiscoveryConfig    `json:"discovery"`
	Routes              []AdminRouteConfig      `json:"routes"`
}

// AdminMasqueConfig 是 MASQUE 相关配置。
type AdminMasqueConfig struct {
	Addr          string `json:"addr"`
	TunnelUDPAddr string `json:"tunnelUdpAddr"`
	AuthMode      string `json:"authMode"`
	PSK           string `json:"psk"`
}

// AdminAuthModel 是 bridge 管理控制台认证配置。
type AdminAuthModel struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password"`
	Realm    string `json:"realm"`
}

// AdminBridgePublicConfig 是 bridge 对外暴露配置。
type AdminBridgePublicConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// AdminIngressConfig 是 ingress 相关配置。
type AdminIngressConfig struct {
	FallbackBackflowURL string `json:"fallbackBackflowUrl"`
	TimeoutSec          int    `json:"timeoutSec"`
}

// AdminDiscoveryConfig 是服务发现聚合配置。
type AdminDiscoveryConfig struct {
	Backends  []string          `json:"backends"`
	TimeoutMs int               `json:"timeoutMs"`
	LocalFile string            `json:"localFile"`
	Nacos     AdminNacosConfig  `json:"nacos"`
	Etcd      AdminEtcdConfig   `json:"etcd"`
	Consul    AdminConsulConfig `json:"consul"`
}

// AdminNacosConfig 是 Nacos 配置。
type AdminNacosConfig struct {
	Addr           string `json:"addr"`
	Namespace      string `json:"namespace"`
	Group          string `json:"group"`
	ServicePattern string `json:"servicePattern"`
	Username       string `json:"username"`
	Password       string `json:"password"`
}

// AdminEtcdConfig 是 Etcd 配置。
type AdminEtcdConfig struct {
	Endpoints []string `json:"endpoints"`
	KeyPrefix string   `json:"keyPrefix"`
}

// AdminConsulConfig 是 Consul 配置。
type AdminConsulConfig struct {
	Addr           string `json:"addr"`
	Datacenter     string `json:"datacenter"`
	ServicePattern string `json:"servicePattern"`
}

// AdminRouteConfig 是本地静态路由配置项。
type AdminRouteConfig struct {
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
}

// DefaultAdminConfigModel 返回管理页默认配置模型。
func DefaultAdminConfigModel() AdminConfigModel {
	return runtimeConfigToAdminModel(defaultConfig())
}

// ParseConfigYAMLToAdminModel 将 YAML 文本解析为管理页模型，并补齐默认值。
func ParseConfigYAMLToAdminModel(path string, content []byte) (AdminConfigModel, error) {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return DefaultAdminConfigModel(), nil
	}

	var fileCfg bridgeConfigFile
	if err := yaml.Unmarshal(content, &fileCfg); err != nil {
		return AdminConfigModel{}, fmt.Errorf("decode yaml failed: %w", err)
	}

	merged := mergeConfigFile(defaultConfig(), fileCfg, strings.TrimSpace(path))
	model := runtimeConfigToAdminModel(merged)

	// 本地 routes 优先读取顶层 routes，若为空则回退 discovery.local.routes。
	sourceRoutes := fileCfg.Routes
	if len(sourceRoutes) == 0 {
		sourceRoutes = fileCfg.Discovery.Local.Routes
	}
	model.Routes = make([]AdminRouteConfig, 0, len(sourceRoutes))
	for _, route := range sourceRoutes {
		host := strings.TrimSpace(route.Host)
		if host == "" {
			host = strings.TrimSpace(route.Address)
		}
		model.Routes = append(model.Routes, AdminRouteConfig{
			Env:         strings.TrimSpace(route.Env),
			ServiceName: strings.TrimSpace(route.ServiceName),
			Protocol:    strings.TrimSpace(route.Protocol),
			Host:        host,
			Port:        route.Port,
		})
	}
	return normalizeAdminModel(model), nil
}

// MarshalAdminModelYAML 将管理页模型渲染为 bridge YAML。
func MarshalAdminModelYAML(model AdminConfigModel) ([]byte, error) {
	normalized := normalizeAdminModel(model)
	fileCfg := bridgeConfigFile{
		HTTPAddr:            normalized.HTTPAddr,
		TunnelSyncProtocols: append([]string{}, normalized.TunnelSyncProtocols...),
		MasqueAddr:          normalized.Masque.Addr,
		MasqueTunnelUDPAddr: normalized.Masque.TunnelUDPAddr,
		MasqueAuthMode:      normalized.Masque.AuthMode,
		MasquePSK:           normalized.Masque.PSK,
		AdminAuth: bridgeAdminAuthConfigFile{
			Enabled:  normalized.AdminAuth.Enabled,
			Username: strings.TrimSpace(normalized.AdminAuth.Username),
			Password: normalized.AdminAuth.Password,
			Realm:    strings.TrimSpace(normalized.AdminAuth.Realm),
		},
		RouteExtractorOrder: append([]string{}, normalized.RouteExtractorOrder...),
		BridgePublicHost:    normalized.BridgePublic.Host,
		BridgePublicPort:    normalized.BridgePublic.Port,
		FallbackBackflowURL: normalized.Ingress.FallbackBackflowURL,
		IngressTimeoutSec:   normalized.Ingress.TimeoutSec,
		Discovery: bridgeDiscoveryConfigFile{
			Backends:  append([]string{}, normalized.Discovery.Backends...),
			TimeoutMs: normalized.Discovery.TimeoutMs,
			LocalFile: strings.TrimSpace(normalized.Discovery.LocalFile),
			Nacos: bridgeNacosConfigFile{
				Addr:           strings.TrimSpace(normalized.Discovery.Nacos.Addr),
				Namespace:      strings.TrimSpace(normalized.Discovery.Nacos.Namespace),
				Group:          strings.TrimSpace(normalized.Discovery.Nacos.Group),
				ServicePattern: strings.TrimSpace(normalized.Discovery.Nacos.ServicePattern),
				Username:       strings.TrimSpace(normalized.Discovery.Nacos.Username),
				Password:       strings.TrimSpace(normalized.Discovery.Nacos.Password),
			},
			Etcd: bridgeEtcdConfigFile{
				Endpoints: append([]string{}, normalized.Discovery.Etcd.Endpoints...),
				KeyPrefix: strings.TrimSpace(normalized.Discovery.Etcd.KeyPrefix),
			},
			Consul: bridgeConsulConfigFile{
				Addr:           strings.TrimSpace(normalized.Discovery.Consul.Addr),
				Datacenter:     strings.TrimSpace(normalized.Discovery.Consul.Datacenter),
				ServicePattern: strings.TrimSpace(normalized.Discovery.Consul.ServicePattern),
			},
		},
	}

	if len(normalized.Routes) > 0 {
		fileCfg.Routes = make([]bridgeRouteFile, 0, len(normalized.Routes))
		for _, route := range normalized.Routes {
			fileCfg.Routes = append(fileCfg.Routes, bridgeRouteFile{
				Env:         strings.TrimSpace(route.Env),
				ServiceName: strings.TrimSpace(route.ServiceName),
				Protocol:    strings.TrimSpace(route.Protocol),
				Host:        strings.TrimSpace(route.Host),
				Port:        route.Port,
			})
		}
	}
	return yaml.Marshal(fileCfg)
}

func runtimeConfigToAdminModel(cfg Config) AdminConfigModel {
	return AdminConfigModel{
		HTTPAddr:            strings.TrimSpace(cfg.HTTPAddr),
		TunnelSyncProtocols: append([]string{}, cfg.TunnelSyncProtocols...),
		RouteExtractorOrder: append([]string{}, cfg.RouteExtractorOrder...),
		Masque: AdminMasqueConfig{
			Addr:          strings.TrimSpace(cfg.MasqueAddr),
			TunnelUDPAddr: strings.TrimSpace(cfg.MasqueTunnelUDPAddr),
			AuthMode:      strings.TrimSpace(cfg.MasqueAuthMode),
			PSK:           strings.TrimSpace(cfg.MasquePSK),
		},
		AdminAuth: AdminAuthModel{
			Enabled:  cfg.AdminAuth.Enabled,
			Username: strings.TrimSpace(cfg.AdminAuth.Username),
			Password: cfg.AdminAuth.Password,
			Realm:    strings.TrimSpace(cfg.AdminAuth.Realm),
		},
		BridgePublic: AdminBridgePublicConfig{
			Host: strings.TrimSpace(cfg.BridgePublicHost),
			Port: cfg.BridgePublicPort,
		},
		Ingress: AdminIngressConfig{
			FallbackBackflowURL: strings.TrimSpace(cfg.FallbackBackflowURL),
			TimeoutSec:          int(cfg.IngressTimeout / time.Second),
		},
		Discovery: AdminDiscoveryConfig{
			Backends:  append([]string{}, cfg.DiscoveryBackends...),
			TimeoutMs: int(cfg.DiscoveryTimeout / time.Millisecond),
			LocalFile: strings.TrimSpace(cfg.DiscoveryLocalFile),
			Nacos: AdminNacosConfig{
				Addr:           strings.TrimSpace(cfg.NacosDiscovery.ServerAddr),
				Namespace:      strings.TrimSpace(cfg.NacosDiscovery.Namespace),
				Group:          strings.TrimSpace(cfg.NacosDiscovery.Group),
				ServicePattern: strings.TrimSpace(cfg.NacosDiscovery.ServicePattern),
				Username:       strings.TrimSpace(cfg.NacosDiscovery.Username),
				Password:       strings.TrimSpace(cfg.NacosDiscovery.Password),
			},
			Etcd: AdminEtcdConfig{
				Endpoints: append([]string{}, cfg.EtcdDiscovery.Endpoints...),
				KeyPrefix: strings.TrimSpace(cfg.EtcdDiscovery.KeyPrefix),
			},
			Consul: AdminConsulConfig{
				Addr:           strings.TrimSpace(cfg.ConsulDiscovery.Addr),
				Datacenter:     strings.TrimSpace(cfg.ConsulDiscovery.Datacenter),
				ServicePattern: strings.TrimSpace(cfg.ConsulDiscovery.ServicePattern),
			},
		},
		Routes: []AdminRouteConfig{},
	}
}

func normalizeAdminModel(model AdminConfigModel) AdminConfigModel {
	base := runtimeConfigToAdminModel(defaultConfig())

	if value := strings.TrimSpace(model.HTTPAddr); value != "" {
		base.HTTPAddr = value
	}

	if len(model.TunnelSyncProtocols) > 0 {
		base.TunnelSyncProtocols = normalizeTunnelSyncProtocols(strings.Join(model.TunnelSyncProtocols, ","))
	}

	order := sanitizeStringList(model.RouteExtractorOrder)
	if len(order) > 0 {
		base.RouteExtractorOrder = order
	}

	if value := strings.TrimSpace(model.Masque.Addr); value != "" {
		base.Masque.Addr = value
	}
	if value := strings.TrimSpace(model.Masque.TunnelUDPAddr); value != "" {
		base.Masque.TunnelUDPAddr = value
	}
	if value := strings.TrimSpace(model.Masque.AuthMode); value != "" {
		base.Masque.AuthMode = normalizeMasqueAuthMode(value)
	}
	if value := strings.TrimSpace(model.Masque.PSK); value != "" {
		base.Masque.PSK = value
	}
	base.AdminAuth.Enabled = model.AdminAuth.Enabled
	if value := strings.TrimSpace(model.AdminAuth.Username); value != "" {
		base.AdminAuth.Username = value
	}
	if strings.TrimSpace(model.AdminAuth.Password) != "" {
		base.AdminAuth.Password = model.AdminAuth.Password
	}
	if value := strings.TrimSpace(model.AdminAuth.Realm); value != "" {
		base.AdminAuth.Realm = value
	}

	if value := strings.TrimSpace(model.BridgePublic.Host); value != "" {
		base.BridgePublic.Host = value
	}
	if model.BridgePublic.Port > 0 {
		base.BridgePublic.Port = model.BridgePublic.Port
	}

	if value := strings.TrimSpace(model.Ingress.FallbackBackflowURL); value != "" {
		base.Ingress.FallbackBackflowURL = value
	}
	if model.Ingress.TimeoutSec > 0 {
		base.Ingress.TimeoutSec = model.Ingress.TimeoutSec
	}

	if len(model.Discovery.Backends) > 0 {
		base.Discovery.Backends = normalizeDiscoveryBackends(strings.Join(model.Discovery.Backends, ","))
	}
	if model.Discovery.TimeoutMs > 0 {
		base.Discovery.TimeoutMs = model.Discovery.TimeoutMs
	}
	if value := strings.TrimSpace(model.Discovery.LocalFile); value != "" {
		base.Discovery.LocalFile = value
	}

	if value := strings.TrimSpace(model.Discovery.Nacos.Addr); value != "" {
		base.Discovery.Nacos.Addr = value
	}
	if value := strings.TrimSpace(model.Discovery.Nacos.Namespace); value != "" {
		base.Discovery.Nacos.Namespace = value
	}
	if value := strings.TrimSpace(model.Discovery.Nacos.Group); value != "" {
		base.Discovery.Nacos.Group = value
	}
	if value := strings.TrimSpace(model.Discovery.Nacos.ServicePattern); value != "" {
		base.Discovery.Nacos.ServicePattern = value
	}
	if value := strings.TrimSpace(model.Discovery.Nacos.Username); value != "" {
		base.Discovery.Nacos.Username = value
	}
	if value := strings.TrimSpace(model.Discovery.Nacos.Password); value != "" {
		base.Discovery.Nacos.Password = value
	}

	if len(model.Discovery.Etcd.Endpoints) > 0 {
		base.Discovery.Etcd.Endpoints = sanitizeStringList(model.Discovery.Etcd.Endpoints)
	}
	if value := strings.TrimSpace(model.Discovery.Etcd.KeyPrefix); value != "" {
		base.Discovery.Etcd.KeyPrefix = value
	}

	if value := strings.TrimSpace(model.Discovery.Consul.Addr); value != "" {
		base.Discovery.Consul.Addr = value
	}
	if value := strings.TrimSpace(model.Discovery.Consul.Datacenter); value != "" {
		base.Discovery.Consul.Datacenter = value
	}
	if value := strings.TrimSpace(model.Discovery.Consul.ServicePattern); value != "" {
		base.Discovery.Consul.ServicePattern = value
	}

	base.Routes = make([]AdminRouteConfig, 0, len(model.Routes))
	for _, route := range model.Routes {
		normalizedRoute := AdminRouteConfig{
			Env:         strings.TrimSpace(route.Env),
			ServiceName: strings.TrimSpace(route.ServiceName),
			Protocol:    normalizeRouteProtocol(route.Protocol),
			Host:        strings.TrimSpace(route.Host),
			Port:        route.Port,
		}
		if normalizedRoute.Env == "" || normalizedRoute.ServiceName == "" || normalizedRoute.Host == "" || normalizedRoute.Port <= 0 {
			continue
		}
		base.Routes = append(base.Routes, normalizedRoute)
	}

	if len(base.TunnelSyncProtocols) == 0 {
		base.TunnelSyncProtocols = []string{"masque"}
	}
	if len(base.Discovery.Backends) == 0 {
		base.Discovery.Backends = []string{"local", "nacos", "etcd", "consul"}
	}
	if base.Discovery.TimeoutMs <= 0 {
		base.Discovery.TimeoutMs = defaultDiscoveryTimeoutMs
	}
	if base.Ingress.TimeoutSec <= 0 {
		base.Ingress.TimeoutSec = defaultBridgeIngressTimeoutSec
	}
	if base.BridgePublic.Port <= 0 {
		base.BridgePublic.Port = defaultBridgePublicPort
	}
	if strings.TrimSpace(base.Masque.AuthMode) == "" {
		base.Masque.AuthMode = "psk"
	}
	normalizedAdminAuth := normalizeAdminAuthConfig(AdminAuthConfig{
		Enabled:  base.AdminAuth.Enabled,
		Username: base.AdminAuth.Username,
		Password: base.AdminAuth.Password,
		Realm:    base.AdminAuth.Realm,
	})
	base.AdminAuth = AdminAuthModel{
		Enabled:  normalizedAdminAuth.Enabled,
		Username: normalizedAdminAuth.Username,
		Password: normalizedAdminAuth.Password,
		Realm:    normalizedAdminAuth.Realm,
	}
	return base
}

func normalizeRouteProtocol(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "grpc":
		return "grpc"
	default:
		return "http"
	}
}
