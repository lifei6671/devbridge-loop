package app

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestDefaultConfigTunnelPoolValues 验证 tunnelPool 默认值保持文档约定不变。
func TestDefaultConfigTunnelPoolValues(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	if defaultConfig.BridgeTransport != transport.BindingTypeTCPFramed.String() {
		testingObject.Fatalf(
			"unexpected default bridge transport: %s",
			defaultConfig.BridgeTransport,
		)
	}
	if defaultConfig.TunnelPool.MinIdle != 8 {
		testingObject.Fatalf("unexpected default min_idle: %d", defaultConfig.TunnelPool.MinIdle)
	}
	if defaultConfig.TunnelPool.MaxIdle != 32 {
		testingObject.Fatalf("unexpected default max_idle: %d", defaultConfig.TunnelPool.MaxIdle)
	}
	if defaultConfig.TunnelPool.OpenRate != 10 {
		testingObject.Fatalf("unexpected default open_rate: %v", defaultConfig.TunnelPool.OpenRate)
	}
	if defaultConfig.TunnelPool.OpenBurst != 20 {
		testingObject.Fatalf("unexpected default open_burst: %d", defaultConfig.TunnelPool.OpenBurst)
	}
}

// TestValidateRejectsUnknownBridgeTransport 验证非法 bridge_transport 会被拒绝。
func TestValidateRejectsUnknownBridgeTransport(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.BridgeTransport = "custom_binding_x"
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for unknown bridge_transport")
	}
}

// TestValidateRejectsUnwiredBridgeTransport 验证已定义但尚未接线的 transport 会被拒绝。
func TestValidateRejectsUnwiredBridgeTransport(testingObject *testing.T) {
	testingObject.Parallel()
	testCases := []string{
		transport.BindingTypeQUICNative.String(),
		transport.BindingTypeH3Stream.String(),
	}
	for _, bridgeTransport := range testCases {
		config := DefaultConfig()
		config.BridgeTransport = bridgeTransport
		if err := config.Validate(); err == nil {
			testingObject.Fatalf("expected validate error for unwired bridge_transport=%s", bridgeTransport)
		}
	}
}

// TestApplyTunnelPoolOverridePartial 验证外部只传部分字段时其余字段保持默认值。
func TestApplyTunnelPoolOverridePartial(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	maxIdle := 64
	ttl := 120 * time.Second
	reconcileGap := 2 * time.Second
	updatedConfig := defaultConfig.ApplyTunnelPoolOverride(TunnelPoolOverride{
		MaxIdle:      &maxIdle,
		TTL:          &ttl,
		ReconcileGap: &reconcileGap,
	})

	// 未覆盖字段保持默认值。
	if updatedConfig.TunnelPool.MinIdle != defaultConfig.TunnelPool.MinIdle {
		testingObject.Fatalf("unexpected min_idle after partial override: %d", updatedConfig.TunnelPool.MinIdle)
	}
	if updatedConfig.TunnelPool.OpenRate != defaultConfig.TunnelPool.OpenRate {
		testingObject.Fatalf("unexpected open_rate after partial override: %v", updatedConfig.TunnelPool.OpenRate)
	}
	if updatedConfig.TunnelPool.OpenBurst != defaultConfig.TunnelPool.OpenBurst {
		testingObject.Fatalf("unexpected open_burst after partial override: %d", updatedConfig.TunnelPool.OpenBurst)
	}

	// 显式覆盖字段生效。
	if updatedConfig.TunnelPool.MaxIdle != maxIdle {
		testingObject.Fatalf("unexpected max_idle after override: %d", updatedConfig.TunnelPool.MaxIdle)
	}
	if updatedConfig.TunnelPool.TTL != ttl {
		testingObject.Fatalf("unexpected ttl after override: %v", updatedConfig.TunnelPool.TTL)
	}
	if updatedConfig.TunnelPool.ReconcileGap != reconcileGap {
		testingObject.Fatalf("unexpected reconcile_gap after override: %v", updatedConfig.TunnelPool.ReconcileGap)
	}
}
