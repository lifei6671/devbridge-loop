package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/tunnelmasque"
)

const (
	tunnelProtocolHTTP   = "http"
	tunnelProtocolMASQUE = "masque"
)

// TunnelProtocolRuntime 抽象具体协议运行时，屏蔽 app 层对底层协议细节的直接依赖。
type TunnelProtocolRuntime interface {
	Protocol() string
	Run(ctx context.Context) error
	Close() error
}

type masqueProtocolRuntime struct {
	server *tunnelmasque.Server
}

func (r *masqueProtocolRuntime) Protocol() string {
	return tunnelProtocolMASQUE
}

func (r *masqueProtocolRuntime) Run(ctx context.Context) error {
	return r.server.Run(ctx)
}

func (r *masqueProtocolRuntime) Close() error {
	return r.server.Close()
}

// effectiveTunnelProtocols 返回最终生效的 tunnel 协议集合，并保证默认值与去重规则一致。
func effectiveTunnelProtocols(cfg config.Config) []string {
	raw := cfg.TunnelSyncProtocols
	if len(raw) == 0 {
		raw = []string{cfg.TunnelSyncProtocol}
	}

	protocols := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, protocol := range raw {
		normalized := strings.ToLower(strings.TrimSpace(protocol))
		switch normalized {
		case tunnelProtocolHTTP, tunnelProtocolMASQUE:
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			protocols = append(protocols, normalized)
		}
	}

	// 若配置未指定或全部无效，默认启用 MASQUE。
	if len(protocols) == 0 {
		return []string{tunnelProtocolMASQUE}
	}
	return protocols
}

// newTunnelProtocolRuntimes 根据配置构建协议运行时实例。
func newTunnelProtocolRuntimes(cfg config.Config, stateStore *store.MemoryStore) ([]TunnelProtocolRuntime, error) {
	protocols := effectiveTunnelProtocols(cfg)
	runtimes := make([]TunnelProtocolRuntime, 0, len(protocols))

	for _, protocol := range protocols {
		switch protocol {
		// HTTP tunnel 同步复用主 HTTP API 服务，不需要单独 runtime。
		case tunnelProtocolHTTP:
			continue
		case tunnelProtocolMASQUE:
			server, err := tunnelmasque.NewServer(cfg, stateStore)
			if err != nil {
				return nil, fmt.Errorf("init %s tunnel runtime failed: %w", protocol, err)
			}
			runtimes = append(runtimes, &masqueProtocolRuntime{server: server})
		}
	}

	return runtimes, nil
}

func tunnelProtocolEnabled(protocols []string, protocol string) bool {
	target := strings.ToLower(strings.TrimSpace(protocol))
	for _, configured := range protocols {
		if strings.EqualFold(strings.TrimSpace(configured), target) {
			return true
		}
	}
	return false
}
