package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
)

// Config defines top-level runtime settings for the bridge skeleton.
type Config struct {
	Ingress       IngressConfig
	Admin         AdminConfig
	Observability ObservabilityConfig
	ControlPlane  ControlPlaneConfig
}

type IngressConfig struct {
	HTTPAddr     string
	GRPCAddr     string
	HTTPSAddr    string
	TLSSNIAddr   string
	TCPPortRange string
}

type AdminConfig struct {
	// Enabled 控制管理面总开关；关闭时不启动管理端口与相关路由。
	Enabled bool
	// ListenAddr 为管理面监听地址，仅在 Enabled=true 时生效。
	ListenAddr string
	// BasePath 为管理 UI 挂载前缀，默认 /admin。
	BasePath string
	// UIEnabled 控制管理 UI 路由是否启用（仍受 Enabled 总开关约束）。
	UIEnabled bool
	// AuthMode 定义管理 API 鉴权模式，当前支持 bearer。
	AuthMode string
	// AuthTokens 定义静态 Bearer Token 与角色映射。
	AuthTokens []AdminAuthTokenConfig
}

// AdminAuthTokenConfig 定义管理后台静态 Token 配置。
type AdminAuthTokenConfig struct {
	Name  string
	Token string
	Role  string
}

type ObservabilityConfig struct {
	MetricsAddr string
	LogLevel    string
}

type ControlPlaneConfig struct {
	ListenAddr       string
	GRPCH2ListenAddr string
	HeartbeatTimeout time.Duration
}

// DefaultConfig returns a runnable baseline configuration.
func DefaultConfig() Config {
	return Config{
		Ingress: IngressConfig{
			HTTPAddr:     ":8080",
			GRPCAddr:     ":8081",
			HTTPSAddr:    ":8443",
			TLSSNIAddr:   ":8443",
			TCPPortRange: "9000-9100",
		},
		Admin: AdminConfig{
			Enabled:    false,
			ListenAddr: ":39081",
			BasePath:   "/admin",
			UIEnabled:  false,
			AuthMode:   "bearer",
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
		},
		Observability: ObservabilityConfig{
			MetricsAddr: ":39090",
			LogLevel:    "info",
		},
		ControlPlane: ControlPlaneConfig{
			ListenAddr:       ":39080",
			GRPCH2ListenAddr: ":39082",
			HeartbeatTimeout: 30 * time.Second,
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
		if adminAuthMode != "bearer" {
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
	return nil
}
