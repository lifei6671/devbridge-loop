package app

import (
	"fmt"
	"strings"
	"time"
)

// Config defines top-level runtime settings for the agent skeleton.
type Config struct {
	AgentID        string
	BridgeAddr     string
	Session        SessionConfig
	TunnelPool     TunnelPoolConfig
	Observability  ObservabilityConfig
	ControlChannel ControlChannelConfig
}

type SessionConfig struct {
	HeartbeatInterval time.Duration
	AuthTimeout       time.Duration
}

type TunnelPoolConfig struct {
	MinIdle      int
	MaxIdle      int
	MaxInflight  int
	TTL          time.Duration
	OpenRate     float64
	OpenBurst    int
	ReconcileGap time.Duration
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
	OpenRate     *float64
	OpenBurst    *int
	ReconcileGap *time.Duration
}

type ObservabilityConfig struct {
	MetricsAddr string
	LogLevel    string
}

type ControlChannelConfig struct {
	DialTimeout time.Duration
}

// DefaultConfig returns a runnable baseline configuration.
func DefaultConfig() Config {
	return Config{
		AgentID:    "agent-local",
		BridgeAddr: "127.0.0.1:39080",
		Session: SessionConfig{
			HeartbeatInterval: 10 * time.Second,
			AuthTimeout:       5 * time.Second,
		},
		TunnelPool: TunnelPoolConfig{
			MinIdle:      8,
			MaxIdle:      32,
			MaxInflight:  4,
			TTL:          90 * time.Second,
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
	}
}

// Validate ensures required config fields are present.
func (c Config) Validate() error {
	if strings.TrimSpace(c.AgentID) == "" {
		// agent_id 为空会导致会话归属不明确。
		return fmt.Errorf("validate config: empty agent_id")
	}
	if strings.TrimSpace(c.BridgeAddr) == "" {
		// bridge 地址缺失时无法建连。
		return fmt.Errorf("validate config: empty bridge_addr")
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
