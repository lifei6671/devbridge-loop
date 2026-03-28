package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	"gopkg.in/yaml.v3"
)

func TestAdminRuntimeConfigStoreUpdatePersistsExplicitOverridesAndKeepsEnvPriority(testingObject *testing.T) {
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
	if observabilitySnapshot["log_level"] == "trace" {
	} else {
		testingObject.Fatalf("unexpected effective log level after explicit override: got=%v want=trace", observabilitySnapshot["log_level"])
	}

	fieldSources, ok := updateResult.Snapshot["field_sources"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_sources map in snapshot")
	}
	if fieldSources["observability.log_level"] == "explicit" {
	} else {
		testingObject.Fatalf("unexpected log level source: got=%v want=explicit", fieldSources["observability.log_level"])
	}
	if fieldSources["ingress.http_addr"] == "explicit" {
	} else {
		testingObject.Fatalf("unexpected ingress.http_addr source: got=%v want=explicit", fieldSources["ingress.http_addr"])
	}
	if fieldSources["default_scope.namespace"] == "explicit" {
	} else {
		testingObject.Fatalf("unexpected default_scope.namespace source: got=%v want=explicit", fieldSources["default_scope.namespace"])
	}
	if updateResult.Snapshot["config_file_path"] == baseConfigFilePath {
	} else {
		testingObject.Fatalf("unexpected config_file_path: got=%v want=%s", updateResult.Snapshot["config_file_path"], baseConfigFilePath)
	}
	if updateResult.Snapshot["config_file_source"] == "explicit" {
	} else {
		testingObject.Fatalf("unexpected config_file_source: got=%v want=explicit", updateResult.Snapshot["config_file_source"])
	}

	persistedExplicitConfig := loadYAMLRecord(testingObject, baseConfigFilePath)
	if readYAMLPath(persistedExplicitConfig, "ingress.http_addr") == ":18080" {
	} else {
		testingObject.Fatalf("expected existing explicit config preserved, got=%v", readYAMLPath(persistedExplicitConfig, "ingress.http_addr"))
	}
	if readYAMLPath(persistedExplicitConfig, "observability.log_level") == "trace" {
	} else {
		testingObject.Fatalf("expected explicit override persisted, got=%v", readYAMLPath(persistedExplicitConfig, "observability.log_level"))
	}
	if readYAMLPath(persistedExplicitConfig, "default_scope.namespace") == "tenant" {
	} else {
		testingObject.Fatalf("expected default_scope.namespace persisted, got=%v", readYAMLPath(persistedExplicitConfig, "default_scope.namespace"))
	}
	persistedUserConfig := loadYAMLRecord(testingObject, userConfigFilePath)
	if readYAMLPath(persistedUserConfig, "admin.base_path") == "/console" {
	} else {
		testingObject.Fatalf("expected user config preserved, got=%v", readYAMLPath(persistedUserConfig, "admin.base_path"))
	}
}

func TestAdminRuntimeConfigStoreUpdateNullPatchRemovesEditableUserOverrideAndExposesEditablePatch(testingObject *testing.T) {
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
	config.RuntimeExplicitConfigFilePath = ""
	config.RuntimeBaseConfigFilePath = ""
	config.RuntimeSystemConfigFilePath = baseConfigFilePath
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
	editableUserPatch, ok := initialSnapshot["editable_file_patch"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected editable_file_patch in snapshot")
	}
	if readYAMLPath(editableUserPatch, "admin.base_path") == "/user-console" {
	} else {
		testingObject.Fatalf("expected editable file patch to expose admin.base_path, got=%v", readYAMLPath(editableUserPatch, "admin.base_path"))
	}
	if readYAMLPath(editableUserPatch, "admin.ui_enabled") == true {
	} else {
		testingObject.Fatalf("expected editable file patch to expose admin.ui_enabled, got=%v", readYAMLPath(editableUserPatch, "admin.ui_enabled"))
	}
	if readYAMLPath(editableUserPatch, "admin.session_cookie_name") == nil {
	} else {
		testingObject.Fatalf("expected editable file patch to filter unsupported field, got=%v", readYAMLPath(editableUserPatch, "admin.session_cookie_name"))
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
	editableUserPatchAfter, ok := updateResult.Snapshot["editable_file_patch"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected editable_file_patch after update")
	}
	if readYAMLPath(editableUserPatchAfter, "admin.base_path") == nil {
	} else {
		testingObject.Fatalf("expected editable file patch to remove admin.base_path, got=%v", readYAMLPath(editableUserPatchAfter, "admin.base_path"))
	}
	if readYAMLPath(editableUserPatchAfter, "admin.ui_enabled") == true {
	} else {
		testingObject.Fatalf("expected editable file patch to preserve admin.ui_enabled, got=%v", readYAMLPath(editableUserPatchAfter, "admin.ui_enabled"))
	}
	if readYAMLPath(editableUserPatchAfter, "admin.session_cookie_name") == nil {
	} else {
		testingObject.Fatalf("expected editable file patch to keep filtering unsupported field, got=%v", readYAMLPath(editableUserPatchAfter, "admin.session_cookie_name"))
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
		runtimeConfigLayerMaps{},
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

func TestAdminRuntimeConfigStoreUpdatePersistsControlPlaneQUICAndTLSSettings(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
  heartbeat_timeout: "30s"
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err == nil {
	} else {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	updateResult, err := store.update(
		time.Unix(1700000200, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"control_plane.quic_listen_addr":                ":49083",
				"control_plane.tls_mode":                        "required",
				"control_plane.tls_cert_source":                 "managed_ca",
				"control_plane.tls_ca_cert_file":                "/etc/devbridge/root-ca.crt",
				"control_plane.tls_ca_key_file":                 "/etc/devbridge/root-ca.key",
				"control_plane.tls_server_common_name":          "bridge.dev.local",
				"control_plane.tls_server_san_dns":              "bridge.dev.local,bridge.internal",
				"control_plane.tls_server_san_ips":              "127.0.0.1,10.0.0.5",
				"control_plane.tls_server_cert_ttl_ms":          604800000,
				"control_plane.tls_server_cert_renew_before_ms": 86400000,
			},
		},
		"admin-user",
	)
	if err == nil {
	} else {
		testingObject.Fatalf("update config failed: %v", err)
	}

	controlPlaneSnapshot, ok := updateResult.Snapshot["control_plane"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected control_plane snapshot object")
	}
	if controlPlaneSnapshot["quic_listen_addr"] == ":49083" {
	} else {
		testingObject.Fatalf("unexpected quic_listen_addr: got=%v want=:49083", controlPlaneSnapshot["quic_listen_addr"])
	}
	if controlPlaneSnapshot["tls_mode"] == "required" {
	} else {
		testingObject.Fatalf("unexpected tls_mode: got=%v want=required", controlPlaneSnapshot["tls_mode"])
	}
	if controlPlaneSnapshot["tls_cert_source"] == "managed_ca" {
	} else {
		testingObject.Fatalf("unexpected tls_cert_source: got=%v want=managed_ca", controlPlaneSnapshot["tls_cert_source"])
	}
	if controlPlaneSnapshot["tls_server_common_name"] == "bridge.dev.local" {
	} else {
		testingObject.Fatalf(
			"unexpected tls_server_common_name: got=%v want=bridge.dev.local",
			controlPlaneSnapshot["tls_server_common_name"],
		)
	}
	if controlPlaneSnapshot["tls_server_cert_ttl_ms"] == uint64(604800000) {
	} else {
		testingObject.Fatalf(
			"unexpected tls_server_cert_ttl_ms: got=%v want=%d",
			controlPlaneSnapshot["tls_server_cert_ttl_ms"],
			604800000,
		)
	}
	if controlPlaneSnapshot["tls_server_cert_renew_before_ms"] == uint64(86400000) {
	} else {
		testingObject.Fatalf(
			"unexpected tls_server_cert_renew_before_ms: got=%v want=%d",
			controlPlaneSnapshot["tls_server_cert_renew_before_ms"],
			86400000,
		)
	}
	if strings.Join(readStringListValue(controlPlaneSnapshot["tls_server_san_dns"]), ",") == "bridge.dev.local,bridge.internal" {
	} else {
		testingObject.Fatalf("unexpected tls_server_san_dns: got=%v", controlPlaneSnapshot["tls_server_san_dns"])
	}
	if strings.Join(readStringListValue(controlPlaneSnapshot["tls_server_san_ips"]), ",") == "127.0.0.1,10.0.0.5" {
	} else {
		testingObject.Fatalf("unexpected tls_server_san_ips: got=%v", controlPlaneSnapshot["tls_server_san_ips"])
	}

	fieldSources, ok := updateResult.Snapshot["field_sources"].(map[string]any)
	if ok == true {
	} else {
		testingObject.Fatalf("expected field_sources map in snapshot")
	}
	if fieldSources["control_plane.quic_listen_addr"] == "explicit" {
	} else {
		testingObject.Fatalf(
			"unexpected control_plane.quic_listen_addr source: got=%v want=explicit",
			fieldSources["control_plane.quic_listen_addr"],
		)
	}
	if fieldSources["control_plane.tls_mode"] == "explicit" {
	} else {
		testingObject.Fatalf("unexpected control_plane.tls_mode source: got=%v want=explicit", fieldSources["control_plane.tls_mode"])
	}
	if fieldSources["control_plane.tls_server_cert_ttl_ms"] == "explicit" {
	} else {
		testingObject.Fatalf(
			"unexpected control_plane.tls_server_cert_ttl_ms source: got=%v want=explicit",
			fieldSources["control_plane.tls_server_cert_ttl_ms"],
		)
	}

	persistedExplicitConfig := loadYAMLRecord(testingObject, baseConfigFilePath)
	if readYAMLPath(persistedExplicitConfig, "control_plane.quic_listen_addr") == ":49083" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.quic_listen_addr persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.quic_listen_addr"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_mode") == "required" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_mode persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_mode"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_cert_source") == "managed_ca" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_cert_source persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_cert_source"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_cert_file") == "/etc/devbridge/root-ca.crt" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_ca_cert_file persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_cert_file"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_key_file") == "/etc/devbridge/root-ca.key" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_ca_key_file persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_key_file"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_common_name") == "bridge.dev.local" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_server_common_name persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_common_name"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_cert_ttl") == "168h0m0s" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_server_cert_ttl persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_cert_ttl"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_cert_renew_before") == "24h0m0s" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_server_cert_renew_before persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_cert_renew_before"),
		)
	}
	if strings.Join(readYAMLStringSlice(persistedExplicitConfig, "control_plane.tls_server_san_dns"), ",") == "bridge.dev.local,bridge.internal" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_server_san_dns persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_san_dns"),
		)
	}
	if strings.Join(readYAMLStringSlice(persistedExplicitConfig, "control_plane.tls_server_san_ips"), ",") == "127.0.0.1,10.0.0.5" {
	} else {
		testingObject.Fatalf(
			"expected control_plane.tls_server_san_ips persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_san_ips"),
		)
	}
}

func TestAdminRuntimeConfigStoreUpdateAutoFillsManagedCARequiredFields(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
  heartbeat_timeout: "30s"
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	updateResult, err := store.update(
		time.Unix(1700000300, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"control_plane.tls_mode":        "optional",
				"control_plane.tls_cert_source": "managed_ca",
			},
		},
		"admin-user",
	)
	if err != nil {
		testingObject.Fatalf("update config failed: %v", err)
	}

	controlPlaneSnapshot, ok := updateResult.Snapshot["control_plane"].(map[string]any)
	if !ok {
		testingObject.Fatalf("expected control_plane snapshot object")
	}
	expectedCACertFile := filepath.Join(tempDir, "root-ca.crt")
	expectedCAKeyFile := filepath.Join(tempDir, "root-ca.key")
	if controlPlaneSnapshot["tls_cert_source"] != "managed_ca" {
		testingObject.Fatalf("unexpected tls_cert_source: got=%v want=managed_ca", controlPlaneSnapshot["tls_cert_source"])
	}
	if controlPlaneSnapshot["tls_ca_cert_file"] != expectedCACertFile {
		testingObject.Fatalf(
			"unexpected tls_ca_cert_file: got=%v want=%s",
			controlPlaneSnapshot["tls_ca_cert_file"],
			expectedCACertFile,
		)
	}
	if controlPlaneSnapshot["tls_ca_key_file"] != expectedCAKeyFile {
		testingObject.Fatalf(
			"unexpected tls_ca_key_file: got=%v want=%s",
			controlPlaneSnapshot["tls_ca_key_file"],
			expectedCAKeyFile,
		)
	}
	if controlPlaneSnapshot["tls_server_common_name"] != "localhost" {
		testingObject.Fatalf(
			"unexpected tls_server_common_name: got=%v want=localhost",
			controlPlaneSnapshot["tls_server_common_name"],
		)
	}
	if strings.Join(readStringListValue(controlPlaneSnapshot["tls_server_san_dns"]), ",") != "localhost" {
		testingObject.Fatalf("unexpected tls_server_san_dns: got=%v", controlPlaneSnapshot["tls_server_san_dns"])
	}
	if strings.Join(readStringListValue(controlPlaneSnapshot["tls_server_san_ips"]), ",") != "127.0.0.1" {
		testingObject.Fatalf("unexpected tls_server_san_ips: got=%v", controlPlaneSnapshot["tls_server_san_ips"])
	}

	persistedExplicitConfig := loadYAMLRecord(testingObject, baseConfigFilePath)
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_cert_file") != expectedCACertFile {
		testingObject.Fatalf(
			"expected control_plane.tls_ca_cert_file persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_cert_file"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_key_file") != expectedCAKeyFile {
		testingObject.Fatalf(
			"expected control_plane.tls_ca_key_file persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_ca_key_file"),
		)
	}
	if readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_common_name") != "localhost" {
		testingObject.Fatalf(
			"expected control_plane.tls_server_common_name persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_common_name"),
		)
	}
	if strings.Join(readYAMLStringSlice(persistedExplicitConfig, "control_plane.tls_server_san_dns"), ",") != "localhost" {
		testingObject.Fatalf(
			"expected control_plane.tls_server_san_dns persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_san_dns"),
		)
	}
	if strings.Join(readYAMLStringSlice(persistedExplicitConfig, "control_plane.tls_server_san_ips"), ",") != "127.0.0.1" {
		testingObject.Fatalf(
			"expected control_plane.tls_server_san_ips persisted, got=%v",
			readYAMLPath(persistedExplicitConfig, "control_plane.tls_server_san_ips"),
		)
	}
}

func TestAdminRuntimeConfigStoreUpdateInitializesManagedCARootFiles(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
  heartbeat_timeout: "30s"
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	updateResult, err := store.update(
		time.Unix(1700000400, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"control_plane.tls_mode":        "optional",
				"control_plane.tls_cert_source": "managed_ca",
			},
		},
		"admin-user",
	)
	if err != nil {
		testingObject.Fatalf("update config failed: %v", err)
	}

	expectedCACertFile := filepath.Join(tempDir, "root-ca.crt")
	expectedCAKeyFile := filepath.Join(tempDir, "root-ca.key")
	if updateResult.Snapshot["config_file_path"] != baseConfigFilePath {
		testingObject.Fatalf(
			"unexpected config_file_path in snapshot: got=%v want=%s",
			updateResult.Snapshot["config_file_path"],
			baseConfigFilePath,
		)
	}
	if updateResult.Snapshot["config_file_source"] != "explicit" {
		testingObject.Fatalf(
			"unexpected config_file_source in snapshot: got=%v want=explicit",
			updateResult.Snapshot["config_file_source"],
		)
	}
	controlPlaneSnapshot, ok := updateResult.Snapshot["control_plane"].(map[string]any)
	if !ok {
		testingObject.Fatalf("expected control_plane snapshot object")
	}
	editableUserPatch, ok := updateResult.Snapshot["editable_file_patch"].(map[string]any)
	if !ok {
		testingObject.Fatalf("expected editable_file_patch in snapshot")
	}
	if readYAMLPath(editableUserPatch, "control_plane.tls_mode") != "optional" {
		testingObject.Fatalf(
			"unexpected tls_mode in editable_file_patch: got=%v want=optional",
			readYAMLPath(editableUserPatch, "control_plane.tls_mode"),
		)
	}
	if controlPlaneSnapshot["tls_mode"] != "optional" {
		testingObject.Fatalf("unexpected tls_mode in snapshot: got=%v want=optional", controlPlaneSnapshot["tls_mode"])
	}
	if controlPlaneSnapshot["tls_cert_source"] != "managed_ca" {
		testingObject.Fatalf(
			"unexpected tls_cert_source in snapshot: got=%v want=managed_ca",
			controlPlaneSnapshot["tls_cert_source"],
		)
	}
	if controlPlaneSnapshot["tls_ca_cert_file"] != expectedCACertFile {
		testingObject.Fatalf(
			"unexpected tls_ca_cert_file in snapshot: got=%v want=%s",
			controlPlaneSnapshot["tls_ca_cert_file"],
			expectedCACertFile,
		)
	}
	if controlPlaneSnapshot["tls_ca_key_file"] != expectedCAKeyFile {
		testingObject.Fatalf(
			"unexpected tls_ca_key_file in snapshot: got=%v want=%s",
			controlPlaneSnapshot["tls_ca_key_file"],
			expectedCAKeyFile,
		)
	}
	caCertContent, err := os.ReadFile(expectedCACertFile)
	if err != nil {
		testingObject.Fatalf("read initialized managed ca cert failed: %v", err)
	}
	if len(caCertContent) == 0 {
		testingObject.Fatalf("expected initialized managed ca cert content")
	}
	caKeyContent, err := os.ReadFile(expectedCAKeyFile)
	if err != nil {
		testingObject.Fatalf("read initialized managed ca key failed: %v", err)
	}
	if len(caKeyContent) == 0 {
		testingObject.Fatalf("expected initialized managed ca key content")
	}
}

func TestAdminRuntimeConfigStoreUpdateDoesNotInitializeManagedCARootFilesWhenPersistFails(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
  heartbeat_timeout: "30s"
`),
	)

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)
	store.saveConfigFunc = func(layer map[string]any, configFilePath string) error {
		return fmt.Errorf("inject persist failure for %s", configFilePath)
	}

	expectedCACertFile := filepath.Join(tempDir, "root-ca.crt")
	expectedCAKeyFile := filepath.Join(tempDir, "root-ca.key")
	_, err = store.update(
		time.Unix(1700000400, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"control_plane.tls_mode":        "optional",
				"control_plane.tls_cert_source": "managed_ca",
			},
		},
		"admin-user",
	)
	if err == nil {
		testingObject.Fatalf("expected persist config failure")
	}
	if _, statErr := os.Stat(expectedCACertFile); !os.IsNotExist(statErr) {
		testingObject.Fatalf("expected managed ca cert not created on persist failure, statErr=%v", statErr)
	}
	if _, statErr := os.Stat(expectedCAKeyFile); !os.IsNotExist(statErr) {
		testingObject.Fatalf("expected managed ca key not created on persist failure, statErr=%v", statErr)
	}
}

func TestAdminRuntimeConfigStoreUpdateDoesNotPersistConfigWhenManagedCAInitializationFails(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	initialConfigContent := []byte(`control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
  heartbeat_timeout: "30s"
`)

	writeTestFile(testingObject, baseConfigFilePath, initialConfigContent)
	writeTestFile(testingObject, filepath.Join(tempDir, "root-ca.crt"), []byte("invalid cert pem"))
	writeTestFile(testingObject, filepath.Join(tempDir, "root-ca.key"), []byte("invalid key pem"))

	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	store := newAdminRuntimeConfigStore(config)

	_, err = store.update(
		time.Unix(1700000400, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"control_plane.tls_mode":        "optional",
				"control_plane.tls_cert_source": "managed_ca",
			},
		},
		"admin-user",
	)
	if err == nil {
		testingObject.Fatalf("expected managed ca initialization failure")
	}
	if !strings.Contains(err.Error(), "initialize managed ca root files failed") {
		testingObject.Fatalf("expected managed ca initialization error, got=%v", err)
	}

	persistedConfigContent, err := os.ReadFile(baseConfigFilePath)
	if err != nil {
		testingObject.Fatalf("read config after failed update: %v", err)
	}
	if string(persistedConfigContent) != string(initialConfigContent) {
		testingObject.Fatalf(
			"expected config file unchanged on managed ca initialization failure, got=%s",
			string(persistedConfigContent),
		)
	}
}

func TestAdminRuntimeConfigStoreUpdateFallsBackToUserConfigWhenExplicitConfigIsReadOnly(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	explicitConfigFilePath := filepath.Join(tempDir, "readonly-explicit.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "bridge.yaml")

	writeTestFile(
		testingObject,
		explicitConfigFilePath,
		[]byte(`observability:
  log_level: warn
control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
`),
	)
	if err := os.Chmod(explicitConfigFilePath, 0o400); err != nil {
		testingObject.Fatalf("chmod explicit config read-only failed: %v", err)
	}
	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(explicitConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	config.RuntimeLocalConfigFilePath = ""
	store := newAdminRuntimeConfigStore(config)

	updateResult, err := store.update(
		time.Unix(1700000500, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"admin.base_path": "/console",
			},
		},
		"admin-user",
	)
	if err != nil {
		testingObject.Fatalf("update config failed: %v", err)
	}

	if updateResult.Snapshot["config_file_path"] != userConfigFilePath {
		testingObject.Fatalf(
			"unexpected config_file_path: got=%v want=%s",
			updateResult.Snapshot["config_file_path"],
			userConfigFilePath,
		)
	}
	if updateResult.Snapshot["config_file_source"] != "user" {
		testingObject.Fatalf(
			"unexpected config_file_source: got=%v want=user",
			updateResult.Snapshot["config_file_source"],
		)
	}
	adminSnapshot, ok := updateResult.Snapshot["admin"].(map[string]any)
	if !ok {
		testingObject.Fatalf("expected admin snapshot object")
	}
	if adminSnapshot["base_path"] != "/console" {
		testingObject.Fatalf("unexpected admin.base_path: got=%v want=/console", adminSnapshot["base_path"])
	}

	persistedExplicitConfig := loadYAMLRecord(testingObject, explicitConfigFilePath)
	if readYAMLPath(persistedExplicitConfig, "observability.log_level") != "warn" {
		testingObject.Fatalf(
			"expected read-only explicit config unchanged, got=%v",
			readYAMLPath(persistedExplicitConfig, "observability.log_level"),
		)
	}
	persistedUserConfig := loadYAMLRecord(testingObject, userConfigFilePath)
	if readYAMLPath(persistedUserConfig, "admin.base_path") != "/console" {
		testingObject.Fatalf(
			"expected user config override persisted, got=%v",
			readYAMLPath(persistedUserConfig, "admin.base_path"),
		)
	}
}

func TestAdminRuntimeConfigStoreUpdateRejectsShadowedWriteWhenExplicitConfigIsReadOnly(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	explicitConfigFilePath := filepath.Join(tempDir, "readonly-explicit.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "bridge.yaml")

	writeTestFile(
		testingObject,
		explicitConfigFilePath,
		[]byte(`observability:
  log_level: warn
control_plane:
  listen_addr: ":19080"
  grpc_h2_listen_addr: ":19082"
`),
	)
	if err := os.Chmod(explicitConfigFilePath, 0o400); err != nil {
		testingObject.Fatalf("chmod explicit config read-only failed: %v", err)
	}
	testingObject.Setenv("XDG_CONFIG_HOME", userConfigHome)

	config, err := LoadRuntimeConfig(explicitConfigFilePath)
	if err != nil {
		testingObject.Fatalf("load runtime config failed: %v", err)
	}
	config.RuntimeLocalConfigFilePath = ""
	store := newAdminRuntimeConfigStore(config)

	_, err = store.update(
		time.Unix(1700000600, 0).UTC(),
		adminapi.ConfigUpdateRequest{
			IfMatchVersion: 1,
			Patch: map[string]any{
				"observability.log_level": "debug",
			},
		},
		"admin-user",
	)
	if err == nil {
		testingObject.Fatalf("expected shadowed config update to fail")
	}
	if !errors.Is(err, adminapi.ErrAdminInvalidArgument) {
		testingObject.Fatalf("expected invalid argument error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "higher-priority explicit config") {
		testingObject.Fatalf("expected shadowed config error, got=%v", err)
	}

	persistedExplicitConfig := loadYAMLRecord(testingObject, explicitConfigFilePath)
	if readYAMLPath(persistedExplicitConfig, "observability.log_level") != "warn" {
		testingObject.Fatalf(
			"expected read-only explicit config unchanged, got=%v",
			readYAMLPath(persistedExplicitConfig, "observability.log_level"),
		)
	}
	if _, statErr := os.Stat(userConfigFilePath); !errors.Is(statErr, os.ErrNotExist) {
		testingObject.Fatalf("expected user config file untouched, statErr=%v", statErr)
	}
}

func TestEnsureManagedCARootFilesForConfigCreatesDefaultUserPathFiles(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	editableConfigFilePath := filepath.Join(tempDir, "runtime", "bridge.yaml")
	expectedCACertFile := filepath.Join(tempDir, "runtime", "root-ca.crt")
	expectedCAKeyFile := filepath.Join(tempDir, "runtime", "root-ca.key")

	config := DefaultConfig()
	config.ControlPlane.TLSMode = "optional"
	config.ControlPlane.TLSCertSource = "managed_ca"
	config.ControlPlane.TLSCACertFile = expectedCACertFile
	config.ControlPlane.TLSCAKeyFile = expectedCAKeyFile

	if err := ensureManagedCARootFilesForConfig(config, editableConfigFilePath); err != nil {
		testingObject.Fatalf("ensure managed ca root files failed: %v", err)
	}
	if _, err := os.Stat(expectedCACertFile); err != nil {
		testingObject.Fatalf("expected managed ca cert file created: %v", err)
	}
	if _, err := os.Stat(expectedCAKeyFile); err != nil {
		testingObject.Fatalf("expected managed ca key file created: %v", err)
	}
}

func TestResolveEditableRuntimeConfigTarget(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	localConfigFilePath := filepath.Join(tempDir, "bridge.yaml")
	userConfigFilePath := filepath.Join(tempDir, "xdg", "devbridge", "bridge.yaml")
	systemConfigFilePath := filepath.Join(tempDir, "etc", "devbridge", "bridge.yaml")

	writeYAMLFile := func(filePath string) {
		writeTestFile(testingObject, filePath, []byte("admin:\n  enabled: true\n"))
	}

	testCases := []struct {
		name       string
		prepare    func()
		layerMaps  runtimeConfigLayerMaps
		wantPath   string
		wantSource string
	}{
		{
			name: "explicit path wins even before file exists",
			layerMaps: runtimeConfigLayerMaps{
				explicitConfigFilePath: filepath.Join(tempDir, "explicit.yaml"),
			},
			wantPath:   filepath.Join(tempDir, "explicit.yaml"),
			wantSource: runtimeConfigSourceExplicit,
		},
		{
			name: "local file wins over user and system",
			prepare: func() {
				writeYAMLFile(localConfigFilePath)
				writeYAMLFile(userConfigFilePath)
				writeYAMLFile(systemConfigFilePath)
			},
			layerMaps: runtimeConfigLayerMaps{
				localConfigFilePath:  localConfigFilePath,
				userConfigFilePath:   userConfigFilePath,
				systemConfigFilePath: systemConfigFilePath,
			},
			wantPath:   localConfigFilePath,
			wantSource: runtimeConfigSourceLocal,
		},
		{
			name: "user file wins when local is absent",
			prepare: func() {
				writeYAMLFile(userConfigFilePath)
				writeYAMLFile(systemConfigFilePath)
			},
			layerMaps: runtimeConfigLayerMaps{
				localConfigFilePath:  localConfigFilePath,
				userConfigFilePath:   userConfigFilePath,
				systemConfigFilePath: systemConfigFilePath,
			},
			wantPath:   userConfigFilePath,
			wantSource: runtimeConfigSourceUser,
		},
		{
			name: "user path wins when only system exists",
			prepare: func() {
				writeYAMLFile(systemConfigFilePath)
			},
			layerMaps: runtimeConfigLayerMaps{
				userConfigFilePath:   userConfigFilePath,
				systemConfigFilePath: systemConfigFilePath,
			},
			wantPath:   userConfigFilePath,
			wantSource: runtimeConfigSourceUser,
		},
		{
			name: "user path is fallback when no file exists",
			layerMaps: runtimeConfigLayerMaps{
				userConfigFilePath:   userConfigFilePath,
				systemConfigFilePath: systemConfigFilePath,
			},
			wantPath:   userConfigFilePath,
			wantSource: runtimeConfigSourceUser,
		},
	}

	for _, testCase := range testCases {
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			_ = os.RemoveAll(tempDir)
			if testCase.prepare != nil {
				testCase.prepare()
			}
			target, err := resolveEditableRuntimeConfigTarget(testCase.layerMaps)
			if err != nil {
				testingObject.Fatalf("resolve editable runtime config target failed: %v", err)
			}
			if target.path != testCase.wantPath {
				testingObject.Fatalf("unexpected target path: got=%s want=%s", target.path, testCase.wantPath)
			}
			if target.source != testCase.wantSource {
				testingObject.Fatalf("unexpected target source: got=%s want=%s", target.source, testCase.wantSource)
			}
		})
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

func readYAMLStringSlice(record map[string]any, dottedPath string) []string {
	rawValue := readYAMLPath(record, dottedPath)
	return readStringListValue(rawValue)
}

func readStringListValue(rawValue any) []string {
	switch value := rawValue.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			textValue, ok := item.(string)
			if ok == true {
				result = append(result, textValue)
			}
		}
		return result
	default:
		return nil
	}
}
