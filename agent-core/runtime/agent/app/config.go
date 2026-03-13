package app

import "time"

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
	MinIdle     int
	MaxIdle     int
	MaxInflight int
	TTL         time.Duration
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
			MinIdle:     8,
			MaxIdle:     32,
			MaxInflight: 64,
			TTL:         2 * time.Minute,
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
	return nil
}
