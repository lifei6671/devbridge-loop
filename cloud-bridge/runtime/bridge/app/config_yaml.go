package app

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfigFromYAMLFile 从 YAML 文件加载 Bridge 运行配置，并执行默认值回填与校验。
func LoadConfigFromYAMLFile(configFilePath string) (Config, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return Config{}, fmt.Errorf("load config from yaml: empty file path")
	}
	rawConfigData, err := os.ReadFile(normalizedConfigFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("load config from yaml: read file failed: %w", err)
	}
	return ParseConfigYAML(rawConfigData)
}

// ParseConfigYAML 解析 YAML 文本为 Config，并使用 DefaultConfig 作为缺省值基线。
func ParseConfigYAML(rawConfigData []byte) (Config, error) {
	defaultConfig := DefaultConfig()
	yamlDecoder := yaml.NewDecoder(bytes.NewReader(rawConfigData))
	// 开启严格字段校验，避免拼写错误的配置键被静默忽略。
	yamlDecoder.KnownFields(true)
	if err := yamlDecoder.Decode(&defaultConfig); err != nil {
		return Config{}, fmt.Errorf("parse yaml config: %w", err)
	}
	// 解析后统一走一遍结构化校验，保证运行时语义一致。
	if err := defaultConfig.Validate(); err != nil {
		return Config{}, err
	}
	return defaultConfig, nil
}
