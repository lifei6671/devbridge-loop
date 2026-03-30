package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRuntimeConfigStoreSnapshotDoesNotExposeSensitiveSecrets(testingObject *testing.T) {
	config := DefaultConfig()
	config.Session.AuthToken = "secret-token"
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	store := newAgentRuntimeConfigStore(config)
	snapshot := store.snapshot("tcp", "127.0.0.1:0")

	configDocument, ok := snapshot["config"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected config document type: %T", snapshot["config"])
	}
	sessionDocument, ok := configDocument["session"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected session document type: %T", configDocument["session"])
	}
	if authToken, ok := sessionDocument["auth_token"].(string); !ok || authToken != "" {
		testingObject.Fatalf("expected snapshot auth_token to be empty, got=%v", sessionDocument["auth_token"])
	}
	uiDocument, ok := configDocument["ui"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected ui document type: %T", configDocument["ui"])
	}
	webDocument, ok := uiDocument["web"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected web document type: %T", uiDocument["web"])
	}
	authDocument, ok := webDocument["auth"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected auth document type: %T", webDocument["auth"])
	}
	if password, ok := authDocument["password"].(string); !ok || password != "" {
		testingObject.Fatalf("expected snapshot ui.web.auth.password to be empty, got=%v", authDocument["password"])
	}
}

func TestAgentRuntimeConfigStoreUpdatePreservesCachedIPCContextInSnapshot(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigFilePath := filepath.Join(tempDir, "user", "agent.yaml")

	config := DefaultConfig()
	config.RuntimeConfigFilePath = userConfigFilePath
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	store := newAgentRuntimeConfigStore(config)
	cachedSnapshot := store.snapshot("tcp", "127.0.0.1:0")
	if cachedSnapshot["ipc_transport"] != "tcp" {
		testingObject.Fatalf("unexpected cached ipc_transport: got=%v want=tcp", cachedSnapshot["ipc_transport"])
	}
	if cachedSnapshot["ipc_endpoint"] != "127.0.0.1:0" {
		testingObject.Fatalf("unexpected cached ipc_endpoint: got=%v want=127.0.0.1:0", cachedSnapshot["ipc_endpoint"])
	}

	updatedConfig := config
	updatedConfig.AgentID = "agent-updated"

	result, err := store.update(updatedConfig, "admin")
	if err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}
	if result["ipc_transport"] != "tcp" {
		testingObject.Fatalf("unexpected ipc_transport after update: got=%v want=tcp", result["ipc_transport"])
	}
	if result["ipc_endpoint"] != "127.0.0.1:0" {
		testingObject.Fatalf("unexpected ipc_endpoint after update: got=%v want=127.0.0.1:0", result["ipc_endpoint"])
	}
}

func TestAgentRuntimeConfigStoreUpdatePersistsToEditableFile(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigFilePath := filepath.Join(tempDir, "user", "agent.yaml")
	localConfigFilePath := filepath.Join(tempDir, "work", "agent.yaml")

	config := DefaultConfig()
	config.RuntimeConfigFilePath = userConfigFilePath
	config.RuntimeLocalConfigFilePath = localConfigFilePath
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	store := newAgentRuntimeConfigStore(config)
	updatedConfig := config
	updatedConfig.AgentID = "agent-updated"
	updatedConfig.BridgeAddr = "127.0.0.1:49081"

	result, err := store.update(updatedConfig, "admin")
	if err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}
	if result["config_file_path"] != userConfigFilePath {
		testingObject.Fatalf("unexpected config file path: got=%v want=%s", result["config_file_path"], userConfigFilePath)
	}
	savedConfig, err := LoadConfigFromYAMLFile(userConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if savedConfig.AgentID != "agent-updated" {
		testingObject.Fatalf("unexpected saved agent_id: got=%s want=agent-updated", savedConfig.AgentID)
	}
	if _, err := os.Stat(localConfigFilePath); !os.IsNotExist(err) {
		testingObject.Fatalf("expected local config to remain absent, got err=%v", err)
	}
}

func TestAgentRuntimeConfigStoreUpdateDoesNotFlattenInheritedValuesIntoLocalLayer(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "agent.yaml")
	localConfigFilePath := filepath.Join(tempDir, "agent.yaml")

	writeConfigTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`session:
  auth_method: token
  auth_token: user-secret-token
`),
	)
	writeConfigTestFile(
		testingObject,
		localConfigFilePath,
		[]byte(`agent_id: agent-local-override
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
	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig("")
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}

	store := newAgentRuntimeConfigStore(config)
	updatedConfig := config
	updatedConfig.BridgeAddr = "127.0.0.1:49081"

	if _, err := store.update(updatedConfig, "admin"); err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}

	localConfigContent, err := os.ReadFile(localConfigFilePath)
	if err != nil {
		testingObject.Fatalf("read local config failed: %v", err)
	}
	if strings.Contains(string(localConfigContent), "auth_token") {
		testingObject.Fatalf("expected local layer to keep inherited auth_token out of file, got=%s", string(localConfigContent))
	}
	if !strings.Contains(string(localConfigContent), "bridge_addr: 127.0.0.1:49081") {
		testingObject.Fatalf("expected local layer to persist updated bridge_addr, got=%s", string(localConfigContent))
	}
}

func TestAgentRuntimeConfigStoreUpdateKeepsExistingTokenWhenIncomingTokenIsEmpty(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigFilePath := filepath.Join(tempDir, "user", "agent.yaml")
	writeConfigTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`session:
  auth_method: token
  auth_token: persisted-secret
`),
	)

	config := DefaultConfig()
	config.RuntimeConfigFilePath = userConfigFilePath
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"
	config.Session.AuthToken = "persisted-secret"

	store := newAgentRuntimeConfigStore(config)
	updatedConfig := config
	updatedConfig.BridgeAddr = "127.0.0.1:49081"
	updatedConfig.Session.AuthToken = ""

	result, err := store.update(updatedConfig, "admin")
	if err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}
	currentConfig := store.currentConfig()
	if currentConfig.Session.AuthToken != "persisted-secret" {
		testingObject.Fatalf("expected runtime token preserved, got=%q", currentConfig.Session.AuthToken)
	}
	savedConfig, err := LoadConfigFromYAMLFile(userConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if savedConfig.Session.AuthToken != "persisted-secret" {
		testingObject.Fatalf("expected saved token preserved, got=%q", savedConfig.Session.AuthToken)
	}
	configDocument, ok := result["config"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected config document type: %T", result["config"])
	}
	sessionDocument, ok := configDocument["session"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected session document type: %T", configDocument["session"])
	}
	if authToken, ok := sessionDocument["auth_token"].(string); !ok || authToken != "" {
		testingObject.Fatalf("expected result auth_token to remain redacted, got=%v", sessionDocument["auth_token"])
	}
}

func TestAgentRuntimeConfigStoreUpdateKeepsExistingWebPasswordWhenIncomingPasswordIsEmpty(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigFilePath := filepath.Join(tempDir, "user", "agent.yaml")
	writeConfigTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`ui:
  web:
    enabled: true
    listen_addr: 127.0.0.1:39082
    auth:
      username: admin
      password: persisted-password
`),
	)

	config := DefaultConfig()
	config.RuntimeConfigFilePath = userConfigFilePath
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "persisted-password"

	store := newAgentRuntimeConfigStore(config)
	updatedConfig := config
	updatedConfig.BridgeAddr = "127.0.0.1:49081"
	updatedConfig.UI.Web.Auth.Password = ""

	result, err := store.update(updatedConfig, "admin")
	if err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}
	currentConfig := store.currentConfig()
	if currentConfig.UI.Web.Auth.Password != "persisted-password" {
		testingObject.Fatalf("expected runtime web password preserved, got=%q", currentConfig.UI.Web.Auth.Password)
	}
	savedConfig, err := LoadConfigFromYAMLFile(userConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if savedConfig.UI.Web.Auth.Password != "persisted-password" {
		testingObject.Fatalf("expected saved web password preserved, got=%q", savedConfig.UI.Web.Auth.Password)
	}
	configDocument, ok := result["config"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected config document type: %T", result["config"])
	}
	uiDocument, ok := configDocument["ui"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected ui document type: %T", configDocument["ui"])
	}
	webDocument, ok := uiDocument["web"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected web document type: %T", uiDocument["web"])
	}
	authDocument, ok := webDocument["auth"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected auth document type: %T", webDocument["auth"])
	}
	if password, ok := authDocument["password"].(string); !ok || password != "" {
		testingObject.Fatalf("expected result password to remain redacted, got=%v", authDocument["password"])
	}
}

func TestAgentRuntimeConfigStoreUpdateReplacesExistingTokenWhenIncomingTokenIsNonEmpty(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	userConfigFilePath := filepath.Join(tempDir, "user", "agent.yaml")

	config := DefaultConfig()
	config.RuntimeConfigFilePath = userConfigFilePath
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"
	config.Session.AuthToken = "persisted-secret"

	store := newAgentRuntimeConfigStore(config)
	updatedConfig := config
	updatedConfig.Session.AuthToken = "new-secret"

	if _, err := store.update(updatedConfig, "admin"); err != nil {
		testingObject.Fatalf("update config store failed: %v", err)
	}
	currentConfig := store.currentConfig()
	if currentConfig.Session.AuthToken != "new-secret" {
		testingObject.Fatalf("expected runtime token updated, got=%q", currentConfig.Session.AuthToken)
	}
	savedConfig, err := LoadConfigFromYAMLFile(userConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if savedConfig.Session.AuthToken != "new-secret" {
		testingObject.Fatalf("expected saved token updated, got=%q", savedConfig.Session.AuthToken)
	}
}
