package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	"gopkg.in/yaml.v3"
)

func TestAdminRuntimeConfigStoreUpdatePersistsUserOverridesAndKeepsEnvPriority(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "bridge.yaml")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`ingress:
  http_addr: ":18080"
observability:
  log_level: warn
control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
`),
	)
	writeTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`admin:
  base_path: "/console"
observability:
  log_level: debug
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)
	testingObject.Setenv("DEV_BRIDGE_CFG_OBSERVABILITY_LOG_LEVEL", "error")

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err == nil {
	} else {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	updateResult, err := store.update(
		time.Unix(1700000000, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"observability.log_level": "trace",
				"default_scope.namespace": "tenant",
			},
		},
		"admin-user",
	)
	if err == nil {
	} else {
		testingObject.Fatalf("update config failed: %v", err)
	}

	observabilitySnapshot, ok := updateResult.Snapshot["observability"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected observability snapshot object")
	}
	if observabilitySnapshot["log_level"] == "error" {
	} else {
		testingObject.Fatalf("unexpected effective log level after env override: got=%v want=error", observabilitySnapshot["log_level"])
	}

	fieldSources, ok := updateResult.Snapshot["field_sources"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_sources map in snapshot")
	}
	if fieldSources["observability.log_level"] == "env" {
	} else {
		testingObject.Fatalf("unexpected log level source: got=%v want=env", fieldSources["observability.log_level"])
	}
	if fieldSources["ingress.http_addr"] == "system" {
	} else {
		testingObject.Fatalf("unexpected ingress.http_addr source: got=%v want=system", fieldSources["ingress.http_addr"])
	}
	if fieldSources["default_scope.namespace"] == "user" {
	} else {
		testingObject.Fatalf("unexpected default_scope.namespace source: got=%v want=user", fieldSources["default_scope.namespace"])
	}
	if updateResult.Snapshot["config_file_path"] == userConfigFilePath {
	} else {
		testingObject.Fatalf("unexpected config_file_path: got=%v want=%s", updateResult.Snapshot["config_file_path"], userConfigFilePath)
	}

	persistedUserConfig := loadYAMLRecord(testingObject, userConfigFilePath)
	if readYAMLPath(persistedUserConfig, "admin.base_path") == "/console" {
	} else {
		testingObject.Fatalf("expected existing user config preserved, got=%v", readYAMLPath(persistedUserConfig, "admin.base_path"))
	}
	if readYAMLPath(persistedUserConfig, "observability.log_level") == "trace" {
	} else {
		testingObject.Fatalf("expected user override persisted, got=%v", readYAMLPath(persistedUserConfig, "observability.log_level"))
	}
	if readYAMLPath(persistedUserConfig, "default_scope.namespace") == "tenant" {
	} else {
		testingObject.Fatalf("expected default_scope.namespace persisted, got=%v", readYAMLPath(persistedUserConfig, "default_scope.namespace"))
	}
	if readYAMLPath(persistedUserConfig, "ingress.http_addr") == nil {
	} else {
		testingObject.Fatalf("expected inherited ingress.http_addr not written to user override file, got=%v", readYAMLPath(persistedUserConfig, "ingress.http_addr"))
	}
}

func TestAdminRuntimeConfigStoreUpdateNullPatchRemovesUserOverrideAndExposesEditablePatch(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "bridge.yaml")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`admin:
  enabled: true
  base_path: "/system-console"
control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
`),
	)
	writeTestFile(
		testingObject,
		userConfigFilePath,
		[]byte(`admin:
  base_path: "/user-console"
  ui_enabled: true
  session_cookie_name: "user-session"
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err == nil {
	} else {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	initialSnapshot := store.snapshot()
	restorePreview, ok := initialSnapshot["field_restore_preview"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_restore_preview in snapshot")
	}
	basePathRestorePreview, ok := restorePreview["admin.base_path"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected admin.base_path restore preview in snapshot")
	}
	if basePathRestorePreview["source"] == "system" {
	} else {
		testingObject.Fatalf("unexpected restore preview source for admin.base_path: got=%v want=system", basePathRestorePreview["source"])
	}
	if basePathRestorePreview["value"] == "/system-console" {
	} else {
		testingObject.Fatalf("unexpected restore preview value for admin.base_path: got=%v want=/system-console", basePathRestorePreview["value"])
	}
	uiEnabledRestorePreview, ok := restorePreview["admin.ui_enabled"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected admin.ui_enabled restore preview in snapshot")
	}
	if uiEnabledRestorePreview["source"] == "default" {
	} else {
		testingObject.Fatalf("unexpected restore preview source for admin.ui_enabled: got=%v want=default", uiEnabledRestorePreview["source"])
	}
	if uiEnabledRestorePreview["value"] == true {
	} else {
		testingObject.Fatalf("unexpected restore preview value for admin.ui_enabled: got=%v want=true", uiEnabledRestorePreview["value"])
	}
	editableUserPatch, ok := initialSnapshot["editable_user_patch"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected editable_user_patch in snapshot")
	}
	if readYAMLPath(editableUserPatch, "admin.base_path") == "/user-console" {
	} else {
		testingObject.Fatalf("expected editable user patch to expose admin.base_path, got=%v", readYAMLPath(editableUserPatch, "admin.base_path"))
	}
	if readYAMLPath(editableUserPatch, "admin.ui_enabled") == true {
	} else {
		testingObject.Fatalf("expected editable user patch to expose admin.ui_enabled, got=%v", readYAMLPath(editableUserPatch, "admin.ui_enabled"))
	}
	if readYAMLPath(editableUserPatch, "admin.session_cookie_name") == nil {
	} else {
		testingObject.Fatalf("expected editable user patch to filter unsupported field, got=%v", readYAMLPath(editableUserPatch, "admin.session_cookie_name"))
	}

	updateResult, err := store.update(
		time.Unix(1700000100, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"admin.base_path": nil,
			},
		},
		"admin-user",
	)
	if err == nil {
	} else {
		testingObject.Fatalf("update config failed: %v", err)
	}

	adminSnapshot, ok := updateResult.Snapshot["admin"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected admin snapshot object")
	}
	if adminSnapshot["base_path"] == "/system-console" {
	} else {
		testingObject.Fatalf("unexpected admin.base_path after removing override: got=%v want=/system-console", adminSnapshot["base_path"])
	}

	fieldSources, ok := updateResult.Snapshot["field_sources"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_sources map in snapshot")
	}
	if fieldSources["admin.base_path"] == "system" {
	} else {
		testingObject.Fatalf("unexpected admin.base_path source: got=%v want=system", fieldSources["admin.base_path"])
	}

	restorePreviewAfter, ok := updateResult.Snapshot["field_restore_preview"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_restore_preview after update")
	}
	if _, exists := restorePreviewAfter["admin.base_path"]; exists == true {
		testingObject.Fatalf("expected admin.base_path restore preview to be removed after deleting override")
	}
	uiEnabledRestorePreviewAfter, ok := restorePreviewAfter["admin.ui_enabled"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected admin.ui_enabled restore preview after update")
	}
	if uiEnabledRestorePreviewAfter["source"] == "default" {
	} else {
		testingObject.Fatalf("unexpected restore preview source for admin.ui_enabled after update: got=%v want=default", uiEnabledRestorePreviewAfter["source"])
	}
	editableUserPatchAfter, ok := updateResult.Snapshot["editable_user_patch"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected editable_user_patch after update")
	}
	if readYAMLPath(editableUserPatchAfter, "admin.base_path") == nil {
	} else {
		testingObject.Fatalf("expected editable user patch to remove admin.base_path, got=%v", readYAMLPath(editableUserPatchAfter, "admin.base_path"))
	}
	if readYAMLPath(editableUserPatchAfter, "admin.ui_enabled") == true {
	} else {
		testingObject.Fatalf("expected editable user patch to preserve admin.ui_enabled, got=%v", readYAMLPath(editableUserPatchAfter, "admin.ui_enabled"))
	}
	if readYAMLPath(editableUserPatchAfter, "admin.session_cookie_name") == nil {
	} else {
		testingObject.Fatalf("expected editable user patch to keep filtering unsupported field, got=%v", readYAMLPath(editableUserPatchAfter, "admin.session_cookie_name"))
	}

	persistedUserConfig := loadYAMLRecord(testingObject, userConfigFilePath)
	if readYAMLPath(persistedUserConfig, "admin.base_path") == nil {
	} else {
		testingObject.Fatalf("expected persisted user config to remove admin.base_path, got=%v", readYAMLPath(persistedUserConfig, "admin.base_path"))
	}
	if readYAMLPath(persistedUserConfig, "admin.ui_enabled") == true {
	} else {
		testingObject.Fatalf("expected persisted user config to preserve admin.ui_enabled, got=%v", readYAMLPath(persistedUserConfig, "admin.ui_enabled"))
	}
	if readYAMLPath(persistedUserConfig, "admin.session_cookie_name") == "user-session" {
	} else {
		testingObject.Fatalf("expected persisted user config to preserve unsupported admin.session_cookie_name, got=%v", readYAMLPath(persistedUserConfig, "admin.session_cookie_name"))
	}
}

func TestBuildAdminConfigSnapshotIncludesQUICListenAddr(testingObject *testing.T) {
	config := DefaultConfig()
	config.ControlPlane.ListenAddr = ":39080"
	config.ControlPlane.GRPCH2ListenAddr = ":39082"
	config.ControlPlane.QUICListenAddr = ":39083"

	snapshot := buildAdminConfigSnapshot(
		config,
		1,
		time.Unix(1_700_000_000, 0).UTC(),
		"tester",
		map[string]any{},
		map[string]any{},
	)

	controlPlaneSnapshot, ok := snapshot["control_plane"].(map[string]any)
	if !ok {
		testingObject.Fatalf("expected control_plane snapshot object")
	}
	if controlPlaneSnapshot["quic_listen_addr"] != ":39083" {
		testingObject.Fatalf(
			"unexpected control_plane.quic_listen_addr: got=%v want=%s",
			controlPlaneSnapshot["quic_listen_addr"],
			":39083",
		)
	}
}

func loadYAMLRecord(testingObject *testing.T, filePath string) map[string]any {
	testingObject.Helper()
	rawContent, err := os.ReadFile(filePath)
	if err == nil {
	} else {
		testingObject.Fatalf("read yaml file failed: %v", err)
	}
	parsed := map[string]any{}
	if err := yaml.Unmarshal(rawContent, &parsed); err == nil {
	} else {
		testingObject.Fatalf("decode yaml failed: %v", err)
	}
	return parsed
}

func readYAMLPath(record map[string]any, dottedPath string) any {
	segments := strings.Split(dottedPath, ".")
	var current any = record
	for _, segment := range segments {
		currentRecord, ok := current.(map[string]any)
		if ok == true {
		} else {
			return nil
		}
		nextValue, exists := currentRecord[segment]
		if exists == true {
		} else {
			return nil
		}
		current = nextValue
	}
	return current
}
