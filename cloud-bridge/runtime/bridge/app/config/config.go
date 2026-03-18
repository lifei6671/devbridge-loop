package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
)

// controlPlaneTLSMode 表示控制面 TLS 接入模式。
type controlPlaneTLSMode string

const (
	// controlPlaneTLSModeRequired 表示只允许 TLS 连接。
	controlPlaneTLSModeRequired controlPlaneTLSMode = "required"
	// controlPlaneTLSModeOptional 表示同时允许 TLS/明文连接。
	controlPlaneTLSModeOptional controlPlaneTLSMode = "optional"
	// controlPlaneTLSModePlaintext 表示只允许明文连接。
	controlPlaneTLSModePlaintext controlPlaneTLSMode = "plaintext"
)

// controlPlaneTLSCertSource 表示控制面证书来源模式。
type controlPlaneTLSCertSource string

const (
	// controlPlaneTLSCertSourceExternal 表示使用外部提供的 cert/key。
	controlPlaneTLSCertSourceExternal controlPlaneTLSCertSource = "external"
	// controlPlaneTLSCertSourceManagedCA 表示使用自建 CA 动态签发。
	controlPlaneTLSCertSourceManagedCA controlPlaneTLSCertSource = "managed_ca"
)

// normalizeControlPlaneTLSMode 归一化并校验 TLS 模式文本。
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

// normalizeControlPlaneTLSCertSource 归一化并校验证书来源模式文本。
func normalizeControlPlaneTLSCertSource(rawSource string) (controlPlaneTLSCertSource, error) {
	switch strings.ToLower(strings.TrimSpace(rawSource)) {
	case "", string(controlPlaneTLSCertSourceExternal):
		return controlPlaneTLSCertSourceExternal, nil
	case string(controlPlaneTLSCertSourceManagedCA):
		return controlPlaneTLSCertSourceManagedCA, nil
	default:
		return "", fmt.Errorf("unsupported control_plane.tls_cert_source=%s", rawSource)
	}
}

// Config defines top-level runtime settings for the bridge skeleton.
type Config struct {
	Ingress       IngressConfig       `yaml:"ingress"`
	Admin         AdminConfig         `yaml:"admin"`
	Observability ObservabilityConfig `yaml:"observability"`
	ControlPlane  ControlPlaneConfig  `yaml:"control_plane"`
	TunnelReuse   TunnelReuseConfig   `yaml:"tunnel_reuse"`
	// RuntimeConfigFilePath 记录当前配置来源文件路径（仅运行时使用，不参与 YAML 编解码）。
	RuntimeConfigFilePath string `yaml:"-"`
}

type IngressConfig struct {
	HTTPAddr     string `yaml:"http_addr"`
	GRPCAddr     string `yaml:"grpc_addr"`
	HTTPSAddr    string `yaml:"https_addr"`
	TLSSNIAddr   string `yaml:"tls_sni_addr"`
	TCPPortRange string `yaml:"tcp_port_range"`
}

type AdminConfig struct {
	// Enabled 控制管理面总开关；关闭时不启动管理端口与相关路由。
	Enabled bool `yaml:"enabled"`
	// ListenAddr 为管理面监听地址，仅在 Enabled=true 时生效。
	ListenAddr string `yaml:"listen_addr"`
	// AllowSharedListener 控制是否允许管理面与其他监听地址复用端口。
	AllowSharedListener bool `yaml:"allow_shared_listener"`
	// BasePath 为管理 UI 挂载前缀，默认 /admin。
	BasePath string `yaml:"base_path"`
	// UIEnabled 控制管理 UI 路由是否启用（仍受 Enabled 总开关约束）。
	UIEnabled bool `yaml:"ui_enabled"`
	// AuthMode 定义管理 API 鉴权模式，支持 bearer/cookie。
	AuthMode string `yaml:"auth_mode"`
	// AuthTokens 定义静态 Bearer Token 与角色映射。
	AuthTokens []AdminAuthTokenConfig `yaml:"auth_tokens"`
	// CookieTokenName 定义 cookie 鉴权模式读取 token 的 cookie 名。
	CookieTokenName string `yaml:"cookie_token_name"`
	// CSRFCookieName 定义 cookie 鉴权模式 CSRF 双提交 cookie 名。
	CSRFCookieName string `yaml:"csrf_cookie_name"`
	// CSRFHeaderName 定义 cookie 鉴权模式 CSRF Header 名。
	CSRFHeaderName string `yaml:"csrf_header_name"`
	// AllowedOrigins 定义 cookie 鉴权模式允许的管理端来源 Origin 列表。
	AllowedOrigins []string `yaml:"allowed_origins"`
}

// AdminAuthTokenConfig 定义管理后台静态 Token 配置。
type AdminAuthTokenConfig struct {
	Name  string `yaml:"name"`
	Token string `yaml:"token"`
	Role  string `yaml:"role"`
}

type ObservabilityConfig struct {
	MetricsAddr string `yaml:"metrics_addr"`
	LogLevel    string `yaml:"log_level"`
}

type ControlPlaneConfig struct {
	ListenAddr               string        `yaml:"listen_addr"`
	GRPCH2ListenAddr         string        `yaml:"grpc_h2_listen_addr"`
	HeartbeatTimeout         time.Duration `yaml:"heartbeat_timeout"`
	TLSMode                  string        `yaml:"tls_mode"`
	TLSCertSource            string        `yaml:"tls_cert_source"`
	TLSCertFile              string        `yaml:"tls_cert_file"`
	TLSKeyFile               string        `yaml:"tls_key_file"`
	TLSCACertFile            string        `yaml:"tls_ca_cert_file"`
	TLSCAKeyFile             string        `yaml:"tls_ca_key_file"`
	TLSServerCommonName      string        `yaml:"tls_server_common_name"`
	TLSServerSANDNS          []string      `yaml:"tls_server_san_dns"`
	TLSServerSANIPs          []string      `yaml:"tls_server_san_ips"`
	TLSServerCertTTL         time.Duration `yaml:"tls_server_cert_ttl"`
	TLSServerCertRenewBefore time.Duration `yaml:"tls_server_cert_renew_before"`
}

type TunnelReuseConfig struct {
	MaxReuseCount     int           `yaml:"max_reuse_count"`
	RecycleTimeout    time.Duration `yaml:"recycle_timeout"`
	CloseAckTimeout   time.Duration `yaml:"close_ack_timeout"`
	EnforceFinalClose bool          `yaml:"enforce_final_close"`
}

// DefaultConfig returns a runnable baseline configuration.
func DefaultConfig() Config {
	return Config{
		Ingress: IngressConfig{
			HTTPAddr:     ":38080",
			GRPCAddr:     ":38081",
			HTTPSAddr:    ":8443",
			TLSSNIAddr:   ":8443",
			TCPPortRange: "9000-9100",
		},
		Admin: AdminConfig{
			Enabled:             true,
			ListenAddr:          ":39081",
			AllowSharedListener: false,
			BasePath:            "/admin",
			UIEnabled:           true,
			AuthMode:            "bearer",
			AuthTokens: []AdminAuthTokenConfig{
				{
					Name:  "viewer",
					Token: "devbridge-viewer-token",
					Role:  "viewer",
				},
				{
					Name:  "operator",
					Token: "devbridge-operator-token",
					Role:  "operator",
				},
				{
					Name:  "admin",
					Token: "devbridge-admin-token",
					Role:  "admin",
				},
			},
			CookieTokenName: "devbridge_admin_token",
			CSRFCookieName:  "devbridge_admin_csrf",
			CSRFHeaderName:  "X-CSRF-Token",
			AllowedOrigins: []string{
				"http://127.0.0.1:39081",
				"http://localhost:39081",
			},
		},
		Observability: ObservabilityConfig{
			MetricsAddr: ":39090",
			LogLevel:    "info",
		},
		ControlPlane: ControlPlaneConfig{
			ListenAddr:               ":39080",
			GRPCH2ListenAddr:         ":39082",
			HeartbeatTimeout:         30 * time.Second,
			TLSMode:                  string(controlPlaneTLSModePlaintext),
			TLSCertSource:            string(controlPlaneTLSCertSourceExternal),
			TLSServerCertTTL:         168 * time.Hour,
			TLSServerCertRenewBefore: 24 * time.Hour,
		},
		TunnelReuse: TunnelReuseConfig{
			MaxReuseCount:     256,
			RecycleTimeout:    3 * time.Second,
			CloseAckTimeout:   3 * time.Second,
			EnforceFinalClose: true,
		},
	}
}

// Validate ensures required config fields are present.
func (c Config) Validate() error {
	if err := ingress.ValidateSharedTLSListenerConstraint(ingress.SharedTLSListenerConfig{
		HTTPSListenAddr:  c.Ingress.HTTPSAddr,
		TLSSNIListenAddr: c.Ingress.TLSSNIAddr,
	}); err != nil {
		return err
	}
	if c.Admin.Enabled && strings.TrimSpace(c.Admin.ListenAddr) == "" {
		// 仅在启用管理面时要求提供监听地址，关闭时允许为空。
		return fmt.Errorf("validate config: empty admin listen addr when admin is enabled")
	}
	if c.Admin.Enabled {
		adminAuthMode := strings.ToLower(strings.TrimSpace(c.Admin.AuthMode))
		if adminAuthMode == "" {
			return fmt.Errorf("validate config: empty admin auth mode when admin is enabled")
		}
		if adminAuthMode != "bearer" && adminAuthMode != "cookie" {
			return fmt.Errorf("validate config: unsupported admin auth mode=%s", c.Admin.AuthMode)
		}
		if len(c.Admin.AuthTokens) == 0 {
			return fmt.Errorf("validate config: empty admin auth tokens when admin is enabled")
		}
		tokenSet := make(map[string]struct{}, len(c.Admin.AuthTokens))
		for _, tokenConfig := range c.Admin.AuthTokens {
			normalizedToken := strings.TrimSpace(tokenConfig.Token)
			if normalizedToken == "" {
				return fmt.Errorf("validate config: empty admin auth token value")
			}
			if _, exists := tokenSet[normalizedToken]; exists {
				return fmt.Errorf("validate config: duplicated admin auth token")
			}
			tokenSet[normalizedToken] = struct{}{}
			normalizedRole := strings.ToLower(strings.TrimSpace(tokenConfig.Role))
			if normalizedRole != "viewer" && normalizedRole != "operator" && normalizedRole != "admin" {
				return fmt.Errorf("validate config: unsupported admin auth role=%s", tokenConfig.Role)
			}
		}
		if adminAuthMode == "cookie" {
			if strings.TrimSpace(c.Admin.CookieTokenName) == "" {
				return fmt.Errorf("validate config: empty admin cookie token name when auth mode is cookie")
			}
			if strings.TrimSpace(c.Admin.CSRFCookieName) == "" {
				return fmt.Errorf("validate config: empty admin csrf cookie name when auth mode is cookie")
			}
			if strings.TrimSpace(c.Admin.CSRFHeaderName) == "" {
				return fmt.Errorf("validate config: empty admin csrf header name when auth mode is cookie")
			}
			if len(c.Admin.AllowedOrigins) == 0 {
				return fmt.Errorf("validate config: empty admin allowed origins when auth mode is cookie")
			}
			for _, rawOrigin := range c.Admin.AllowedOrigins {
				if _, ok := normalizeOrigin(rawOrigin); !ok {
					return fmt.Errorf("validate config: invalid admin allowed origin=%s", rawOrigin)
				}
			}
		}
		if err := c.validateAdminNetworkIsolation(); err != nil {
			return err
		}
	}
	if strings.TrimSpace(c.ControlPlane.ListenAddr) == "" {
		return fmt.Errorf("validate config: empty control plane listen addr")
	}
	if strings.TrimSpace(c.ControlPlane.GRPCH2ListenAddr) == "" {
		return fmt.Errorf("validate config: empty grpc_h2 control plane listen addr")
	}
	if strings.TrimSpace(c.ControlPlane.GRPCH2ListenAddr) == strings.TrimSpace(c.ControlPlane.ListenAddr) {
		return fmt.Errorf("validate config: grpc_h2 listen addr must be different from tcp listen addr")
	}
	normalizedTLSMode, err := normalizeControlPlaneTLSMode(c.ControlPlane.TLSMode)
	if err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	normalizedTLSCertSource, err := normalizeControlPlaneTLSCertSource(c.ControlPlane.TLSCertSource)
	if err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	if normalizedTLSMode != controlPlaneTLSModePlaintext {
		switch normalizedTLSCertSource {
		case controlPlaneTLSCertSourceExternal:
			if strings.TrimSpace(c.ControlPlane.TLSCertFile) == "" {
				return fmt.Errorf(
					"validate config: empty control_plane.tls_cert_file when tls_mode=%s tls_cert_source=%s",
					normalizedTLSMode,
					normalizedTLSCertSource,
				)
			}
			if strings.TrimSpace(c.ControlPlane.TLSKeyFile) == "" {
				return fmt.Errorf(
					"validate config: empty control_plane.tls_key_file when tls_mode=%s tls_cert_source=%s",
					normalizedTLSMode,
					normalizedTLSCertSource,
				)
			}
		case controlPlaneTLSCertSourceManagedCA:
			if strings.TrimSpace(c.ControlPlane.TLSCACertFile) == "" {
				return fmt.Errorf(
					"validate config: empty control_plane.tls_ca_cert_file when tls_mode=%s tls_cert_source=%s",
					normalizedTLSMode,
					normalizedTLSCertSource,
				)
			}
			if strings.TrimSpace(c.ControlPlane.TLSCAKeyFile) == "" {
				return fmt.Errorf(
					"validate config: empty control_plane.tls_ca_key_file when tls_mode=%s tls_cert_source=%s",
					normalizedTLSMode,
					normalizedTLSCertSource,
				)
			}
			sanDNSNames := normalizeNonEmptyStringSlice(c.ControlPlane.TLSServerSANDNS)
			sanIPTexts := normalizeNonEmptyStringSlice(c.ControlPlane.TLSServerSANIPs)
			// managed_ca 模式必须至少有一个 SAN，确保 Agent 可按地址做证书匹配。
			if len(sanDNSNames) == 0 && len(sanIPTexts) == 0 {
				return fmt.Errorf(
					"validate config: empty control_plane tls server san when tls_mode=%s tls_cert_source=%s",
					normalizedTLSMode,
					normalizedTLSCertSource,
				)
			}
			for _, sanIPText := range sanIPTexts {
				if net.ParseIP(sanIPText) == nil {
					return fmt.Errorf("validate config: invalid control_plane.tls_server_san_ips item=%s", sanIPText)
				}
			}
			if c.ControlPlane.TLSServerCertTTL <= 0 {
				return fmt.Errorf("validate config: invalid control_plane.tls_server_cert_ttl=%s", c.ControlPlane.TLSServerCertTTL)
			}
			if c.ControlPlane.TLSServerCertRenewBefore < 0 {
				return fmt.Errorf(
					"validate config: invalid control_plane.tls_server_cert_renew_before=%s",
					c.ControlPlane.TLSServerCertRenewBefore,
				)
			}
			if c.ControlPlane.TLSServerCertRenewBefore >= c.ControlPlane.TLSServerCertTTL {
				return fmt.Errorf(
					"validate config: control_plane.tls_server_cert_renew_before must be less than tls_server_cert_ttl",
				)
			}
		default:
			return fmt.Errorf("validate config: unsupported control_plane.tls_cert_source=%s", normalizedTLSCertSource)
		}
	}
	if c.TunnelReuse.MaxReuseCount <= 0 {
		return fmt.Errorf("validate config: invalid tunnel_reuse.max_reuse_count=%d", c.TunnelReuse.MaxReuseCount)
	}
	if c.TunnelReuse.RecycleTimeout <= 0 {
		return fmt.Errorf("validate config: invalid tunnel_reuse.recycle_timeout=%v", c.TunnelReuse.RecycleTimeout)
	}
	if c.TunnelReuse.CloseAckTimeout <= 0 {
		return fmt.Errorf("validate config: invalid tunnel_reuse.close_ack_timeout=%v", c.TunnelReuse.CloseAckTimeout)
	}
	return nil
}

// validateAdminNetworkIsolation 校验管理面监听地址与业务端口默认隔离，避免路径前缀成为唯一边界。
func (c Config) validateAdminNetworkIsolation() error {
	if !c.Admin.Enabled || c.Admin.AllowSharedListener {
		return nil
	}
	normalizedAdminAddr := normalizeListenAddr(c.Admin.ListenAddr)
	if normalizedAdminAddr == "" {
		return nil
	}
	addressesToCompare := []struct {
		name string
		addr string
	}{
		{name: "ingress.http_addr", addr: c.Ingress.HTTPAddr},
		{name: "ingress.grpc_addr", addr: c.Ingress.GRPCAddr},
		{name: "ingress.https_addr", addr: c.Ingress.HTTPSAddr},
		{name: "ingress.tls_sni_addr", addr: c.Ingress.TLSSNIAddr},
		{name: "observability.metrics_addr", addr: c.Observability.MetricsAddr},
		{name: "control_plane.listen_addr", addr: c.ControlPlane.ListenAddr},
		{name: "control_plane.grpc_h2_listen_addr", addr: c.ControlPlane.GRPCH2ListenAddr},
	}
	for _, candidate := range addressesToCompare {
		if normalizeListenAddr(candidate.addr) != normalizedAdminAddr {
			continue
		}
		// 默认要求 admin 独立监听；确有需要时可显式打开 allow_shared_listener。
		return fmt.Errorf(
			"validate config: admin listen addr conflicts with %s (set admin.allow_shared_listener=true to override)",
			candidate.name,
		)
	}
	return nil
}

// normalizeListenAddr 统一归一化监听地址，便于配置冲突比较。
func normalizeListenAddr(listenAddr string) string {
	normalizedListenAddr := strings.ToLower(strings.TrimSpace(listenAddr))
	if normalizedListenAddr == "" {
		return ""
	}
	host, port, splitErr := net.SplitHostPort(normalizedListenAddr)
	if splitErr != nil {
		// 非 host:port 形式（或解析失败）时保留原字符串作为兜底比较值。
		return normalizedListenAddr
	}
	normalizedPort := strings.TrimSpace(port)
	if parsedPort, err := strconv.Atoi(normalizedPort); err == nil && parsedPort >= 0 && parsedPort <= 65535 {
		normalizedPort = strconv.Itoa(parsedPort)
	}
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	switch normalizedHost {
	case "", "0.0.0.0", "::", "[::]", "*":
		normalizedHost = "*"
	}
	return normalizedHost + ":" + normalizedPort
}

// normalizeOrigin 统一归一化 Origin 字符串为 scheme://host 形式。
func normalizeOrigin(rawOrigin string) (string, bool) {
	trimmedOrigin := strings.TrimSpace(rawOrigin)
	if trimmedOrigin == "" {
		return "", false
	}
	parsedOrigin, err := url.Parse(trimmedOrigin)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(parsedOrigin.Scheme) == "" || strings.TrimSpace(parsedOrigin.Host) == "" {
		return "", false
	}
	if strings.TrimSpace(parsedOrigin.Path) != "" && strings.TrimSpace(parsedOrigin.Path) != "/" {
		return "", false
	}
	if strings.TrimSpace(parsedOrigin.RawQuery) != "" || strings.TrimSpace(parsedOrigin.Fragment) != "" {
		return "", false
	}
	return strings.ToLower(parsedOrigin.Scheme) + "://" + strings.ToLower(parsedOrigin.Host), true
}

// normalizeNonEmptyStringSlice 对字符串数组做 trim，并过滤空值项。
func normalizeNonEmptyStringSlice(rawItems []string) []string {
	if len(rawItems) == 0 {
		return nil
	}
	normalizedItems := make([]string, 0, len(rawItems))
	for _, rawItem := range rawItems {
		normalizedItem := strings.TrimSpace(rawItem)
		if normalizedItem == "" {
			continue
		}
		normalizedItems = append(normalizedItems, normalizedItem)
	}
	return normalizedItems
}
