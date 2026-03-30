package app

import (
	"context"
	"strings"
	"testing"
)

// TestBootstrapWithOptionsTunnelPoolOverride 验证初始化阶段支持 tunnelPool 参数覆盖。
func TestBootstrapWithOptionsTunnelPoolOverride(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	maxIdle := 40
	openBurst := 30
	runtime, err := BootstrapWithOptions(context.Background(), defaultConfig, BootstrapOptions{
		TunnelPoolOverride: &TunnelPoolOverride{
			MaxIdle:   &maxIdle,
			OpenBurst: &openBurst,
		},
	})
	if err != nil {
		testingObject.Fatalf("bootstrap with options failed: %v", err)
	}

	if runtime.cfg.TunnelPool.MaxIdle != maxIdle {
		testingObject.Fatalf("unexpected max_idle after bootstrap override: %d", runtime.cfg.TunnelPool.MaxIdle)
	}
	if runtime.cfg.TunnelPool.OpenBurst != openBurst {
		testingObject.Fatalf("unexpected open_burst after bootstrap override: %d", runtime.cfg.TunnelPool.OpenBurst)
	}
	// 未覆盖字段保持默认配置。
	if runtime.cfg.TunnelPool.MinIdle != defaultConfig.TunnelPool.MinIdle {
		testingObject.Fatalf("unexpected min_idle after bootstrap override: %d", runtime.cfg.TunnelPool.MinIdle)
	}
	if runtime.cfg.TunnelPool.OpenRate != defaultConfig.TunnelPool.OpenRate {
		testingObject.Fatalf("unexpected open_rate after bootstrap override: %v", runtime.cfg.TunnelPool.OpenRate)
	}
}

// TestBootstrapWithOptionsInvalidTunnelPool 验证非法覆盖参数会在初始化时被校验拒绝。
func TestBootstrapWithOptionsInvalidTunnelPool(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	maxIdle := 4
	minIdle := 8
	_, err := BootstrapWithOptions(context.Background(), defaultConfig, BootstrapOptions{
		TunnelPoolOverride: &TunnelPoolOverride{
			MinIdle: &minIdle,
			MaxIdle: &maxIdle,
		},
	})
	if err == nil {
		testingObject.Fatalf("expected bootstrap validation error for invalid override")
	}
}

// TestResolveRuntimeServersWebOnlySkipsLocalRPC 验证 web-only 模式不会依赖 LocalRPC 环境变量。
func TestResolveRuntimeServersWebOnlySkipsLocalRPC(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	runtime, err := BootstrapWithOptions(context.Background(), config, BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}

	ipcServer, httpServer, err := runtime.resolveRuntimeServers(RunOptions{
		EnableWeb: true,
	})
	if err != nil {
		testingObject.Fatalf("resolve runtime servers failed: %v", err)
	}
	if ipcServer != nil {
		testingObject.Fatalf("expected localrpc server to stay disabled in web-only mode")
	}
	if httpServer == nil {
		testingObject.Fatalf("expected http server in web-only mode")
	}
}

// TestResolveRuntimeServersTauriRequiresIPC 验证 tauri/localrpc 模式下仍要求显式 IPC 启动参数。
func TestResolveRuntimeServersTauriRequiresIPC(testingObject *testing.T) {
	testingObject.Parallel()

	runtime, err := BootstrapWithOptions(context.Background(), DefaultConfig(), BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}

	_, _, err = runtime.resolveRuntimeServers(RunOptions{
		EnableLocalRPC: true,
	})
	if err == nil {
		testingObject.Fatalf("expected localrpc env validation error")
	}
	if !strings.Contains(err.Error(), "DEV_AGENT_IPC_ENDPOINT") {
		testingObject.Fatalf("unexpected localrpc env validation error: %v", err)
	}
}
