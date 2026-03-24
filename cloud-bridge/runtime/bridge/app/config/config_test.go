package config

import "testing"

// TestDefaultConfigSetsQUICListenAddr 验证默认配置会提供 QUIC 控制面监听地址。
func TestDefaultConfigSetsQUICListenAddr(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	if defaultConfig.ControlPlane.QUICListenAddr == "" {
		testingObject.Fatalf("expected default quic listen addr to be set")
	}
}

// TestValidateRejectsEmptyQUICListenAddr 验证 QUIC 控制面监听地址不能为空。
func TestValidateRejectsEmptyQUICListenAddr(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.ControlPlane.QUICListenAddr = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty quic listen addr")
	}
}

// TestValidateRejectsQUICListenAddrCollision 验证 QUIC 控制面监听地址不能与现有控制面端口冲突。
func TestValidateRejectsQUICListenAddrCollision(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.ControlPlane.QUICListenAddr = config.ControlPlane.ListenAddr
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for quic listen addr collision")
	}
}
