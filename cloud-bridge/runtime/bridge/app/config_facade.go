package app

import appconfig "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app/config"

// Config 对外保持 Bridge 运行配置类型别名，兼容现有 app 层调用方。
type Config = appconfig.Config

// IngressConfig 对外保持入站监听配置类型别名。
type IngressConfig = appconfig.IngressConfig

// AdminConfig 对外保持管理面配置类型别名。
type AdminConfig = appconfig.AdminConfig

// AdminLegacyAuthTokenConfig 对外保持旧版静态 token 配置类型别名，供升级兼容迁移使用。
type AdminLegacyAuthTokenConfig = appconfig.AdminLegacyAuthTokenConfig

// AdminAuthProviderConfig 对外保持管理面认证 provider 配置类型别名。
type AdminAuthProviderConfig = appconfig.AdminAuthProviderConfig

// AdminPasswordProviderConfig 对外保持本地用户名密码 provider 配置类型别名。
type AdminPasswordProviderConfig = appconfig.AdminPasswordProviderConfig

// AdminPasswordAccountConfig 对外保持本地登录账号配置类型别名。
type AdminPasswordAccountConfig = appconfig.AdminPasswordAccountConfig

// ConnectorAuthConfig 对外保持 connector 认证配置类型别名。
type ConnectorAuthConfig = appconfig.ConnectorAuthConfig

// ConnectorTokenStoreConfig 对外保持 connector token store 配置类型别名。
type ConnectorTokenStoreConfig = appconfig.ConnectorTokenStoreConfig

// ConnectorTokenFileStoreConfig 对外保持 connector token file store 配置类型别名。
type ConnectorTokenFileStoreConfig = appconfig.ConnectorTokenFileStoreConfig

// ObservabilityConfig 对外保持观测配置类型别名。
type ObservabilityConfig = appconfig.ObservabilityConfig

// ControlPlaneConfig 对外保持控制面配置类型别名。
type ControlPlaneConfig = appconfig.ControlPlaneConfig

// TunnelReuseConfig 对外保持 Tunnel 复用配置类型别名。
type TunnelReuseConfig = appconfig.TunnelReuseConfig

// DefaultConfig 返回可运行的默认配置，并复用独立配置包实现。
func DefaultConfig() Config {
	return appconfig.DefaultConfig()
}

// LoadConfigFromYAMLFile 从 YAML 文件加载运行配置。
func LoadConfigFromYAMLFile(configFilePath string) (Config, error) {
	return appconfig.LoadConfigFromYAMLFile(configFilePath)
}

// ParseConfigYAML 解析 YAML 文本为运行配置。
func ParseConfigYAML(rawConfigData []byte) (Config, error) {
	return appconfig.ParseConfigYAML(rawConfigData)
}

// SaveConfigToYAMLFile 将运行配置保存为 YAML。
func SaveConfigToYAMLFile(config Config, configFilePath string) error {
	return appconfig.SaveConfigToYAMLFile(config, configFilePath)
}
