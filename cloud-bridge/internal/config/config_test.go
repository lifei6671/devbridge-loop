package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromEnv_WithYAMLConfigFile(t *testing.T) {
	configFilePath := filepath.Join(t.TempDir(), "bridge.yaml")
	content := `
httpAddr: 0.0.0.0:48080
tunnelSyncProtocols: [http, masque]
masqueTunnelUdpAddr: 127.0.0.1:49081
routeExtractorOrder: [header, host]
bridgePublicHost: bridge.test.internal
bridgePublicPort: 8443
fallbackBackflowUrl: http://127.0.0.1:49090
ingressTimeoutSec: 15
discovery:
  backends: [local]
  timeoutMs: 3500
  nacos:
    addr: 127.0.0.1:8848
  etcd:
    endpoints: [127.0.0.1:2379]
    keyPrefix: /services
  consul:
    addr: 127.0.0.1:8500
routes:
  - env: base
    serviceName: user
    protocol: http
    host: 127.0.0.1
    port: 8081
`
	if err := os.WriteFile(configFilePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	t.Setenv(defaultBridgeConfigEnv, configFilePath)
	cfg := LoadFromEnv()

	if cfg.HTTPAddr != "0.0.0.0:48080" {
		t.Fatalf("unexpected http addr: %s", cfg.HTTPAddr)
	}
	if cfg.TunnelSyncProtocol != "http" {
		t.Fatalf("unexpected tunnel protocol: %s", cfg.TunnelSyncProtocol)
	}
	if cfg.BridgePublicHost != "bridge.test.internal" || cfg.BridgePublicPort != 8443 {
		t.Fatalf("unexpected bridge public endpoint: %s:%d", cfg.BridgePublicHost, cfg.BridgePublicPort)
	}
	if cfg.DiscoveryTimeout.Milliseconds() != 3500 {
		t.Fatalf("unexpected discovery timeout: %d", cfg.DiscoveryTimeout.Milliseconds())
	}
	if len(cfg.DiscoveryBackends) != 1 || cfg.DiscoveryBackends[0] != "local" {
		t.Fatalf("unexpected discovery backends: %+v", cfg.DiscoveryBackends)
	}
	// YAML 文件内嵌 routes 时，默认使用该文件作为 local discovery 源。
	if cfg.DiscoveryLocalFile != configFilePath {
		t.Fatalf("unexpected local discovery file: %s", cfg.DiscoveryLocalFile)
	}
}

func TestLoadFromEnv_EnvOverridesConfigFile(t *testing.T) {
	configFilePath := filepath.Join(t.TempDir(), "bridge.yaml")
	content := `
httpAddr: 0.0.0.0:48080
discovery:
  backends: [local]
routes:
  - env: base
    serviceName: order
    protocol: grpc
    host: 127.0.0.1
    port: 9091
`
	if err := os.WriteFile(configFilePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	t.Setenv(defaultBridgeConfigEnv, configFilePath)
	t.Setenv("DEVLOOP_BRIDGE_HTTP_ADDR", "0.0.0.0:58080")
	t.Setenv("DEVLOOP_BRIDGE_DISCOVERY_BACKENDS", "nacos,consul")
	cfg := LoadFromEnv()

	if cfg.HTTPAddr != "0.0.0.0:58080" {
		t.Fatalf("unexpected http addr: %s", cfg.HTTPAddr)
	}
	if len(cfg.DiscoveryBackends) != 2 || cfg.DiscoveryBackends[0] != "nacos" || cfg.DiscoveryBackends[1] != "consul" {
		t.Fatalf("unexpected discovery backends: %+v", cfg.DiscoveryBackends)
	}
}
