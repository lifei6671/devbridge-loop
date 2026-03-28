package app

import (
	"os"
	"path/filepath"
	"testing"
)

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
	if err == nil {
	} else {
		testingObject.Fatalf("resolve runtime user config file path failed: %v", err)
	}
	if configFilePath == "/tmp/devbridge-xdg/devbridge/bridge.yaml" {
	} else {
		testingObject.Fatalf("unexpected linux user config path: got=%s", configFilePath)
	}
}

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
	if err == nil {
	} else {
		testingObject.Fatalf("resolve runtime user config file path failed: %v", err)
	}
	if configFilePath == `C:\Users\demo\AppData\Roaming\DevBridge\bridge.yaml` {
	} else {
		testingObject.Fatalf("unexpected windows user config path: got=%s", configFilePath)
	}
}

func TestResolveRuntimeSystemConfigFilePathWindowsKnownFolder(testingObject *testing.T) {
	configFilePath, err := resolveRuntimeSystemConfigFilePath("windows", func(key string) (string, bool) {
		if key == "ProgramData" {
			return `C:\ProgramData`, true
		}
		return "", false
	})
	if err == nil {
	} else {
		testingObject.Fatalf("resolve runtime system config file path failed: %v", err)
	}
	if configFilePath == `C:\ProgramData\DevBridge\bridge.yaml` {
	} else {
		testingObject.Fatalf("unexpected windows system config path: got=%s", configFilePath)
	}
}

func TestLoadRuntimeConfigAppliesExplicitOverridesBeforeEnvAndUser(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigHome := filepath.Join(tempDir, "xdg")
	userConfigFilePath := filepath.Join(userConfigHome, "devbridge", "bridge.yaml")

	writeTestFile(
		testingObject,
		baseConfigFilePath,
		[]byte(`ingress:
  http_addr: ":18080"
admin:
  base_path: "/console"
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
		[]byte(`ingress:
  http_addr: ":28080"
default_scope:
  namespace: tenant
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
	if config.Ingress.HTTPAddr == ":18080" {
	} else {
		testingObject.Fatalf("unexpected ingress.http_addr: got=%s want=:18080", config.Ingress.HTTPAddr)
	}
	if config.Admin.BasePath == "/console" {
	} else {
		testingObject.Fatalf("unexpected admin.base_path: got=%s want=/console", config.Admin.BasePath)
	}
	if config.Observability.LogLevel == "warn" {
	} else {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=warn", config.Observability.LogLevel)
	}
	if config.DefaultScope.Namespace == "tenant" {
	} else {
		testingObject.Fatalf("unexpected default_scope.namespace: got=%s want=tenant", config.DefaultScope.Namespace)
	}
	if config.RuntimeConfigFilePath == userConfigFilePath {
	} else {
		testingObject.Fatalf("unexpected runtime config file path: got=%s want=%s", config.RuntimeConfigFilePath, userConfigFilePath)
	}
	if config.RuntimeBaseConfigFilePath == baseConfigFilePath {
	} else {
		testingObject.Fatalf("unexpected runtime base config file path: got=%s want=%s", config.RuntimeBaseConfigFilePath, baseConfigFilePath)
	}
}

func TestBuildRuntimeConfigFromLayerMapsAppliesExplicitLocalEnvPriority(testingObject *testing.T) {
	testingObject.Setenv("DEV_BRIDGE_CFG_OBSERVABILITY_LOG_LEVEL", "error")

	systemLayer := map[string]any{
		"admin": map[string]any{
			"base_path": "/system-console",
		},
		"control_plane": map[string]any{
			"listen_addr":         ":19080",
			"grpc_h2_listen_addr": ":19082",
		},
		"ingress": map[string]any{
			"http_addr": ":18080",
		},
	}
	userLayer := map[string]any{
		"admin": map[string]any{
			"base_path": "/user-console",
		},
		"control_plane": map[string]any{
			"grpc_h2_listen_addr": ":29082",
		},
		"ingress": map[string]any{
			"grpc_addr": ":28081",
		},
	}
	localLayer := map[string]any{
		"control_plane": map[string]any{
			"grpc_h2_listen_addr": ":39082",
		},
	}
	explicitLayer := map[string]any{
		"ingress": map[string]any{
			"http_addr": ":58080",
		},
	}

	config, err := buildRuntimeConfigFromLayerMaps(
		runtimeConfigLayerMaps{
			systemConfigFilePath:   "/etc/devbridge/bridge.yaml",
			systemLayer:            systemLayer,
			userConfigFilePath:     "/home/demo/.config/devbridge/bridge.yaml",
			userLayer:              userLayer,
			localConfigFilePath:    "/srv/devbridge/bridge.yaml",
			localLayer:             localLayer,
			explicitConfigFilePath: "/tmp/explicit-bridge.yaml",
			explicitLayer:          explicitLayer,
		},
	)
	if err != nil {
		testingObject.Fatalf("build runtime config from layer maps failed: %v", err)
	}

	if config.Ingress.HTTPAddr != ":58080" {
		testingObject.Fatalf("unexpected ingress.http_addr: got=%s want=:58080", config.Ingress.HTTPAddr)
	}
	if config.Observability.LogLevel != "error" {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=error", config.Observability.LogLevel)
	}
	if config.ControlPlane.GRPCH2ListenAddr != ":39082" {
		testingObject.Fatalf(
			"unexpected control_plane.grpc_h2_listen_addr: got=%s want=:39082",
			config.ControlPlane.GRPCH2ListenAddr,
		)
	}
	if config.Admin.BasePath != "/user-console" {
		testingObject.Fatalf("unexpected admin.base_path: got=%s want=/user-console", config.Admin.BasePath)
	}
	if config.RuntimeBaseConfigFilePath != "/tmp/explicit-bridge.yaml" {
		testingObject.Fatalf("unexpected runtime base config path: got=%s want=/tmp/explicit-bridge.yaml", config.RuntimeBaseConfigFilePath)
	}
}

func TestBuildRuntimeConfigFieldSourcesTracksExplicitLocalEnvPriority(testingObject *testing.T) {
	testingObject.Setenv("DEV_BRIDGE_CFG_OBSERVABILITY_LOG_LEVEL", "error")

	fieldSources, err := buildRuntimeConfigFieldSources(runtimeConfigLayerMaps{
		systemLayer: map[string]any{
			"control_plane": map[string]any{
				"listen_addr": ":19080",
			},
		},
		userLayer: map[string]any{
			"admin": map[string]any{
				"base_path": "/user-console",
			},
		},
		localLayer: map[string]any{
			"control_plane": map[string]any{
				"grpc_h2_listen_addr": ":39082",
			},
		},
		explicitLayer: map[string]any{
			"ingress": map[string]any{
				"http_addr": ":58080",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("build runtime config field sources failed: %v", err)
	}

	if fieldSources["ingress.http_addr"] != "explicit" {
		testingObject.Fatalf("unexpected ingress.http_addr source: got=%v want=explicit", fieldSources["ingress.http_addr"])
	}
	if fieldSources["observability.log_level"] != "env" {
		testingObject.Fatalf("unexpected observability.log_level source: got=%v want=env", fieldSources["observability.log_level"])
	}
	if fieldSources["control_plane.grpc_h2_listen_addr"] != "local" {
		testingObject.Fatalf(
			"unexpected control_plane.grpc_h2_listen_addr source: got=%v want=local",
			fieldSources["control_plane.grpc_h2_listen_addr"],
		)
	}
	if fieldSources["admin.base_path"] != "user" {
		testingObject.Fatalf("unexpected admin.base_path source: got=%v want=user", fieldSources["admin.base_path"])
	}
	if fieldSources["control_plane.listen_addr"] != "system" {
		testingObject.Fatalf(
			"unexpected control_plane.listen_addr source: got=%v want=system",
			fieldSources["control_plane.listen_addr"],
		)
	}
}

func TestBuildRuntimeConfigFromLayerMapsKeepsLowerLayerTLSEntriesWhenExplicitLayerOmitsThem(testingObject *testing.T) {
	config, err := buildRuntimeConfigFromLayerMaps(runtimeConfigLayerMaps{
		userConfigFilePath: "/home/demo/.config/devbridge/bridge.yaml",
		userLayer: map[string]any{
			"control_plane": map[string]any{
				"tls_mode":               "optional",
				"tls_cert_source":        "managed_ca",
				"tls_ca_cert_file":       "/home/demo/.config/devbridge/root-ca.crt",
				"tls_ca_key_file":        "/home/demo/.config/devbridge/root-ca.key",
				"tls_server_common_name": "localhost",
				"tls_server_san_dns":     []any{"localhost"},
			},
		},
		explicitConfigFilePath: "/tmp/explicit-bridge.yaml",
		explicitLayer: map[string]any{
			"control_plane": map[string]any{
				"listen_addr":         ":19080",
				"grpc_h2_listen_addr": ":19082",
				"heartbeat_timeout":   "30s",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("build runtime config from layer maps failed: %v", err)
	}

	if config.ControlPlane.TLSMode != "optional" {
		testingObject.Fatalf("unexpected tls_mode: got=%s want=optional", config.ControlPlane.TLSMode)
	}
	if config.ControlPlane.TLSCertSource != "managed_ca" {
		testingObject.Fatalf("unexpected tls_cert_source: got=%s want=managed_ca", config.ControlPlane.TLSCertSource)
	}
}

func writeTestFile(testingObject *testing.T, filePath string, content []byte) {
	testingObject.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err == nil {
	} else {
		testingObject.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filePath, content, 0o600); err == nil {
	} else {
		testingObject.Fatalf("write file failed: %v", err)
	}
}
