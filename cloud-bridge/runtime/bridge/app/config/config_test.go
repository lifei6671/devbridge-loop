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

// TestDefaultConfigSetsConnectorTokenStoreDefaults 验证默认配置会提供 connector token store 默认值。
func TestDefaultConfigSetsConnectorTokenStoreDefaults(testingObject *testing.T) {
	testingObject.Parallel()
	defaultConfig := DefaultConfig()
	if defaultConfig.ConnectorAuth.TokenStore.Driver != "file" {
		testingObject.Fatalf(
			"unexpected default connector token store driver: got=%s want=%s",
			defaultConfig.ConnectorAuth.TokenStore.Driver,
			"file",
		)
	}
	if defaultConfig.ConnectorAuth.TokenStore.File.Path != "./bridge.tokens.yaml" {
		testingObject.Fatalf(
			"unexpected default connector token store file path: got=%s want=%s",
			defaultConfig.ConnectorAuth.TokenStore.File.Path,
			"./bridge.tokens.yaml",
		)
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

// TestValidateRejectsInvalidConnectorTokenStoreDriver 验证 connector token store driver 只能取受支持的值。
func TestValidateRejectsInvalidConnectorTokenStoreDriver(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.ConnectorAuth.TokenStore.Driver = "invalid"
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for invalid connector token store driver")
	}
}

// TestValidateRejectsEmptyConnectorTokenStoreFilePathWhenDriverFile 验证 file driver 时必须提供路径。
func TestValidateRejectsEmptyConnectorTokenStoreFilePathWhenDriverFile(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.ConnectorAuth.TokenStore.File.Path = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("expected validate error for empty connector token store file path")
	}
}

// TestValidateAllowsMemoryConnectorTokenStoreWithoutFilePath 验证 memory driver 不要求 file.path。
func TestValidateAllowsMemoryConnectorTokenStoreWithoutFilePath(testingObject *testing.T) {
	testingObject.Parallel()
	config := DefaultConfig()
	config.ConnectorAuth.TokenStore.Driver = "memory"
	config.ConnectorAuth.TokenStore.File.Path = ""
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("expected memory driver without file path to pass validate, got=%v", err)
	}
}
