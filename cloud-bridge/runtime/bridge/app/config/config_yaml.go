package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/internal/fileutil"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
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
	defaultConfig.NormalizeCompatibility()
	// 解析后统一走一遍结构化校验，保证运行时语义一致。
	if err := defaultConfig.Validate(); err != nil {
		return Config{}, err
	}
	return defaultConfig, nil
}

type persistedConfigYAML struct {
	Ingress          IngressConfig            `yaml:"ingress"`
	Admin            AdminConfig              `yaml:"admin"`
	ConnectorAuth    ConnectorAuthConfig      `yaml:"connector_auth"`
	Observability    ObservabilityConfig      `yaml:"observability"`
	DefaultScope     pb.Scope                 `yaml:"default_scope"`
	FallbackPolicies []pb.ScopeFallbackPolicy `yaml:"fallback_policies"`
	ControlPlane     struct {
		ListenAddr               string   `yaml:"listen_addr"`
		GRPCH2ListenAddr         string   `yaml:"grpc_h2_listen_addr"`
		QUICListenAddr           string   `yaml:"quic_listen_addr"`
		HeartbeatTimeout         string   `yaml:"heartbeat_timeout"`
		TLSMode                  string   `yaml:"tls_mode"`
		TLSCertSource            string   `yaml:"tls_cert_source"`
		TLSCertFile              string   `yaml:"tls_cert_file"`
		TLSKeyFile               string   `yaml:"tls_key_file"`
		TLSCACertFile            string   `yaml:"tls_ca_cert_file"`
		TLSCAKeyFile             string   `yaml:"tls_ca_key_file"`
		TLSServerCommonName      string   `yaml:"tls_server_common_name"`
		TLSServerSANDNS          []string `yaml:"tls_server_san_dns"`
		TLSServerSANIPs          []string `yaml:"tls_server_san_ips"`
		TLSServerCertTTL         string   `yaml:"tls_server_cert_ttl"`
		TLSServerCertRenewBefore string   `yaml:"tls_server_cert_renew_before"`
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
	configToPersist.NormalizeCompatibility()
	if err := configToPersist.Validate(); err != nil {
		return fmt.Errorf("save config to yaml: invalid config: %w", err)
	}

	persisted := persistedConfigYAML{
		Ingress:          configToPersist.Ingress,
		Admin:            configToPersist.Admin,
		ConnectorAuth:    configToPersist.ConnectorAuth,
		Observability:    configToPersist.Observability,
		DefaultScope:     configToPersist.DefaultScope,
		FallbackPolicies: append([]pb.ScopeFallbackPolicy(nil), configToPersist.FallbackPolicies...),
	}
	persisted.ControlPlane.ListenAddr = configToPersist.ControlPlane.ListenAddr
	persisted.ControlPlane.GRPCH2ListenAddr = configToPersist.ControlPlane.GRPCH2ListenAddr
	persisted.ControlPlane.QUICListenAddr = configToPersist.ControlPlane.QUICListenAddr
	persisted.ControlPlane.HeartbeatTimeout = configToPersist.ControlPlane.HeartbeatTimeout.String()
	persisted.ControlPlane.TLSMode = configToPersist.ControlPlane.TLSMode
	persisted.ControlPlane.TLSCertSource = configToPersist.ControlPlane.TLSCertSource
	persisted.ControlPlane.TLSCertFile = configToPersist.ControlPlane.TLSCertFile
	persisted.ControlPlane.TLSKeyFile = configToPersist.ControlPlane.TLSKeyFile
	persisted.ControlPlane.TLSCACertFile = configToPersist.ControlPlane.TLSCACertFile
	persisted.ControlPlane.TLSCAKeyFile = configToPersist.ControlPlane.TLSCAKeyFile
	persisted.ControlPlane.TLSServerCommonName = configToPersist.ControlPlane.TLSServerCommonName
	persisted.ControlPlane.TLSServerSANDNS = append([]string(nil), configToPersist.ControlPlane.TLSServerSANDNS...)
	persisted.ControlPlane.TLSServerSANIPs = append([]string(nil), configToPersist.ControlPlane.TLSServerSANIPs...)
	persisted.ControlPlane.TLSServerCertTTL = configToPersist.ControlPlane.TLSServerCertTTL.String()
	persisted.ControlPlane.TLSServerCertRenewBefore = configToPersist.ControlPlane.TLSServerCertRenewBefore.String()

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
	if renameErr := fileutil.ReplaceFile(tempFilePath, absoluteConfigFilePath); renameErr != nil {
		return fmt.Errorf("save config to yaml: replace target file failed: %w", renameErr)
	}
	return nil
}
