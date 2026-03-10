package app

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/discovery"
)

func newDiscoveryResolver(cfg config.Config) (discovery.Resolver, error) {
	backend, hasMore := pickPrimaryDiscoveryBackend(cfg.DiscoveryBackends)
	if backend == "" {
		slog.Warn("no discovery backend enabled, fallback to noop resolver")
		return discovery.NoopResolver{}, nil
	}
	if hasMore {
		// 发现总开关改为“单选”后，这里兜底处理历史配置，避免多个发现器同时生效。
		slog.Warn("multiple discovery backends configured, only first backend takes effect",
			"configured", cfg.DiscoveryBackends,
			"selected", backend,
		)
	}

	resolver, err := buildDiscoveryResolver(backend, cfg)
	if err != nil {
		return nil, err
	}
	slog.Info("bridge service discovery enabled",
		"backend", backend,
		"localFile", strings.TrimSpace(cfg.DiscoveryLocalFile),
	)
	return resolver, nil
}

func buildDiscoveryResolver(backend string, cfg config.Config) (discovery.Resolver, error) {
	// 运行时仅启动一个发现器，避免多个发现源同时生效导致行为不可预期。
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "local":
		resolver, err := discovery.NewLocalFileResolver(cfg.DiscoveryLocalFile)
		if err != nil {
			return nil, fmt.Errorf("init local discovery resolver failed: %w", err)
		}
		return resolver, nil
	case "nacos":
		return discovery.NewNacosResolver(discovery.NacosConfig{
			ServerAddr:     cfg.NacosDiscovery.ServerAddr,
			Namespace:      cfg.NacosDiscovery.Namespace,
			Group:          cfg.NacosDiscovery.Group,
			ServicePattern: cfg.NacosDiscovery.ServicePattern,
			Username:       cfg.NacosDiscovery.Username,
			Password:       cfg.NacosDiscovery.Password,
			Timeout:        cfg.DiscoveryTimeout,
		}), nil
	case "etcd":
		return discovery.NewEtcdResolver(discovery.EtcdConfig{
			Endpoints: cfg.EtcdDiscovery.Endpoints,
			KeyPrefix: cfg.EtcdDiscovery.KeyPrefix,
			Timeout:   cfg.DiscoveryTimeout,
		}), nil
	case "consul":
		return discovery.NewConsulResolver(discovery.ConsulConfig{
			Addr:           cfg.ConsulDiscovery.Addr,
			Datacenter:     cfg.ConsulDiscovery.Datacenter,
			ServicePattern: cfg.ConsulDiscovery.ServicePattern,
			Timeout:        cfg.DiscoveryTimeout,
		}), nil
	default:
		return discovery.NoopResolver{}, nil
	}
}

func pickPrimaryDiscoveryBackend(backends []string) (backend string, hasMore bool) {
	// 历史配置可能写入多个后端，这里按声明顺序选第一个作为最终生效项。
	valid := make([]string, 0, len(backends))
	for _, raw := range backends {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "local", "nacos", "etcd", "consul":
			valid = append(valid, strings.ToLower(strings.TrimSpace(raw)))
		}
	}
	if len(valid) == 0 {
		return "", false
	}
	return valid[0], len(valid) > 1
}
