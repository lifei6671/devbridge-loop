package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/app"
)

// TestLoadRuntimeConfigFromEnvAppliesBridgeTLS 验证启动入口会把 Bridge TLS 环境变量接入 runtime 配置。
func TestLoadRuntimeConfigFromEnvAppliesBridgeTLS(testingObject *testing.T) {
	testingObject.Setenv(envBridgeTLSEnabled, "true")
	testingObject.Setenv(envBridgeTLSRootCAFile, "/etc/devbridge/root-ca.crt")
	testingObject.Setenv(envBridgeTLSServerName, "bridge.internal.example")

	config, _, err := loadRuntimeConfigFromEnv(app.DefaultConfig())
	if err != nil {
		testingObject.Fatalf("load runtime config from env failed: %v", err)
	}
	if !config.BridgeTLS.Enabled {
		testingObject.Fatalf("expected bridge tls enabled from env")
	}
	if config.BridgeTLS.RootCAFile != "/etc/devbridge/root-ca.crt" {
		testingObject.Fatalf("unexpected bridge tls root ca file: got=%s", config.BridgeTLS.RootCAFile)
	}
	if config.BridgeTLS.ServerName != "bridge.internal.example" {
		testingObject.Fatalf("unexpected bridge tls server name: got=%s", config.BridgeTLS.ServerName)
	}
}

// TestLoadRuntimeConfigFromEnvRejectsEnabledBridgeTLSWithoutRootCA 验证开启 Bridge TLS 后必须提供 Root CA 路径。
func TestLoadRuntimeConfigFromEnvRejectsEnabledBridgeTLSWithoutRootCA(testingObject *testing.T) {
	testingObject.Setenv(envBridgeTLSEnabled, "true")
	testingObject.Setenv(envBridgeTLSRootCAFile, "   ")

	_, _, err := loadRuntimeConfigFromEnv(app.DefaultConfig())
	if err == nil {
		testingObject.Fatalf("expected missing bridge tls root ca validation error")
	}
	if !strings.Contains(err.Error(), "bridge_tls.root_ca_file") {
		testingObject.Fatalf("unexpected error for missing bridge tls root ca: %v", err)
	}
}

// TestLoadRuntimeConfigLoadsExplicitYAML 验证启动入口支持显式 -config YAML。
func TestLoadRuntimeConfigLoadsExplicitYAML(testingObject *testing.T) {
	configFilePath := filepath.Join(testingObject.TempDir(), "agent.yaml")
	if err := os.WriteFile(configFilePath, []byte(`
agent_id: agent-web
bridge_addr: 127.0.0.1:39081
bridge_transport: tcp_framed
session:
  auth_method: token
  auth_token: yaml-token
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:39082
    auth:
      username: admin
      password: change-me
`), 0o600); err != nil {
		testingObject.Fatalf("write config file failed: %v", err)
	}

	config, _, err := loadRuntimeConfig(configFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	if config.AgentID != "agent-web" {
		testingObject.Fatalf("unexpected agent_id from yaml: %s", config.AgentID)
	}
	if !config.UI.Web.Enabled {
		testingObject.Fatalf("expected web ui enabled from yaml")
	}
}

// TestLoadRuntimeConfigFromArgsDefaultsToWebMode 验证启用 Web UI 的 YAML 在未显式传 tauri 时默认进入 web-only 模式。
func TestLoadRuntimeConfigFromArgsDefaultsToWebMode(testingObject *testing.T) {
	configFilePath := filepath.Join(testingObject.TempDir(), "agent.yaml")
	if err := os.WriteFile(configFilePath, []byte(`
agent_id: agent-web
bridge_addr: 127.0.0.1:39081
bridge_transport: tcp_framed
session:
  auth_method: token
  auth_token: yaml-token
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:39082
    auth:
      username: admin
      password: change-me
`), 0o600); err != nil {
		testingObject.Fatalf("write config file failed: %v", err)
	}

	_, _, runOptions, err := loadRuntimeConfigFromArgs([]string{"-config", configFilePath})
	if err != nil {
		testingObject.Fatalf("load runtime config from args failed: %v", err)
	}
	if !runOptions.EnableWeb {
		testingObject.Fatalf("expected web mode enabled by default")
	}
	if runOptions.EnableLocalRPC {
		testingObject.Fatalf("expected tauri/localrpc disabled without explicit -tauri flag")
	}
}

// TestLoadRuntimeConfigFromArgsRejectsMissingServeTarget 验证既未启用 Web UI 又未显式传 tauri 时会拒绝启动。
func TestLoadRuntimeConfigFromArgsRejectsMissingServeTarget(testingObject *testing.T) {
	_, _, _, err := loadRuntimeConfigFromArgs(nil)
	if err == nil {
		testingObject.Fatalf("expected missing serve target error")
	}
	if !strings.Contains(err.Error(), "-tauri") {
		testingObject.Fatalf("unexpected missing serve target error: %v", err)
	}
}

// TestLoadRuntimeConfigFromArgsEnablesTauriMode 验证显式传递 -tauri 后会启用 LocalRPC 运行模式。
func TestLoadRuntimeConfigFromArgsEnablesTauriMode(testingObject *testing.T) {
	_, _, runOptions, err := loadRuntimeConfigFromArgs([]string{"-tauri"})
	if err != nil {
		testingObject.Fatalf("load runtime config from args failed: %v", err)
	}
	if !runOptions.EnableLocalRPC {
		testingObject.Fatalf("expected localrpc enabled when -tauri is passed")
	}
	if runOptions.EnableWeb {
		testingObject.Fatalf("expected web disabled with default config when only -tauri is passed")
	}
}
