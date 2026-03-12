package tcpbinding

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const defaultTunnelIDPrefix = "tcp-framed-tunnel"

// TunnelIdentityConfig 描述 tcp tunnel 的身份信息。
type TunnelIdentityConfig struct {
	SessionID      string
	SessionEpoch   uint64
	TunnelIDPrefix string
	Labels         map[string]string
}

// normalized 返回标准化后的身份配置。
func (config TunnelIdentityConfig) normalized() TunnelIdentityConfig {
	normalizedConfig := config
	normalizedConfig.SessionID = strings.TrimSpace(config.SessionID)
	normalizedPrefix := strings.TrimSpace(config.TunnelIDPrefix)
	if normalizedPrefix == "" {
		// 未指定前缀时使用 tcp_framed 默认命名。
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

// tunnelIDGenerator 负责生成单调递增的 tunnel id。
type tunnelIDGenerator struct {
	prefix   string
	sequence atomic.Uint64
}

// newTunnelIDGenerator 创建 tunnel id 生成器。
func newTunnelIDGenerator(prefix string) *tunnelIDGenerator {
	normalizedPrefix := strings.TrimSpace(prefix)
	if normalizedPrefix == "" {
		// 前缀为空时统一回退默认值。
		normalizedPrefix = defaultTunnelIDPrefix
	}
	return &tunnelIDGenerator{prefix: normalizedPrefix}
}

// Next 返回下一个 tunnel id。
func (generator *tunnelIDGenerator) Next() string {
	return fmt.Sprintf("%s-%d", generator.prefix, generator.sequence.Add(1))
}

// buildTunnelMeta 使用身份配置构造 tunnel 元数据。
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
