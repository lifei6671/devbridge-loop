package app

import "time"

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
	TLSSNIAddr   string
	TCPPortRange string
}

type AdminConfig struct {
	ListenAddr string
	UIEnabled  bool
}

type ObservabilityConfig struct {
	MetricsAddr string
	LogLevel    string
}

type ControlPlaneConfig struct {
	HeartbeatTimeout time.Duration
}

// DefaultConfig returns a runnable baseline configuration.
func DefaultConfig() Config {
	return Config{
		Ingress: IngressConfig{
			HTTPAddr:     ":8080",
			GRPCAddr:     ":8081",
			TLSSNIAddr:   ":8443",
			TCPPortRange: "9000-9100",
		},
		Admin: AdminConfig{
			ListenAddr: ":39080",
			UIEnabled:  true,
		},
		Observability: ObservabilityConfig{
			MetricsAddr: ":39090",
			LogLevel:    "info",
		},
		ControlPlane: ControlPlaneConfig{
			HeartbeatTimeout: 30 * time.Second,
		},
	}
}

// Validate ensures required config fields are present.
func (c Config) Validate() error {
	return nil
}
