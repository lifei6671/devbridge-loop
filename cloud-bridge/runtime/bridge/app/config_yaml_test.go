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
}

// TestLoadConfigFromYAMLFileRejectsEmptyPath 验证空路径会被配置加载入口拒绝。
func TestLoadConfigFromYAMLFileRejectsEmptyPath(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := LoadConfigFromYAMLFile("   ")
	if err == nil {
		testingObject.Fatalf("expected empty yaml path to fail")
	}
}
