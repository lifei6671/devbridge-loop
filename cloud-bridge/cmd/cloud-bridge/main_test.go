package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app"
)

func TestResolveLoadedRuntimeConfigPathsOmitsMissingFiles(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	if err := os.WriteFile(baseConfigFilePath, []byte("admin:\n  enabled: true\n"), 0o600); err != nil {
		testingObject.Fatalf("write base config failed: %v", err)
	}
	missingUserConfigFilePath := filepath.Join(tempDir, "user.yaml")

	basePath, userPath := resolveLoadedRuntimeConfigPaths(app.Config{
		RuntimeBaseConfigFilePath: baseConfigFilePath,
		RuntimeConfigFilePath:     missingUserConfigFilePath,
	})

	if basePath != baseConfigFilePath {
		testingObject.Fatalf("unexpected base config path: got=%s want=%s", basePath, baseConfigFilePath)
	}
	if userPath != "" {
		testingObject.Fatalf("unexpected user config path: got=%s want empty", userPath)
	}
}

func TestResolveLoadedRuntimeConfigPathsKeepsExistingFiles(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	baseConfigFilePath := filepath.Join(tempDir, "base.yaml")
	userConfigFilePath := filepath.Join(tempDir, "user.yaml")
	if err := os.WriteFile(baseConfigFilePath, []byte("admin:\n  enabled: true\n"), 0o600); err != nil {
		testingObject.Fatalf("write base config failed: %v", err)
	}
	if err := os.WriteFile(userConfigFilePath, []byte("observability:\n  log_level: debug\n"), 0o600); err != nil {
		testingObject.Fatalf("write user config failed: %v", err)
	}

	basePath, userPath := resolveLoadedRuntimeConfigPaths(app.Config{
		RuntimeBaseConfigFilePath: baseConfigFilePath,
		RuntimeConfigFilePath:     userConfigFilePath,
	})

	if basePath != baseConfigFilePath {
		testingObject.Fatalf("unexpected base config path: got=%s want=%s", basePath, baseConfigFilePath)
	}
	if userPath != userConfigFilePath {
		testingObject.Fatalf("unexpected user config path: got=%s want=%s", userPath, userConfigFilePath)
	}
}
