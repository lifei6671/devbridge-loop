package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// Config defines top-level runtime settings for the agent skeleton.
type Config struct {
	AgentID         string               `yaml:"agent_id"`
	BridgeAddr      string               `yaml:"bridge_addr"`
	BridgeTransport string               `yaml:"bridge_transport"`
	BridgeTLS       BridgeTLSConfig      `yaml:"bridge_tls"`
	Session         SessionConfig        `yaml:"session"`
	TunnelPool      TunnelPoolConfig     `yaml:"tunnel_pool"`
	Observability   ObservabilityConfig  `yaml:"observability"`
	ControlChannel  ControlChannelConfig `yaml:"control_channel"`
	UI              LocalUIConfig        `yaml:"ui"`
	// RuntimeConfigFilePath 记录用户目录配置文件路径，仅运行时使用，不参与 YAML 编解码。
	RuntimeConfigFilePath string `yaml:"-"`
	// RuntimeBaseConfigFilePath 记录当前最高优先级基础配置文件路径，仅运行时使用，不参与 YAML 编解码。
	RuntimeBaseConfigFilePath string `yaml:"-"`
	// RuntimeSystemConfigFilePath 记录系统级配置文件路径，仅运行时使用，不参与 YAML 编解码。
	RuntimeSystemConfigFilePath string `yaml:"-"`
	// RuntimeLocalConfigFilePath 记录程序工作目录配置文件路径，仅运行时使用，不参与 YAML 编解码。
	RuntimeLocalConfigFilePath string `yaml:"-"`
	// RuntimeExplicitConfigFilePath 记录 -config 显式传入的配置文件路径，仅运行时使用，不参与 YAML 编解码。
	RuntimeExplicitConfigFilePath string `yaml:"-"`
}

// BridgeTLSConfig 描述 Agent 连接 Bridge 时使用的 TLS 参数。
type BridgeTLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	RootCAFile string `yaml:"root_ca_file"`
	ServerName string `yaml:"server_name"`
}

type SessionConfig struct {
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	AuthTimeout       time.Duration `yaml:"auth_timeout"`
	AuthMethod        string        `yaml:"auth_method"`
	AuthToken         string        `yaml:"auth_token"`
	ClientCapVersion  string        `yaml:"client_cap_version"`
}

type TunnelPoolConfig struct {
	MinIdle      int           `yaml:"min_idle"`
	MaxIdle      int           `yaml:"max_idle"`
	MaxInflight  int           `yaml:"max_inflight"`
	TTL          time.Duration `yaml:"ttl"`
	MaxReuse     int           `yaml:"max_reuse"`
	RecycleAckTO time.Duration `yaml:"recycle_ack_timeout"`
	OpenRate     float64       `yaml:"open_rate"`
	OpenBurst    int           `yaml:"open_burst"`
	ReconcileGap time.Duration `yaml:"reconcile_gap"`
}

// TunnelPoolOverride 用于外部按字段覆盖 tunnelPool 参数。
//
// 约束：
// 1. nil 表示未传入，不覆盖原值。
// 2. 非 nil 表示显式传入，覆盖原值后再走 Validate 校验。
type TunnelPoolOverride struct {
	MinIdle      *int
	MaxIdle      *int
	MaxInflight  *int
	TTL          *time.Duration
	MaxReuse     *int
	RecycleAckTO *time.Duration
	OpenRate     *float64
	OpenBurst    *int
	ReconcileGap *time.Duration
}

type ObservabilityConfig struct {
	MetricsAddr string `yaml:"metrics_addr"`
	LogLevel    string `yaml:"log_level"`
}

type ControlChannelConfig struct {
	DialTimeout time.Duration `yaml:"dial_timeout"`
}

type LocalUIConfig struct {
	Web WebUIConfig `yaml:"web"`
}

type WebUIConfig struct {
	Enabled           bool            `yaml:"enabled"`
	ListenAddr        string          `yaml:"listen_addr"`
	BasePath          string          `yaml:"base_path"`
	SessionCookieName string          `yaml:"session_cookie_name"`
	Auth              WebUIAuthConfig `yaml:"auth"`
}

type WebUIAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// DefaultConfig returns a runnable baseline configuration.
func DefaultConfig() Config {
	return Config{
		AgentID:         "agent-local",
		BridgeAddr:      "127.0.0.1:39081",
		BridgeTransport: transport.BindingTypeTCPFramed.String(),
		BridgeTLS: BridgeTLSConfig{
			Enabled: false,
		},
		Session: SessionConfig{
			HeartbeatInterval: 5 * time.Second,
			AuthTimeout:       5 * time.Second,
			AuthMethod:        "token",
			AuthToken:         "dbt_agent-local.agent-dev-secret",
			ClientCapVersion:  "agent-core/v1",
		},
		TunnelPool: TunnelPoolConfig{
			MinIdle:      8,
			MaxIdle:      32,
			MaxInflight:  4,
			TTL:          10 * time.Minute,
			MaxReuse:     256,
			RecycleAckTO: 3 * time.Second,
			OpenRate:     10, // 平滑建连速率（每秒）。
			OpenBurst:    20, // 冷启动允许的突发窗口。
			ReconcileGap: time.Second,
		},
		Observability: ObservabilityConfig{
			MetricsAddr: "127.0.0.1:39090",
			LogLevel:    "info",
		},
		ControlChannel: ControlChannelConfig{
			DialTimeout: 5 * time.Second,
		},
		UI: LocalUIConfig{
			Web: WebUIConfig{
				BasePath:          "/agent",
				SessionCookieName: "devbridge_agent_session",
			},
		},
	}
}

// Normalize 对配置中的可归一化字段做默认值回填与格式整理。
func (c Config) Normalize() Config {
	normalizedConfig := c
	normalizedBasePath := strings.TrimSpace(normalizedConfig.UI.Web.BasePath)
	if normalizedBasePath == "" {
		normalizedBasePath = DefaultConfig().UI.Web.BasePath
	}
	if !strings.HasPrefix(normalizedBasePath, "/") {
		normalizedBasePath = "/" + normalizedBasePath
	}
	normalizedConfig.UI.Web.BasePath = normalizedBasePath

	normalizedSessionCookieName := strings.TrimSpace(normalizedConfig.UI.Web.SessionCookieName)
	if normalizedSessionCookieName == "" {
		normalizedSessionCookieName = DefaultConfig().UI.Web.SessionCookieName
	}
	normalizedConfig.UI.Web.SessionCookieName = normalizedSessionCookieName
	normalizedConfig.UI.Web.ListenAddr = strings.TrimSpace(normalizedConfig.UI.Web.ListenAddr)
	normalizedConfig.UI.Web.Auth.Username = strings.TrimSpace(normalizedConfig.UI.Web.Auth.Username)
	normalizedConfig.UI.Web.Auth.Password = strings.TrimSpace(normalizedConfig.UI.Web.Auth.Password)
	return normalizedConfig
}

// Validate ensures required config fields are present.
func (c Config) Validate() error {
	normalizedBridgeTransport := strings.TrimSpace(c.BridgeTransport)
	if strings.TrimSpace(c.AgentID) == "" {
		// agent_id 为空会导致会话归属不明确。
		return fmt.Errorf("validate config: empty agent_id")
	}
	if strings.TrimSpace(c.BridgeAddr) == "" {
		// bridge 地址缺失时无法建连。
		return fmt.Errorf("validate config: empty bridge_addr")
	}
	if c.BridgeTLS.Enabled && strings.TrimSpace(c.BridgeTLS.RootCAFile) == "" {
		// TLS 模式下必须显式提供 Root CA，避免无意识退回系统默认证书池。
		return fmt.Errorf("validate config: empty bridge_tls.root_ca_file when tls is enabled")
	}
	switch normalizedBridgeTransport {
	case transport.BindingTypeTCPFramed.String(),
		transport.BindingTypeGRPCH2.String(),
		transport.BindingTypeQUICNative.String():
		// 当前 agent runtime 仅支持 tcp_framed / grpc_h2 / quic_native。
	default:
		return fmt.Errorf("validate config: unsupported bridge_transport=%s", c.BridgeTransport)
	}
	if normalizedBridgeTransport == transport.BindingTypeQUICNative.String() && !c.BridgeTLS.Enabled {
		// quic_native 强依赖 TLS，避免落回不受支持的明文 QUIC 模式。
		return fmt.Errorf("validate config: bridge_transport=quic_native requires bridge_tls.enabled=true")
	}
	normalizedAuthMethod := strings.ToLower(strings.TrimSpace(c.Session.AuthMethod))
	if normalizedAuthMethod == "" {
		// 认证方法为空会导致握手阶段无法分支认证策略。
		return fmt.Errorf("validate config: empty session.auth_method")
	}
	if normalizedAuthMethod != "token" {
		// 当前版本仅支持 token 认证模型。
		return fmt.Errorf("validate config: unsupported session.auth_method=%s", c.Session.AuthMethod)
	}
	if strings.TrimSpace(c.Session.AuthToken) == "" {
		// token 为空时无法通过握手认证。
		return fmt.Errorf("validate config: empty session.auth_token")
	}
	if c.TunnelPool.MinIdle < 0 {
		// min_idle 不允许为负值。
		return fmt.Errorf("validate config: invalid tunnel_pool.min_idle=%d", c.TunnelPool.MinIdle)
	}
	if c.TunnelPool.MaxIdle <= 0 {
		// max_idle 必须大于 0。
		return fmt.Errorf("validate config: invalid tunnel_pool.max_idle=%d", c.TunnelPool.MaxIdle)
	}
	if c.TunnelPool.MinIdle > c.TunnelPool.MaxIdle {
		// min_idle 不能超过 max_idle。
		return fmt.Errorf("validate config: tunnel_pool.min_idle=%d greater than max_idle=%d", c.TunnelPool.MinIdle, c.TunnelPool.MaxIdle)
	}
	if c.TunnelPool.MaxInflight <= 0 {
		// max_inflight 必须为正数。
		return fmt.Errorf("validate config: invalid tunnel_pool.max_inflight=%d", c.TunnelPool.MaxInflight)
	}
	if c.TunnelPool.TTL < 0 {
		// ttl 不能是负时长。
		return fmt.Errorf("validate config: invalid tunnel_pool.ttl=%v", c.TunnelPool.TTL)
	}
	if c.TunnelPool.MaxReuse <= 0 {
		// max_reuse 必须大于 0。
		return fmt.Errorf("validate config: invalid tunnel_pool.max_reuse=%d", c.TunnelPool.MaxReuse)
	}
	if c.TunnelPool.RecycleAckTO <= 0 {
		// recycle ack 超时必须为正数。
		return fmt.Errorf("validate config: invalid tunnel_pool.recycle_ack_timeout=%v", c.TunnelPool.RecycleAckTO)
	}
	if c.TunnelPool.OpenRate <= 0 {
		// 平滑建连速率必须为正数。
		return fmt.Errorf("validate config: invalid tunnel_pool.open_rate=%v", c.TunnelPool.OpenRate)
	}
	if c.TunnelPool.OpenBurst <= 0 {
		// 突发窗口必须为正数。
		return fmt.Errorf("validate config: invalid tunnel_pool.open_burst=%d", c.TunnelPool.OpenBurst)
	}
	if c.TunnelPool.ReconcileGap <= 0 {
		// 纠偏间隔必须为正数。
		return fmt.Errorf("validate config: invalid tunnel_pool.reconcile_gap=%v", c.TunnelPool.ReconcileGap)
	}
	if c.UI.Web.Enabled {
		if strings.TrimSpace(c.UI.Web.ListenAddr) == "" {
			return fmt.Errorf("validate config: empty ui.web.listen_addr when web ui is enabled")
		}
		if strings.TrimSpace(c.UI.Web.Auth.Username) == "" {
			return fmt.Errorf("validate config: empty ui.web.auth.username when web ui is enabled")
		}
		if strings.TrimSpace(c.UI.Web.Auth.Password) == "" {
			return fmt.Errorf("validate config: empty ui.web.auth.password when web ui is enabled")
		}
	}
	return nil
}

// ApplyTunnelPoolOverride 按字段应用 tunnelPool 覆盖参数。
func (c Config) ApplyTunnelPoolOverride(override TunnelPoolOverride) Config {
	updatedConfig := c
	updatedPool := updatedConfig.TunnelPool
	if override.MinIdle != nil {
		updatedPool.MinIdle = *override.MinIdle
	}
	if override.MaxIdle != nil {
		updatedPool.MaxIdle = *override.MaxIdle
	}
	if override.MaxInflight != nil {
		updatedPool.MaxInflight = *override.MaxInflight
	}
	if override.TTL != nil {
		updatedPool.TTL = *override.TTL
	}
	if override.MaxReuse != nil {
		updatedPool.MaxReuse = *override.MaxReuse
	}
	if override.RecycleAckTO != nil {
		updatedPool.RecycleAckTO = *override.RecycleAckTO
	}
	if override.OpenRate != nil {
		updatedPool.OpenRate = *override.OpenRate
	}
	if override.OpenBurst != nil {
		updatedPool.OpenBurst = *override.OpenBurst
	}
	if override.ReconcileGap != nil {
		updatedPool.ReconcileGap = *override.ReconcileGap
	}
	updatedConfig.TunnelPool = updatedPool
	return updatedConfig
}
