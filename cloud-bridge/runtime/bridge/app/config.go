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
			HTTPSAddr:    ":8443",
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
	if err := ingress.ValidateSharedTLSListenerConstraint(ingress.SharedTLSListenerConfig{
		HTTPSListenAddr:  c.Ingress.HTTPSAddr,
		TLSSNIListenAddr: c.Ingress.TLSSNIAddr,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(c.Admin.ListenAddr) == "" {
		return fmt.Errorf("validate config: empty admin listen addr")
	}
	return nil
}
