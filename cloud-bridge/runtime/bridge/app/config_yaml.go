package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfigFromYAMLFile 从 YAML 文件加载 Bridge 运行配置，并执行默认值回填与校验。
func LoadConfigFromYAMLFile(configFilePath string) (Config, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return Config{}, fmt.Errorf("load config from yaml: empty file path")
	}
	absoluteConfigFilePath, err := filepath.Abs(normalizedConfigFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("load config from yaml: resolve absolute path failed: %w", err)
	}
	rawConfigData, err := os.ReadFile(normalizedConfigFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("load config from yaml: read file failed: %w", err)
	}
	config, parseErr := ParseConfigYAML(rawConfigData)
	if parseErr != nil {
		return Config{}, parseErr
	}
	config.RuntimeConfigFilePath = absoluteConfigFilePath
	return config, nil
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

type persistedConfigYAML struct {
	Ingress       IngressConfig       `yaml:"ingress"`
	Admin         AdminConfig         `yaml:"admin"`
	Observability ObservabilityConfig `yaml:"observability"`
	ControlPlane  struct {
		ListenAddr       string `yaml:"listen_addr"`
		GRPCH2ListenAddr string `yaml:"grpc_h2_listen_addr"`
		HeartbeatTimeout string `yaml:"heartbeat_timeout"`
	} `yaml:"control_plane"`
}

// SaveConfigToYAMLFile 将配置写回 YAML 文件，用于管理面配置持久化。
func SaveConfigToYAMLFile(config Config, configFilePath string) error {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return fmt.Errorf("save config to yaml: empty file path")
	}
	absoluteConfigFilePath, err := filepath.Abs(normalizedConfigFilePath)
	if err != nil {
		return fmt.Errorf("save config to yaml: resolve absolute path failed: %w", err)
	}
	configToPersist := config
	configToPersist.RuntimeConfigFilePath = ""
	if err := configToPersist.Validate(); err != nil {
		return fmt.Errorf("save config to yaml: invalid config: %w", err)
	}

	persisted := persistedConfigYAML{
		Ingress:       configToPersist.Ingress,
		Admin:         configToPersist.Admin,
		Observability: configToPersist.Observability,
	}
	persisted.ControlPlane.ListenAddr = configToPersist.ControlPlane.ListenAddr
	persisted.ControlPlane.GRPCH2ListenAddr = configToPersist.ControlPlane.GRPCH2ListenAddr
	persisted.ControlPlane.HeartbeatTimeout = configToPersist.ControlPlane.HeartbeatTimeout.String()

	encoded, err := yaml.Marshal(&persisted)
	if err != nil {
		return fmt.Errorf("save config to yaml: encode yaml failed: %w", err)
	}
	configFileDirectory := filepath.Dir(absoluteConfigFilePath)
	if mkdirErr := os.MkdirAll(configFileDirectory, 0o755); mkdirErr != nil {
		return fmt.Errorf("save config to yaml: ensure directory failed: %w", mkdirErr)
	}

	configFileMode := os.FileMode(0o600)
	if stat, statErr := os.Stat(absoluteConfigFilePath); statErr == nil {
		configFileMode = stat.Mode().Perm()
	}

	tempFile, err := os.CreateTemp(configFileDirectory, ".bridge-config-*.tmp")
	if err != nil {
		return fmt.Errorf("save config to yaml: create temp file failed: %w", err)
	}
	tempFilePath := tempFile.Name()
	cleanupTempFile := func() {
		_ = os.Remove(tempFilePath)
	}
	defer cleanupTempFile()
	if chmodErr := tempFile.Chmod(configFileMode); chmodErr != nil {
		_ = tempFile.Close()
		return fmt.Errorf("save config to yaml: chmod temp file failed: %w", chmodErr)
	}
	if _, writeErr := tempFile.Write(encoded); writeErr != nil {
		_ = tempFile.Close()
		return fmt.Errorf("save config to yaml: write temp file failed: %w", writeErr)
	}
	if closeErr := tempFile.Close(); closeErr != nil {
		return fmt.Errorf("save config to yaml: close temp file failed: %w", closeErr)
	}
	if renameErr := os.Rename(tempFilePath, absoluteConfigFilePath); renameErr != nil {
		return fmt.Errorf("save config to yaml: replace target file failed: %w", renameErr)
	}
	return nil
}
