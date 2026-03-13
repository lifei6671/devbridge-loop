package app

import (
	"context"
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
