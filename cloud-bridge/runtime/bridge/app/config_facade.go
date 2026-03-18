package app

import appconfig "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/app/config"

// Config 对外保持 Bridge 运行配置类型别名，兼容现有 app 层调用方。
type Config = appconfig.Config

// IngressConfig 对外保持入站监听配置类型别名。
type IngressConfig = appconfig.IngressConfig

// AdminConfig 对外保持管理面配置类型别名。
type AdminConfig = appconfig.AdminConfig

// AdminAuthTokenConfig 对外保持管理面静态 Token 配置类型别名。
type AdminAuthTokenConfig = appconfig.AdminAuthTokenConfig

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
