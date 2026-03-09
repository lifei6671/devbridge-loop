package config

import (
	"os"
	"strconv"
	"time"
)

// Config defines the runtime configuration for agent-core.
type Config struct {
	HTTPAddr string
	RDName   string
	EnvName  string

	Registration RegistrationConfig
}

// RegistrationConfig contains registration lifecycle settings.
type RegistrationConfig struct {
	DefaultTTLSeconds int
	ScanInterval      time.Duration
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
	}
	if cfg.Registration.DefaultTTLSeconds <= 0 {
		cfg.Registration.DefaultTTLSeconds = 30
	}
	if cfg.Registration.ScanInterval <= 0 {
		cfg.Registration.ScanInterval = 5 * time.Second
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
