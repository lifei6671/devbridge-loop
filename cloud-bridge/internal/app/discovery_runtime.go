package app

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/discovery"
)

func newDiscoveryResolver(cfg config.Config) (discovery.Resolver, error) {
	resolvers := make([]discovery.Resolver, 0, len(cfg.DiscoveryBackends))
	enabled := make([]string, 0, len(cfg.DiscoveryBackends))

	for _, backend := range cfg.DiscoveryBackends {
		switch strings.ToLower(strings.TrimSpace(backend)) {
		case "local":
			resolver, err := discovery.NewLocalFileResolver(cfg.DiscoveryLocalFile)
			if err != nil {
				return nil, fmt.Errorf("init local discovery resolver failed: %w", err)
			}
			resolvers = append(resolvers, resolver)
			enabled = append(enabled, "local")
		case "nacos":
			resolvers = append(resolvers, discovery.NewNacosResolver(discovery.NacosConfig{
				ServerAddr:     cfg.NacosDiscovery.ServerAddr,
				Namespace:      cfg.NacosDiscovery.Namespace,
				Group:          cfg.NacosDiscovery.Group,
				ServicePattern: cfg.NacosDiscovery.ServicePattern,
				Username:       cfg.NacosDiscovery.Username,
				Password:       cfg.NacosDiscovery.Password,
				Timeout:        cfg.DiscoveryTimeout,
			}))
			enabled = append(enabled, "nacos")
		case "etcd":
			resolvers = append(resolvers, discovery.NewEtcdResolver(discovery.EtcdConfig{
				Endpoints: cfg.EtcdDiscovery.Endpoints,
				KeyPrefix: cfg.EtcdDiscovery.KeyPrefix,
				Timeout:   cfg.DiscoveryTimeout,
			}))
			enabled = append(enabled, "etcd")
		case "consul":
			resolvers = append(resolvers, discovery.NewConsulResolver(discovery.ConsulConfig{
				Addr:           cfg.ConsulDiscovery.Addr,
				Datacenter:     cfg.ConsulDiscovery.Datacenter,
				ServicePattern: cfg.ConsulDiscovery.ServicePattern,
				Timeout:        cfg.DiscoveryTimeout,
			}))
			enabled = append(enabled, "consul")
		}
	}

	if len(resolvers) == 0 {
		slog.Warn("no discovery backend enabled, fallback to noop resolver")
		return discovery.NoopResolver{}, nil
	}

	slog.Info("bridge service discovery enabled",
		"backends", enabled,
		"localFile", strings.TrimSpace(cfg.DiscoveryLocalFile),
	)
	return discovery.NewChain(resolvers...), nil
}
