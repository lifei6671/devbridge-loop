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
	if defaultConfig.BridgeAddr != "127.0.0.1:39081" {
		testingObject.Fatalf("unexpected default bridge_addr: %s", defaultConfig.BridgeAddr)
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

// TestValidateAcceptsQUICBridgeTransport 验证已接线的 quic_native transport 可通过校验。
func TestValidateAcceptsQUICBridgeTransport(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.BridgeTransport = transport.BindingTypeQUICNative.String()
	config.BridgeTLS.Enabled = true
	config.BridgeTLS.RootCAFile = "testdata/root-ca.pem"
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("expected quic_native bridge_transport to pass validation, got %v", err)
	}
}

// TestValidateRejectsQUICBridgeTransportWithoutTLS 验证 quic_native 必须显式启用 Bridge TLS。
func TestValidateRejectsQUICBridgeTransportWithoutTLS(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.BridgeTransport = transport.BindingTypeQUICNative.String()
	config.BridgeTLS.Enabled = false
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for quic_native without bridge tls")
	}
}

// TestValidateRejectsStillUnwiredBridgeTransport 验证仍未接线的 transport 会被拒绝。
func TestValidateRejectsStillUnwiredBridgeTransport(testingObject *testing.T) {
	testingObject.Parallel()
	testCases := []string{
		transport.BindingTypeH3Stream.String(),
	}
	for _, bridgeTransport := range testCases {
		bridgeTransport := bridgeTransport
		config := DefaultConfig()
		config.BridgeTransport = bridgeTransport
		if err := config.Validate(); err == nil {
			testingObject.Fatalf("expected validate error for unwired bridge_transport=%s", bridgeTransport)
		}
	}
}

// TestValidateRejectsUnsupportedAuthMethod 验证仅允许 token 认证方法。
func TestValidateRejectsUnsupportedAuthMethod(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.Session.AuthMethod = "hmac"
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for unsupported session.auth_method")
	}
}

// TestValidateRejectsEmptyAuthToken 验证 session.auth_token 为空会被拒绝。
func TestValidateRejectsEmptyAuthToken(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.Session.AuthToken = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty session.auth_token")
	}
}

// TestValidateRejectsMissingBridgeTLSRootCA 验证启用 Bridge TLS 时必须显式提供 Root CA 文件。
func TestValidateRejectsMissingBridgeTLSRootCA(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.BridgeTLS.Enabled = true
	config.BridgeTLS.RootCAFile = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty bridge tls root ca")
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

// TestValidateRejectsEnabledWebUIWithoutListenAddr 验证启用 Web UI 后必须显式提供监听地址。
func TestValidateRejectsEnabledWebUIWithoutListenAddr(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = ""
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty ui.web.listen_addr")
	}
}

// TestValidateRejectsEnabledWebUIWithoutAuth 验证启用 Web UI 后必须提供账号密码。
func TestValidateRejectsEnabledWebUIWithoutAuth(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.Auth.Username = "   "
	config.UI.Web.Auth.Password = "   "
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty ui.web.auth credentials")
	}
}
