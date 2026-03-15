package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseConfigYAMLAppliesDefaultsAndOverrides 验证 YAML 仅覆盖显式字段，其余字段保持默认值。
func TestParseConfigYAMLAppliesDefaultsAndOverrides(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
admin:
  enabled: true
  ui_enabled: true
  listen_addr: ":49081"
  auth_mode: bearer
  auth_tokens:
    - name: viewer
      token: viewer-token
      role: viewer
    - name: operator
      token: operator-token
      role: operator
    - name: admin
      token: admin-token
      role: admin
control_plane:
  listen_addr: ":49080"
  grpc_h2_listen_addr: ":49082"
  heartbeat_timeout: 45s
observability:
  log_level: debug
`
	config, err := ParseConfigYAML([]byte(configYAML))
	if err != nil {
		testingObject.Fatalf("parse config yaml failed: %v", err)
	}
	if !config.Admin.Enabled {
		testingObject.Fatalf("expected admin enabled from yaml")
	}
	if !config.Admin.UIEnabled {
		testingObject.Fatalf("expected admin ui enabled from yaml")
	}
	if config.Admin.ListenAddr != ":49081" {
		testingObject.Fatalf("unexpected admin listen addr: got=%s want=%s", config.Admin.ListenAddr, ":49081")
	}
	if config.ControlPlane.HeartbeatTimeout != 45*time.Second {
		testingObject.Fatalf("unexpected heartbeat timeout: got=%s want=%s", config.ControlPlane.HeartbeatTimeout, 45*time.Second)
	}
	// 未在 YAML 中配置的字段应继续沿用默认值。
	if config.Ingress.HTTPAddr != ":8080" {
		testingObject.Fatalf("unexpected ingress http addr default: got=%s want=%s", config.Ingress.HTTPAddr, ":8080")
	}
	if len(config.Admin.AuthTokens) != 3 {
		testingObject.Fatalf("unexpected admin token count: got=%d want=%d", len(config.Admin.AuthTokens), 3)
	}
}

// TestParseConfigYAMLRejectsUnknownFields 验证未知配置字段会被严格校验拒绝。
func TestParseConfigYAMLRejectsUnknownFields(testingObject *testing.T) {
	testingObject.Parallel()

	configYAML := `
unknown_root: true
`
	_, err := ParseConfigYAML([]byte(configYAML))
	if err == nil {
		testingObject.Fatalf("expected parse yaml config to fail for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown_root") {
		testingObject.Fatalf("unexpected parse error: %v", err)
	}
}

// TestLoadConfigFromYAMLFile 验证可从指定 YAML 文件路径加载配置。
func TestLoadConfigFromYAMLFile(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	configFilePath := filepath.Join(tempDir, "bridge.yaml")
	configYAML := `
admin:
  enabled: false
control_plane:
  listen_addr: ":59080"
  grpc_h2_listen_addr: ":59082"
`
	if err := os.WriteFile(configFilePath, []byte(configYAML), 0o600); err != nil {
		testingObject.Fatalf("write temp yaml file failed: %v", err)
	}
	config, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load yaml config file failed: %v", err)
	}
	if config.ControlPlane.ListenAddr != ":59080" {
		testingObject.Fatalf("unexpected control plane listen addr: got=%s want=%s", config.ControlPlane.ListenAddr, ":59080")
	}
	if config.Admin.Enabled {
		testingObject.Fatalf("expected admin disabled from yaml")
	}
	if config.RuntimeConfigFilePath != configFilePath {
		testingObject.Fatalf("unexpected runtime config file path: got=%s want=%s", config.RuntimeConfigFilePath, configFilePath)
	}
}

// TestLoadConfigFromYAMLFileRejectsEmptyPath 验证空路径会被配置加载入口拒绝。
func TestLoadConfigFromYAMLFileRejectsEmptyPath(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := LoadConfigFromYAMLFile("   ")
	if err == nil {
		testingObject.Fatalf("expected empty yaml path to fail")
	}
}

// TestSaveConfigToYAMLFileRoundTrip 验证配置可写回 YAML 并可再次加载。
func TestSaveConfigToYAMLFileRoundTrip(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	configFilePath := filepath.Join(tempDir, "bridge.yaml")
	config := DefaultConfig()
	config.Ingress.HTTPAddr = ":18080"
	config.Ingress.GRPCAddr = ":18081"
	config.Ingress.TCPPortRange = "9100-9200"
	config.ControlPlane.ListenAddr = ":19080"
	config.ControlPlane.GRPCH2ListenAddr = ":19082"
	config.ControlPlane.HeartbeatTimeout = 45 * time.Second
	config.Admin.BasePath = "/console"
	config.Observability.LogLevel = "debug"

	if err := SaveConfigToYAMLFile(config, configFilePath); err != nil {
		testingObject.Fatalf("save config yaml failed: %v", err)
	}
	loadedConfig, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config yaml failed: %v", err)
	}
	if loadedConfig.Ingress.HTTPAddr != config.Ingress.HTTPAddr {
		testingObject.Fatalf("unexpected ingress.http_addr: got=%s want=%s", loadedConfig.Ingress.HTTPAddr, config.Ingress.HTTPAddr)
	}
	if loadedConfig.ControlPlane.HeartbeatTimeout != config.ControlPlane.HeartbeatTimeout {
		testingObject.Fatalf(
			"unexpected control_plane.heartbeat_timeout: got=%s want=%s",
			loadedConfig.ControlPlane.HeartbeatTimeout,
			config.ControlPlane.HeartbeatTimeout,
		)
	}
	if loadedConfig.Admin.BasePath != config.Admin.BasePath {
		testingObject.Fatalf("unexpected admin.base_path: got=%s want=%s", loadedConfig.Admin.BasePath, config.Admin.BasePath)
	}
	if loadedConfig.Observability.LogLevel != config.Observability.LogLevel {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=%s", loadedConfig.Observability.LogLevel, config.Observability.LogLevel)
	}
}
