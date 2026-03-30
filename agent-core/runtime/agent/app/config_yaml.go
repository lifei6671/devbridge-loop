package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfigFromYAMLFile 从 YAML 文件加载 Agent 运行配置，并执行默认值回填与校验。
func LoadConfigFromYAMLFile(configFilePath string) (Config, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return Config{}, fmt.Errorf("load config from yaml: empty file path")
	}
	absoluteConfigFilePath, err := filepath.Abs(normalizedConfigFilePath)
	if err != nil {
		return Config{}, fmt.Errorf("load config from yaml: resolve absolute path failed: %w", err)
	}
	rawConfigData, err := os.ReadFile(absoluteConfigFilePath)
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
	config := DefaultConfig()
	yamlDecoder := yaml.NewDecoder(bytes.NewReader(rawConfigData))
	yamlDecoder.KnownFields(true)
	if err := yamlDecoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("parse yaml config: %w", err)
	}
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

type persistedConfigYAML struct {
	AgentID         string              `yaml:"agent_id"`
	BridgeAddr      string              `yaml:"bridge_addr"`
	BridgeTransport string              `yaml:"bridge_transport"`
	BridgeTLS       BridgeTLSConfig     `yaml:"bridge_tls"`
	Observability   ObservabilityConfig `yaml:"observability"`
	ControlChannel  struct {
		DialTimeout string `yaml:"dial_timeout"`
	} `yaml:"control_channel"`
	Session struct {
		HeartbeatInterval string `yaml:"heartbeat_interval"`
		AuthTimeout       string `yaml:"auth_timeout"`
		AuthMethod        string `yaml:"auth_method"`
		AuthToken         string `yaml:"auth_token"`
		ClientCapVersion  string `yaml:"client_cap_version"`
	} `yaml:"session"`
	TunnelPool struct {
		MinIdle      int     `yaml:"min_idle"`
		MaxIdle      int     `yaml:"max_idle"`
		MaxInflight  int     `yaml:"max_inflight"`
		TTL          string  `yaml:"ttl"`
		MaxReuse     int     `yaml:"max_reuse"`
		RecycleAckTO string  `yaml:"recycle_ack_timeout"`
		OpenRate     float64 `yaml:"open_rate"`
		OpenBurst    int     `yaml:"open_burst"`
		ReconcileGap string  `yaml:"reconcile_gap"`
	} `yaml:"tunnel_pool"`
	UI LocalUIConfig `yaml:"ui"`
}

func normalizePersistedConfig(config Config) (Config, error) {
	configToPersist := config
	configToPersist.RuntimeConfigFilePath = ""
	configToPersist.RuntimeBaseConfigFilePath = ""
	configToPersist.RuntimeSystemConfigFilePath = ""
	configToPersist.RuntimeLocalConfigFilePath = ""
	configToPersist.RuntimeExplicitConfigFilePath = ""
	configToPersist = configToPersist.Normalize()
	if err := configToPersist.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return configToPersist, nil
}

func buildPersistedConfigYAMLDocument(config Config) (persistedConfigYAML, error) {
	configToPersist, err := normalizePersistedConfig(config)
	if err != nil {
		return persistedConfigYAML{}, err
	}
	persisted := persistedConfigYAML{
		AgentID:         configToPersist.AgentID,
		BridgeAddr:      configToPersist.BridgeAddr,
		BridgeTransport: configToPersist.BridgeTransport,
		BridgeTLS:       configToPersist.BridgeTLS,
		Observability:   configToPersist.Observability,
		UI:              configToPersist.UI,
	}
	persisted.ControlChannel.DialTimeout = configToPersist.ControlChannel.DialTimeout.String()
	persisted.Session.HeartbeatInterval = configToPersist.Session.HeartbeatInterval.String()
	persisted.Session.AuthTimeout = configToPersist.Session.AuthTimeout.String()
	persisted.Session.AuthMethod = configToPersist.Session.AuthMethod
	persisted.Session.AuthToken = configToPersist.Session.AuthToken
	persisted.Session.ClientCapVersion = configToPersist.Session.ClientCapVersion
	persisted.TunnelPool.MinIdle = configToPersist.TunnelPool.MinIdle
	persisted.TunnelPool.MaxIdle = configToPersist.TunnelPool.MaxIdle
	persisted.TunnelPool.MaxInflight = configToPersist.TunnelPool.MaxInflight
	persisted.TunnelPool.TTL = configToPersist.TunnelPool.TTL.String()
	persisted.TunnelPool.MaxReuse = configToPersist.TunnelPool.MaxReuse
	persisted.TunnelPool.RecycleAckTO = configToPersist.TunnelPool.RecycleAckTO.String()
	persisted.TunnelPool.OpenRate = configToPersist.TunnelPool.OpenRate
	persisted.TunnelPool.OpenBurst = configToPersist.TunnelPool.OpenBurst
	persisted.TunnelPool.ReconcileGap = configToPersist.TunnelPool.ReconcileGap.String()
	return persisted, nil
}

func buildPersistedConfigDocumentMap(config Config) (map[string]any, error) {
	persisted, err := buildPersistedConfigYAMLDocument(config)
	if err != nil {
		return nil, err
	}
	encoded, err := yaml.Marshal(&persisted)
	if err != nil {
		return nil, fmt.Errorf("encode persisted config document: %w", err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode persisted config document: %w", err)
	}
	return document, nil
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
	persisted, err := buildPersistedConfigYAMLDocument(config)
	if err != nil {
		return fmt.Errorf("save config to yaml: %w", err)
	}
	encoded, err := yaml.Marshal(&persisted)
	if err != nil {
		return fmt.Errorf("save config to yaml: encode yaml failed: %w", err)
	}
	if err := writeRuntimeConfigBytesToFile(encoded, absoluteConfigFilePath); err != nil {
		return fmt.Errorf("save config to yaml: %w", err)
	}
	return nil
}
