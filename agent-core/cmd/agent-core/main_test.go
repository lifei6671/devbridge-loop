package main

import (
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
	if !strings.Contains(err.Error(), envBridgeTLSRootCAFile) {
		testingObject.Fatalf("unexpected error for missing bridge tls root ca: %v", err)
	}
}
