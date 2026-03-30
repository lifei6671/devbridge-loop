package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestParseConfigYAMLLoadsWebUIConfig 验证 YAML 可加载 ui.web 配置。
func TestParseConfigYAMLLoadsWebUIConfig(testingObject *testing.T) {
	testingObject.Parallel()

	config, err := ParseConfigYAML([]byte(`
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
    base_path: /agent
    session_cookie_name: devbridge_agent_session
    auth:
      username: admin
      password: change-me
`))
	if err != nil {
		testingObject.Fatalf("parse config yaml failed: %v", err)
	}
	if !config.UI.Web.Enabled {
		testingObject.Fatalf("expected ui.web enabled from yaml")
	}
	if config.UI.Web.ListenAddr != "127.0.0.1:39082" {
		testingObject.Fatalf("unexpected web listen addr: %s", config.UI.Web.ListenAddr)
	}
	if config.UI.Web.Auth.Username != "admin" {
		testingObject.Fatalf("unexpected web auth username: %s", config.UI.Web.Auth.Username)
	}
}

// TestParseConfigYAMLRejectsEnabledWebUIWithoutAuth 验证 YAML 启用 Web UI 但未提供认证信息时会失败。
func TestParseConfigYAMLRejectsEnabledWebUIWithoutAuth(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := ParseConfigYAML([]byte(`
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:39082
`))
	if err == nil {
		testingObject.Fatalf("expected yaml parse validation error for missing ui.web.auth")
	}
}

// TestResolveRuntimeUserConfigFilePathLinuxXDG 验证 Linux 下优先使用 XDG_CONFIG_HOME。
func TestResolveRuntimeUserConfigFilePathLinuxXDG(testingObject *testing.T) {
	configFilePath, err := resolveRuntimeUserConfigFilePath(
		"linux",
		func(key string) (string, bool) {
			if key == "XDG_CONFIG_HOME" {
				return "/tmp/devbridge-xdg", true
			}
			return "", false
		},
		"/home/demo",
	)
	if err != nil {
		testingObject.Fatalf("resolve runtime user config file path failed: %v", err)
	}
	if configFilePath != "/tmp/devbridge-xdg/devbridge/agent.yaml" {
		testingObject.Fatalf("unexpected linux user config path: got=%s", configFilePath)
	}
}

// TestResolveRuntimeUserConfigFilePathWindowsKnownFolder 验证 Windows 下优先使用 APPDATA。
func TestResolveRuntimeUserConfigFilePathWindowsKnownFolder(testingObject *testing.T) {
	configFilePath, err := resolveRuntimeUserConfigFilePath(
		"windows",
		func(key string) (string, bool) {
			if key == "APPDATA" {
				return `C:\Users\demo\AppData\Roaming`, true
			}
			return "", false
		},
		`C:\Users\demo`,
	)
	if err != nil {
		testingObject.Fatalf("resolve runtime user config file path failed: %v", err)
	}
	if configFilePath != `C:\Users\demo\AppData\Roaming\DevBridge\agent.yaml` {
		testingObject.Fatalf("unexpected windows user config path: got=%s", configFilePath)
	}
}

// TestLoadRuntimeConfigAppliesExplicitOverridesBeforeEnvAndUser 验证 agent-core 运行配置优先级与 Bridge 保持一致。
func TestLoadRuntimeConfigAppliesExplicitOverridesBeforeEnvAndUser(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "agent.yaml")

	writeConfigTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`agent_id: agent-explicit
bridge_addr: 127.0.0.1:49081
bridge_transport: tcp_framed
session:
  auth_method: token
  auth_token: explicit-token
observability:
  log_level: warn
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:49082
    auth:
      username: explicit-admin
      password: explicit-pass
`),
	)
	writeConfigTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`agent_id: agent-user
bridge_addr: 127.0.0.1:59081
session:
  auth_method: token
  auth_token: user-token
observability:
  log_level: debug
ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:59082
    auth:
      username: user-admin
      password: user-pass
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)
	testingObject.Setenv("DEV_AGENT_CFG_AGENT_ID", "agent-env")

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	if config.AgentID != "agent-explicit" {
		testingObject.Fatalf("unexpected agent_id: got=%s want=agent-explicit", config.AgentID)
	}
	if config.Observability.LogLevel != "warn" {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=warn", config.Observability.LogLevel)
	}
	if config.UI.Web.Auth.Username != "explicit-admin" {
		testingObject.Fatalf("unexpected ui.web.auth.username: got=%s want=explicit-admin", config.UI.Web.Auth.Username)
	}
	if config.RuntimeConfigFilePath != userConfigFilePath {
		testingObject.Fatalf("unexpected runtime config file path: got=%s want=%s", config.RuntimeConfigFilePath, userConfigFilePath)
	}
	if config.RuntimeBaseConfigFilePath != baseConfigFilePath {
		testingObject.Fatalf("unexpected runtime base config path: got=%s want=%s", config.RuntimeBaseConfigFilePath, baseConfigFilePath)
	}
}

// TestLoadRuntimeConfigUsesLocalConfigWhenNoExplicitPath 验证未显式指定配置文件时会自动加载当前工作目录 agent.yaml。
func TestLoadRuntimeConfigUsesLocalConfigWhenNoExplicitPath(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	localConfigFilePath := filepath.Join(tempDir, "agent.yaml")
	writeConfigTestFile(
		testingObject,
		localConfigFilePath,
		[]byte(`agent_id: agent-local
bridge_addr: 127.0.0.1:69081
bridge_transport: tcp_framed
session:
  auth_method: token
  auth_token: local-token
`),
	)

	workingDirectory, err := os.Getwd()
	if err != nil {
		testingObject.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		testingObject.Fatalf("chdir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(workingDirectory)
	}()

	config, err := LoadRuntimeConfig("")
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	if config.AgentID != "agent-local" {
		testingObject.Fatalf("unexpected agent_id: got=%s want=agent-local", config.AgentID)
	}
	if config.RuntimeBaseConfigFilePath != localConfigFilePath {
		testingObject.Fatalf("unexpected runtime base config path: got=%s want=%s", config.RuntimeBaseConfigFilePath, localConfigFilePath)
	}
}

// TestBuildRuntimeConfigFromLayerMapsAppliesExplicitLocalEnvPriority 验证层级合并顺序为 explicit > env > local > user > system > default。
func TestBuildRuntimeConfigFromLayerMapsAppliesExplicitLocalEnvPriority(testingObject *testing.T) {
	testingObject.Setenv("DEV_AGENT_CFG_AGENT_ID", "agent-env")

	config, err := buildRuntimeConfigFromLayerMaps(runtimeConfigLayerMaps{
		systemConfigFilePath: "/etc/devbridge/agent.yaml",
		systemLayer: map[string]any{
			"agent_id":    "agent-system",
			"bridge_addr": "127.0.0.1:39081",
			"session": map[string]any{
				"auth_method": "token",
				"auth_token":  "system-token",
			},
		},
		userConfigFilePath: "/home/demo/.config/devbridge/agent.yaml",
		userLayer: map[string]any{
			"bridge_addr": "127.0.0.1:49081",
			"ui": map[string]any{
				"web": map[string]any{
					"enabled":     true,
					"listen_addr": "127.0.0.1:49082",
					"auth": map[string]any{
						"username": "user-admin",
						"password": "user-pass",
					},
				},
			},
		},
		localConfigFilePath: "/srv/devbridge/agent.yaml",
		localLayer: map[string]any{
			"bridge_addr": "127.0.0.1:59081",
		},
		explicitConfigFilePath: "/tmp/explicit-agent.yaml",
		explicitLayer: map[string]any{
			"agent_id": "agent-explicit",
		},
	})
	if err != nil {
		testingObject.Fatalf("build runtime config from layer maps failed: %v", err)
	}

	if config.AgentID != "agent-explicit" {
		testingObject.Fatalf("unexpected agent_id: got=%s want=agent-explicit", config.AgentID)
	}
	if config.BridgeAddr != "127.0.0.1:59081" {
		testingObject.Fatalf("unexpected bridge_addr: got=%s want=127.0.0.1:59081", config.BridgeAddr)
	}
	if !config.UI.Web.Enabled {
		testingObject.Fatalf("expected web ui enabled from user layer")
	}
	if config.UI.Web.Auth.Username != "user-admin" {
		testingObject.Fatalf("unexpected ui.web.auth.username: got=%s want=user-admin", config.UI.Web.Auth.Username)
	}
	if config.RuntimeBaseConfigFilePath != "/tmp/explicit-agent.yaml" {
		testingObject.Fatalf("unexpected runtime base config path: got=%s want=/tmp/explicit-agent.yaml", config.RuntimeBaseConfigFilePath)
	}
}

// TestSaveConfigToYAMLFileCreatesParentDir 验证配置保存使用与 Bridge 一致的原子落盘方式。
func TestSaveConfigToYAMLFileCreatesParentDir(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	configFilePath := filepath.Join(testingObject.TempDir(), "nested", "agent.yaml")
	if err := SaveConfigToYAMLFile(config, configFilePath); err != nil {
		testingObject.Fatalf("save config to yaml failed: %v", err)
	}
	savedConfig, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if !savedConfig.UI.Web.Enabled {
		testingObject.Fatalf("expected saved web ui enabled")
	}
}

func TestWriteRuntimeConfigBytesToFileKeepsPreviousFileWhenReplaceFails(testingObject *testing.T) {
	configFilePath := filepath.Join(testingObject.TempDir(), "agent.yaml")
	originalContent := []byte("agent_id: agent-before\n")
	if err := os.WriteFile(configFilePath, originalContent, 0o600); err != nil {
		testingObject.Fatalf("write original runtime config failed: %v", err)
	}

	originalReplaceFile := runtimeConfigReplaceFile
	runtimeConfigReplaceFile = func(oldPath string, newPath string) error {
		return errors.New("replace failed")
	}
	defer func() {
		runtimeConfigReplaceFile = originalReplaceFile
	}()

	if err := writeRuntimeConfigBytesToFile([]byte("agent_id: agent-after\n"), configFilePath); err == nil {
		testingObject.Fatalf("expected runtime config write to fail when replace fails")
	}

	currentContent, err := os.ReadFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("read runtime config after failed replace failed: %v", err)
	}
	if string(currentContent) != string(originalContent) {
		testingObject.Fatalf("expected runtime config file to remain unchanged after failed replace")
	}
}

func writeConfigTestFile(testingObject *testing.T, filePath string, content []byte) {
	testingObject.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		testingObject.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		testingObject.Fatalf("write file failed: %v", err)
	}
}
