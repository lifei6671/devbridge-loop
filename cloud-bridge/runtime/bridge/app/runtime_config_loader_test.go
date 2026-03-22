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

func TestLoadRuntimeConfigAppliesUserOverridesAndEnvPriority(testingObject *testing.T) {
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
	if config.Ingress.HTTPAddr == ":28080" {
	} else {
		testingObject.Fatalf("unexpected ingress.http_addr: got=%s want=:28080", config.Ingress.HTTPAddr)
	}
	if config.Admin.BasePath == "/console" {
	} else {
		testingObject.Fatalf("unexpected admin.base_path: got=%s want=/console", config.Admin.BasePath)
	}
	if config.Observability.LogLevel == "error" {
	} else {
		testingObject.Fatalf("unexpected observability.log_level: got=%s want=error", config.Observability.LogLevel)
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
