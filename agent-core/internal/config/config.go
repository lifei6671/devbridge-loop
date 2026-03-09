package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config defines the runtime configuration for agent-core.
type Config struct {
	HTTPAddr string
	RDName   string
	EnvName  string

	Registration RegistrationConfig
	Tunnel       TunnelConfig
}

// RegistrationConfig contains registration lifecycle settings.
type RegistrationConfig struct {
	DefaultTTLSeconds int
	ScanInterval      time.Duration
}

// TunnelConfig contains tunnel sync settings from agent-core to cloud-bridge.
type TunnelConfig struct {
	BridgeAddress     string
	BackflowBaseURL   string
	HeartbeatInterval time.Duration
	ReconnectBackoff  []time.Duration
	RequestTimeout    time.Duration
}

// LoadFromEnv loads config from environment variables with sane defaults.
func LoadFromEnv() Config {
	cfg := Config{
		HTTPAddr: getenv("DEVLOOP_AGENT_HTTP_ADDR", "127.0.0.1:19090"),
		RDName:   getenv("DEVLOOP_RD_NAME", "unknown-rd"),
		EnvName:  getenv("DEVLOOP_ENV_NAME", "dev-default"),
		Registration: RegistrationConfig{
			DefaultTTLSeconds: getenvInt("DEVLOOP_DEFAULT_TTL_SECONDS", 30),
			ScanInterval:      time.Duration(getenvInt("DEVLOOP_SCAN_INTERVAL_SECONDS", 5)) * time.Second,
		},
		Tunnel: TunnelConfig{
			BridgeAddress:     getenv("DEVLOOP_TUNNEL_BRIDGE_ADDRESS", "http://127.0.0.1:18080"),
			BackflowBaseURL:   getenv("DEVLOOP_TUNNEL_BACKFLOW_BASE_URL", defaultBackflowBaseURL(getenv("DEVLOOP_AGENT_HTTP_ADDR", "127.0.0.1:19090"))),
			HeartbeatInterval: time.Duration(getenvInt("DEVLOOP_TUNNEL_HEARTBEAT_INTERVAL_SEC", 10)) * time.Second,
			ReconnectBackoff:  parseDurationListMillis(getenv("DEVLOOP_TUNNEL_RECONNECT_BACKOFF_MS", "500,1000,2000,5000")),
			RequestTimeout:    time.Duration(getenvInt("DEVLOOP_TUNNEL_REQUEST_TIMEOUT_SEC", 5)) * time.Second,
		},
	}
	if cfg.Registration.DefaultTTLSeconds <= 0 {
		cfg.Registration.DefaultTTLSeconds = 30
	}
	if cfg.Registration.ScanInterval <= 0 {
		cfg.Registration.ScanInterval = 5 * time.Second
	}
	if cfg.Tunnel.HeartbeatInterval <= 0 {
		cfg.Tunnel.HeartbeatInterval = 10 * time.Second
	}
	if len(cfg.Tunnel.ReconnectBackoff) == 0 {
		cfg.Tunnel.ReconnectBackoff = []time.Duration{500 * time.Millisecond, 1 * time.Second, 2 * time.Second, 5 * time.Second}
	}
	if cfg.Tunnel.RequestTimeout <= 0 {
		cfg.Tunnel.RequestTimeout = 5 * time.Second
	}
	return cfg
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := getenv(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseDurationListMillis(value string) []time.Duration {
	parts := strings.Split(value, ",")
	result := make([]time.Duration, 0, len(parts))
	for _, part := range parts {
		millis, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || millis <= 0 {
			continue
		}
		result = append(result, time.Duration(millis)*time.Millisecond)
	}
	return result
}

func defaultBackflowBaseURL(httpAddr string) string {
	value := strings.TrimSpace(httpAddr)
	if value == "" {
		return "http://127.0.0.1:19090"
	}
	if strings.Contains(value, "://") {
		return value
	}
	if strings.HasPrefix(value, ":") {
		value = "127.0.0.1" + value
	}
	return "http://" + value
}
