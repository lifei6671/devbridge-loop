package grpcbinding

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const defaultTunnelIDPrefix = "grpc-h2-tunnel"

// TunnelIdentityConfig 描述 grpc tunnel 的身份信息。
type TunnelIdentityConfig struct {
	SessionID      string
	SessionEpoch   uint64
	TunnelIDPrefix string
	Labels         map[string]string
}

func (config TunnelIdentityConfig) normalized() TunnelIdentityConfig {
	normalizedConfig := config
	normalizedConfig.SessionID = strings.TrimSpace(config.SessionID)
	normalizedPrefix := strings.TrimSpace(config.TunnelIDPrefix)
	if normalizedPrefix == "" {
		normalizedPrefix = defaultTunnelIDPrefix
	}
	normalizedConfig.TunnelIDPrefix = normalizedPrefix
	if len(config.Labels) > 0 {
		normalizedConfig.Labels = make(map[string]string, len(config.Labels))
		for key, value := range config.Labels {
			normalizedConfig.Labels[key] = value
		}
	}
	return normalizedConfig
}

type tunnelIDGenerator struct {
	prefix   string
	sequence atomic.Uint64
}

func newTunnelIDGenerator(prefix string) *tunnelIDGenerator {
	normalizedPrefix := strings.TrimSpace(prefix)
	if normalizedPrefix == "" {
		normalizedPrefix = defaultTunnelIDPrefix
	}
	return &tunnelIDGenerator{prefix: normalizedPrefix}
}

func (generator *tunnelIDGenerator) Next() string {
	return fmt.Sprintf("%s-%d", generator.prefix, generator.sequence.Add(1))
}

func buildTunnelMeta(config TunnelIdentityConfig, tunnelID string, createdAt time.Time) transport.TunnelMeta {
	normalizedConfig := config.normalized()
	meta := transport.TunnelMeta{
		TunnelID:     tunnelID,
		SessionID:    normalizedConfig.SessionID,
		SessionEpoch: normalizedConfig.SessionEpoch,
		CreatedAt:    createdAt,
	}
	if len(normalizedConfig.Labels) > 0 {
		meta.Labels = make(map[string]string, len(normalizedConfig.Labels))
		for key, value := range normalizedConfig.Labels {
			meta.Labels[key] = value
		}
	}
	return meta
}
