package app

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/internal/fileutil"
	"gopkg.in/yaml.v3"
)

const (
	runtimeDefaultConfigFileName = "bridge.yaml"

	runtimeConfigSourceDefault  = "default"
	runtimeConfigSourceSystem   = "system"
	runtimeConfigSourceUser     = "user"
	runtimeConfigSourceLocal    = "local"
	runtimeConfigSourceEnv      = "env"
	runtimeConfigSourceExplicit = "explicit"

	linuxRuntimeConfigDirName   = "devbridge"
	windowsRuntimeConfigDirName = "DevBridge"

	envIngressHTTPAddr                = "DEV_BRIDGE_CFG_INGRESS_HTTP_ADDR"
	envIngressGRPCAddr                = "DEV_BRIDGE_CFG_INGRESS_GRPC_ADDR"
	envIngressHTTPSAddr               = "DEV_BRIDGE_CFG_INGRESS_HTTPS_ADDR"
	envIngressTLSSNIAddr              = "DEV_BRIDGE_CFG_INGRESS_TLS_SNI_ADDR"
	envIngressTCPPortRange            = "DEV_BRIDGE_CFG_INGRESS_TCP_PORT_RANGE"
	envIngressBaseDomain              = "DEV_BRIDGE_CFG_INGRESS_BASE_DOMAIN"
	envAdminEnabled                   = "DEV_BRIDGE_CFG_ADMIN_ENABLED"
	envAdminListenAddr                = "DEV_BRIDGE_CFG_ADMIN_LISTEN_ADDR"
	envAdminAllowSharedListener       = "DEV_BRIDGE_CFG_ADMIN_ALLOW_SHARED_LISTENER"
	envAdminUIEnabled                 = "DEV_BRIDGE_CFG_ADMIN_UI_ENABLED"
	envAdminBasePath                  = "DEV_BRIDGE_CFG_ADMIN_BASE_PATH"
	envControlPlaneListenAddr         = "DEV_BRIDGE_CFG_CONTROL_PLANE_LISTEN_ADDR"
	envControlPlaneGRPCH2ListenAddr   = "DEV_BRIDGE_CFG_CONTROL_PLANE_GRPC_H2_LISTEN_ADDR"
	envControlPlaneQUICListenAddr     = "DEV_BRIDGE_CFG_CONTROL_PLANE_QUIC_LISTEN_ADDR"
	envControlPlaneHeartbeatTimeout   = "DEV_BRIDGE_CFG_CONTROL_PLANE_HEARTBEAT_TIMEOUT"
	envControlPlaneHeartbeatTimeoutMS = "DEV_BRIDGE_CFG_CONTROL_PLANE_HEARTBEAT_TIMEOUT_MS"
	envObservabilityLogLevel          = "DEV_BRIDGE_CFG_OBSERVABILITY_LOG_LEVEL"
	envObservabilityMetricsAddr       = "DEV_BRIDGE_CFG_OBSERVABILITY_METRICS_ADDR"
	envDefaultScopeNamespace          = "DEV_BRIDGE_CFG_DEFAULT_SCOPE_NAMESPACE"
	envDefaultScopeEnvironment        = "DEV_BRIDGE_CFG_DEFAULT_SCOPE_ENVIRONMENT"

	envControlPlaneTLSMode                  = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_MODE"
	envControlPlaneTLSCertSource            = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CERT_SOURCE"
	envControlPlaneTLSCertFile              = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CERT_FILE"
	envControlPlaneTLSKeyFile               = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_KEY_FILE"
	envControlPlaneTLSCACertFile            = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CA_CERT_FILE"
	envControlPlaneTLSCAKeyFile             = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_CA_KEY_FILE"
	envControlPlaneTLSServerCommonName      = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_COMMON_NAME"
	envControlPlaneTLSServerSANDNS          = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_SAN_DNS"
	envControlPlaneTLSServerSANIPs          = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_SAN_IPS"
	envControlPlaneTLSServerCertTTL         = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_CERT_TTL"
	envControlPlaneTLSServerCertRenewBefore = "DEV_BRIDGE_CFG_CONTROL_PLANE_TLS_SERVER_CERT_RENEW_BEFORE"
)

type envLookupFn func(key string) (string, bool)

type runtimeConfigLayerMaps struct {
	systemConfigFilePath   string
	systemLayer            map[string]any
	userConfigFilePath     string
	userLayer              map[string]any
	localConfigFilePath    string
	localLayer             map[string]any
	explicitConfigFilePath string
	explicitLayer          map[string]any
}

type runtimeConfigEditableTarget struct {
	source string
	layer  map[string]any
	path   string
}

var runtimeConfigFieldYAMLPaths = map[string]string{
	"default_scope.namespace":                       "default_scope.namespace",
	"default_scope.environment":                     "default_scope.environment",
	"ingress.http_addr":                             "ingress.http_addr",
	"ingress.grpc_addr":                             "ingress.grpc_addr",
	"ingress.https_addr":                            "ingress.https_addr",
	"ingress.tls_sni_addr":                          "ingress.tls_sni_addr",
	"ingress.tcp_port_range":                        "ingress.tcp_port_range",
	"ingress.base_domain":                           "ingress.base_domain",
	"admin.enabled":                                 "admin.enabled",
	"admin.listen_addr":                             "admin.listen_addr",
	"admin.allow_shared_listener":                   "admin.allow_shared_listener",
	"admin.base_path":                               "admin.base_path",
	"admin.ui_enabled":                              "admin.ui_enabled",
	"connector_auth.token_store.driver":             "connector_auth.token_store.driver",
	"connector_auth.token_store.file.path":          "connector_auth.token_store.file.path",
	"control_plane.listen_addr":                     "control_plane.listen_addr",
	"control_plane.grpc_h2_listen_addr":             "control_plane.grpc_h2_listen_addr",
	"control_plane.quic_listen_addr":                "control_plane.quic_listen_addr",
	"control_plane.heartbeat_timeout_ms":            "control_plane.heartbeat_timeout",
	"control_plane.tls_mode":                        "control_plane.tls_mode",
	"control_plane.tls_cert_source":                 "control_plane.tls_cert_source",
	"control_plane.tls_cert_file":                   "control_plane.tls_cert_file",
	"control_plane.tls_key_file":                    "control_plane.tls_key_file",
	"control_plane.tls_ca_cert_file":                "control_plane.tls_ca_cert_file",
	"control_plane.tls_ca_key_file":                 "control_plane.tls_ca_key_file",
	"control_plane.tls_server_common_name":          "control_plane.tls_server_common_name",
	"control_plane.tls_server_san_dns":              "control_plane.tls_server_san_dns",
	"control_plane.tls_server_san_ips":              "control_plane.tls_server_san_ips",
	"control_plane.tls_server_cert_ttl_ms":          "control_plane.tls_server_cert_ttl",
	"control_plane.tls_server_cert_renew_before_ms": "control_plane.tls_server_cert_renew_before",
	"observability.log_level":                       "observability.log_level",
	"observability.metrics_addr":                    "observability.metrics_addr",
}

var runtimeConfigEditableFieldYAMLPaths = map[string]string{
	"default_scope.namespace":                       "default_scope.namespace",
	"default_scope.environment":                     "default_scope.environment",
	"ingress.http_addr":                             "ingress.http_addr",
	"ingress.grpc_addr":                             "ingress.grpc_addr",
	"ingress.https_addr":                            "ingress.https_addr",
	"ingress.tls_sni_addr":                          "ingress.tls_sni_addr",
	"ingress.tcp_port_range":                        "ingress.tcp_port_range",
	"ingress.base_domain":                           "ingress.base_domain",
	"admin.enabled":                                 "admin.enabled",
	"admin.listen_addr":                             "admin.listen_addr",
	"admin.allow_shared_listener":                   "admin.allow_shared_listener",
	"admin.base_path":                               "admin.base_path",
	"admin.ui_enabled":                              "admin.ui_enabled",
	"control_plane.listen_addr":                     "control_plane.listen_addr",
	"control_plane.grpc_h2_listen_addr":             "control_plane.grpc_h2_listen_addr",
	"control_plane.quic_listen_addr":                "control_plane.quic_listen_addr",
	"control_plane.heartbeat_timeout_ms":            "control_plane.heartbeat_timeout",
	"control_plane.tls_mode":                        "control_plane.tls_mode",
	"control_plane.tls_cert_source":                 "control_plane.tls_cert_source",
	"control_plane.tls_cert_file":                   "control_plane.tls_cert_file",
	"control_plane.tls_key_file":                    "control_plane.tls_key_file",
	"control_plane.tls_ca_cert_file":                "control_plane.tls_ca_cert_file",
	"control_plane.tls_ca_key_file":                 "control_plane.tls_ca_key_file",
	"control_plane.tls_server_common_name":          "control_plane.tls_server_common_name",
	"control_plane.tls_server_san_dns":              "control_plane.tls_server_san_dns",
	"control_plane.tls_server_san_ips":              "control_plane.tls_server_san_ips",
	"control_plane.tls_server_cert_ttl_ms":          "control_plane.tls_server_cert_ttl",
	"control_plane.tls_server_cert_renew_before_ms": "control_plane.tls_server_cert_renew_before",
	"observability.log_level":                       "observability.log_level",
	"observability.metrics_addr":                    "observability.metrics_addr",
}

// LoadRuntimeConfig 按“显式 -config > 环境变量 > 程序运行目录 > 用户目录 > 系统目录 > 默认值”的顺序构建运行配置。
func LoadRuntimeConfig(explicitBaseConfigFilePath string) (Config, error) {
	homeDir, _ := os.UserHomeDir()
	userConfigFilePath, err := resolveRuntimeUserConfigFilePath(runtime.GOOS, os.LookupEnv, homeDir)
	if err != nil {
		return Config{}, err
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}
	systemConfigFilePath, err := resolveRuntimeSystemConfigFilePath(runtime.GOOS, os.LookupEnv)
	if err != nil {
		return Config{}, err
	}
	localConfigFilePath, err := resolveRuntimeLocalConfigFilePath(workingDirectory)
	if err != nil {
		return Config{}, err
	}
	explicitConfigFilePath, err := resolveRuntimeExplicitConfigFilePath(explicitBaseConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	systemLayer, err := maybeLoadRuntimeConfigLayerMap(systemConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	userLayer, err := maybeLoadRuntimeConfigLayerMap(userConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	localLayer, err := maybeLoadRuntimeConfigLayerMap(localConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	explicitLayer, err := maybeLoadRuntimeConfigLayerMap(explicitConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	return buildRuntimeConfigFromLayerMaps(runtimeConfigLayerMaps{
		systemConfigFilePath:   systemConfigFilePath,
		systemLayer:            systemLayer,
		userConfigFilePath:     userConfigFilePath,
		userLayer:              userLayer,
		localConfigFilePath:    localConfigFilePath,
		localLayer:             localLayer,
		explicitConfigFilePath: explicitConfigFilePath,
		explicitLayer:          explicitLayer,
	})
}

// ApplyRuntimeConfigEnvOverrides 将环境变量覆盖应用到配置副本，并执行 Validate。
func ApplyRuntimeConfigEnvOverrides(runtimeConfig Config) (Config, error) {
	resolvedConfig := runtimeConfig
	if _, err := applyRuntimeConfigEnvOverridesInPlace(&resolvedConfig); err != nil {
		return Config{}, err
	}
	resolvedConfig.NormalizeCompatibility()
	if err := resolvedConfig.Validate(); err != nil {
		return Config{}, err
	}
	return resolvedConfig, nil
}

// ApplyControlPlaneTLSEnvOverrides 处理 control_plane TLS 相关环境变量覆盖。
func ApplyControlPlaneTLSEnvOverrides(runtimeConfig *Config) error {
	return applyControlPlaneTLSEnvOverridesWithTracking(runtimeConfig, nil)
}

func resolveRuntimeUserConfigFilePath(goos string, lookupEnv envLookupFn, homeDir string) (string, error) {
	normalizedHomeDir := strings.TrimSpace(homeDir)
	if goos == "windows" {
		if appDataDir, ok := lookupNonEmptyEnv(lookupEnv, "APPDATA"); ok {
			return joinWindowsPath(appDataDir, windowsRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
		}
		if normalizedHomeDir == "" {
			return "", errors.New("resolve runtime user config path: APPDATA and user home are empty")
		}
		return joinWindowsPath(normalizedHomeDir, "AppData", "Roaming", windowsRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
	}
	if configHome, ok := lookupNonEmptyEnv(lookupEnv, "XDG_CONFIG_HOME"); ok {
		return joinUnixPath(configHome, linuxRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
	}
	if normalizedHomeDir == "" {
		return "", errors.New("resolve runtime user config path: user home is empty")
	}
	return joinUnixPath(normalizedHomeDir, ".config", linuxRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
}

func resolveRuntimeSystemConfigFilePath(goos string, lookupEnv envLookupFn) (string, error) {
	if goos == "windows" {
		if programDataDir, ok := lookupNonEmptyEnv(lookupEnv, "ProgramData"); ok {
			return joinWindowsPath(programDataDir, windowsRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
		}
		if programDataDir, ok := lookupNonEmptyEnv(lookupEnv, "PROGRAMDATA"); ok {
			return joinWindowsPath(programDataDir, windowsRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
		}
		return joinWindowsPath(`C:\ProgramData`, windowsRuntimeConfigDirName, runtimeDefaultConfigFileName), nil
	}
	return "/etc/devbridge/bridge.yaml", nil
}

func resolveRuntimeExplicitConfigFilePath(explicitBaseConfigFilePath string) (string, error) {
	normalizedExplicitBaseConfigFilePath := strings.TrimSpace(explicitBaseConfigFilePath)
	if normalizedExplicitBaseConfigFilePath != "" {
		return filepath.Abs(normalizedExplicitBaseConfigFilePath)
	}
	return "", nil
}

func resolveRuntimeLocalConfigFilePath(workingDirectory string) (string, error) {
	normalizedWorkingDirectory := strings.TrimSpace(workingDirectory)
	if normalizedWorkingDirectory == "" {
		return "", nil
	}
	return filepath.Abs(filepath.Join(normalizedWorkingDirectory, runtimeDefaultConfigFileName))
}

func maybeLoadRuntimeConfigLayerMap(configFilePath string) (map[string]any, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return map[string]any{}, nil
	}
	configFileExists, err := fileExists(normalizedConfigFilePath)
	if err != nil {
		return nil, err
	}
	if configFileExists {
		return loadRuntimeConfigLayerMapFromFile(normalizedConfigFilePath)
	}
	return map[string]any{}, nil
}

func buildRuntimeConfigFromLayerMaps(layerMaps runtimeConfigLayerMaps) (Config, error) {
	resolvedConfig := DefaultConfig()
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, layerMaps.systemLayer); err != nil {
		return Config{}, err
	}
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, layerMaps.userLayer); err != nil {
		return Config{}, err
	}
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, layerMaps.localLayer); err != nil {
		return Config{}, err
	}
	resolvedConfig.RuntimeConfigFilePath = strings.TrimSpace(layerMaps.userConfigFilePath)
	resolvedConfig.RuntimeSystemConfigFilePath = strings.TrimSpace(layerMaps.systemConfigFilePath)
	resolvedConfig.RuntimeLocalConfigFilePath = strings.TrimSpace(layerMaps.localConfigFilePath)
	resolvedConfig.RuntimeExplicitConfigFilePath = strings.TrimSpace(layerMaps.explicitConfigFilePath)
	resolvedConfig.RuntimeBaseConfigFilePath = runtimeBaseConfigFilePathForLayerMaps(layerMaps)
	if _, err := applyRuntimeConfigEnvOverridesInPlace(&resolvedConfig); err != nil {
		return Config{}, err
	}
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, layerMaps.explicitLayer); err != nil {
		return Config{}, err
	}
	resolveRuntimeConfigRelativePathsInPlace(&resolvedConfig, layerMaps)
	resolvedConfig.NormalizeCompatibility()
	if err := resolvedConfig.Validate(); err != nil {
		return Config{}, err
	}
	return resolvedConfig, nil
}

func resolveRuntimeConfigRelativePathsInPlace(runtimeConfig *Config, layerMaps runtimeConfigLayerMaps) {
	if runtimeConfig == nil {
		return
	}
	runtimeConfig.ConnectorAuth.TokenStore.File.Path = resolveRuntimeConfigRelativeFilePathForLayerMaps(
		strings.TrimSpace(runtimeConfig.ConnectorAuth.TokenStore.File.Path),
		layerMaps,
		[]string{"connector_auth", "token_store", "file", "path"},
	)
}

func resolveRuntimeConfigRelativeFilePathForLayerMaps(
	rawPath string,
	layerMaps runtimeConfigLayerMaps,
	fieldPath []string,
) string {
	normalizedPath := strings.TrimSpace(rawPath)
	if normalizedPath == "" || filepath.IsAbs(normalizedPath) {
		return normalizedPath
	}
	for _, layer := range []struct {
		configFilePath string
		layer          map[string]any
	}{
		{
			configFilePath: layerMaps.explicitConfigFilePath,
			layer:          layerMaps.explicitLayer,
		},
		{
			configFilePath: layerMaps.localConfigFilePath,
			layer:          layerMaps.localLayer,
		},
		{
			configFilePath: layerMaps.userConfigFilePath,
			layer:          layerMaps.userLayer,
		},
		{
			configFilePath: layerMaps.systemConfigFilePath,
			layer:          layerMaps.systemLayer,
		},
	} {
		if _, ok := lookupRuntimeConfigStringFieldInLayer(layer.layer, fieldPath...); !ok {
			continue
		}
		return resolveRuntimeConfigRelativeFilePath(normalizedPath, layer.configFilePath)
	}
	return resolveRuntimeConfigRelativeFilePath(normalizedPath, runtimeBaseConfigFilePathForLayerMaps(layerMaps))
}

func resolveRuntimeConfigRelativeFilePath(rawPath string, configFilePath string) string {
	normalizedPath := strings.TrimSpace(rawPath)
	if normalizedPath == "" || filepath.IsAbs(normalizedPath) {
		return normalizedPath
	}
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return normalizedPath
	}
	return filepath.Clean(filepath.Join(filepath.Dir(normalizedConfigFilePath), normalizedPath))
}

func lookupRuntimeConfigStringFieldInLayer(layer map[string]any, path ...string) (string, bool) {
	if len(layer) == 0 || len(path) == 0 {
		return "", false
	}
	current := any(layer)
	for _, pathSegment := range path {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		nextValue, exists := currentMap[pathSegment]
		if !exists {
			return "", false
		}
		current = nextValue
	}
	stringValue, ok := current.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(stringValue), true
}

func runtimeBaseConfigFilePathForLayerMaps(layerMaps runtimeConfigLayerMaps) string {
	if strings.TrimSpace(layerMaps.explicitConfigFilePath) != "" {
		return strings.TrimSpace(layerMaps.explicitConfigFilePath)
	}
	if len(layerMaps.localLayer) > 0 {
		return strings.TrimSpace(layerMaps.localConfigFilePath)
	}
	if len(layerMaps.systemLayer) > 0 {
		return strings.TrimSpace(layerMaps.systemConfigFilePath)
	}
	return ""
}

func loadRuntimeConfigLayerMapFromFile(configFilePath string) (map[string]any, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return map[string]any{}, nil
	}
	absoluteConfigFilePath, err := filepath.Abs(normalizedConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("load runtime config layer map: resolve absolute path failed: %w", err)
	}
	rawContent, err := os.ReadFile(absoluteConfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("load runtime config layer map: read file failed: %w", err)
	}
	if strings.TrimSpace(string(rawContent)) == "" {
		return map[string]any{}, nil
	}
	layer := map[string]any{}
	if err := yaml.Unmarshal(rawContent, &layer); err != nil {
		return nil, fmt.Errorf("load runtime config layer map: decode yaml failed: %w", err)
	}
	if layer == nil {
		return map[string]any{}, nil
	}
	return layer, nil
}

func applyRuntimeConfigLayerMap(runtimeConfig *Config, layer map[string]any) error {
	if runtimeConfig == nil {
		return errors.New("apply runtime config layer map: nil config")
	}
	if len(layer) == 0 {
		return nil
	}
	encodedLayer, err := yaml.Marshal(layer)
	if err != nil {
		return fmt.Errorf("apply runtime config layer map: encode yaml failed: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(encodedLayer))
	decoder.KnownFields(true)
	if err := decoder.Decode(runtimeConfig); err != nil {
		return fmt.Errorf("apply runtime config layer map: decode yaml failed: %w", err)
	}
	return nil
}

func saveRuntimeConfigLayerMapToFile(layer map[string]any, configFilePath string) error {
	normalizedLayer := layer
	if normalizedLayer == nil {
		normalizedLayer = map[string]any{}
	}
	encodedLayer, err := yaml.Marshal(normalizedLayer)
	if err != nil {
		return fmt.Errorf("save runtime config layer map: encode yaml failed: %w", err)
	}
	return writeRuntimeConfigBytesToFile(encodedLayer, configFilePath)
}

func writeRuntimeConfigBytesToFile(encodedContent []byte, configFilePath string) error {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return fmt.Errorf("write runtime config bytes: empty file path")
	}
	absoluteConfigFilePath, err := filepath.Abs(normalizedConfigFilePath)
	if err != nil {
		return fmt.Errorf("write runtime config bytes: resolve absolute path failed: %w", err)
	}
	configFileDirectory := filepath.Dir(absoluteConfigFilePath)
	if mkdirErr := os.MkdirAll(configFileDirectory, 0o755); mkdirErr != nil {
		return fmt.Errorf("write runtime config bytes: ensure directory failed: %w", mkdirErr)
	}
	configFileMode := os.FileMode(0o600)
	if stat, statErr := os.Stat(absoluteConfigFilePath); statErr == nil {
		configFileMode = stat.Mode().Perm()
	}
	tempFile, err := os.CreateTemp(configFileDirectory, ".bridge-config-*.tmp")
	if err != nil {
		return fmt.Errorf("write runtime config bytes: create temp file failed: %w", err)
	}
	tempFilePath := tempFile.Name()
	cleanupTempFile := func() {
		_ = os.Remove(tempFilePath)
	}
	defer cleanupTempFile()
	if chmodErr := tempFile.Chmod(configFileMode); chmodErr != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write runtime config bytes: chmod temp file failed: %w", chmodErr)
	}
	if _, writeErr := tempFile.Write(encodedContent); writeErr != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write runtime config bytes: write temp file failed: %w", writeErr)
	}
	if closeErr := tempFile.Close(); closeErr != nil {
		return fmt.Errorf("write runtime config bytes: close temp file failed: %w", closeErr)
	}
	if renameErr := fileutil.ReplaceFile(tempFilePath, absoluteConfigFilePath); renameErr != nil {
		return fmt.Errorf("write runtime config bytes: replace target file failed: %w", renameErr)
	}
	return nil
}

func buildRuntimeConfigFieldSources(layerMaps runtimeConfigLayerMaps) (map[string]any, error) {
	envOverrideKeys, err := runtimeEnvOverrideKeys()
	if err != nil {
		return nil, err
	}
	fieldSources := make(map[string]any, len(runtimeConfigFieldYAMLPaths))
	for fieldKey, yamlPath := range runtimeConfigFieldYAMLPaths {
		if _, exists := readRuntimeConfigYAMLPath(layerMaps.explicitLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceExplicit
			continue
		}
		if _, exists := envOverrideKeys[fieldKey]; exists {
			fieldSources[fieldKey] = runtimeConfigSourceEnv
			continue
		}
		if _, exists := readRuntimeConfigYAMLPath(layerMaps.localLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceLocal
			continue
		}
		if _, exists := readRuntimeConfigYAMLPath(layerMaps.userLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceUser
			continue
		}
		if _, exists := readRuntimeConfigYAMLPath(layerMaps.systemLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceSystem
			continue
		}
		fieldSources[fieldKey] = runtimeConfigSourceDefault
	}
	return fieldSources, nil
}

func resolveEditableRuntimeConfigTarget(
	layerMaps runtimeConfigLayerMaps,
) (runtimeConfigEditableTarget, error) {
	explicitConfigEditable, err := isRuntimeConfigTargetEditable(layerMaps.explicitConfigFilePath, true)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat explicit config failed: %w", err)
	}
	if explicitConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceExplicit,
			layer:  cloneRuntimeConfigLayerMap(layerMaps.explicitLayer),
			path:   strings.TrimSpace(layerMaps.explicitConfigFilePath),
		}, nil
	}
	localConfigEditable, err := isRuntimeConfigTargetEditable(layerMaps.localConfigFilePath, false)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat local config failed: %w", err)
	}
	if localConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceLocal,
			layer:  cloneRuntimeConfigLayerMap(layerMaps.localLayer),
			path:   strings.TrimSpace(layerMaps.localConfigFilePath),
		}, nil
	}
	userConfigEditable, err := isRuntimeConfigTargetEditable(layerMaps.userConfigFilePath, true)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat user config failed: %w", err)
	}
	if userConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceUser,
			layer:  cloneRuntimeConfigLayerMap(layerMaps.userLayer),
			path:   strings.TrimSpace(layerMaps.userConfigFilePath),
		}, nil
	}
	if strings.TrimSpace(layerMaps.userConfigFilePath) != "" {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceUser,
			layer:  cloneRuntimeConfigLayerMap(layerMaps.userLayer),
			path:   strings.TrimSpace(layerMaps.userConfigFilePath),
		}, nil
	}
	systemConfigEditable, err := isRuntimeConfigTargetEditable(layerMaps.systemConfigFilePath, false)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat system config failed: %w", err)
	}
	if systemConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceSystem,
			layer:  cloneRuntimeConfigLayerMap(layerMaps.systemLayer),
			path:   strings.TrimSpace(layerMaps.systemConfigFilePath),
		}, nil
	}
	return runtimeConfigEditableTarget{
		source: runtimeConfigSourceDefault,
		layer:  map[string]any{},
		path:   "",
	}, nil
}

func isRuntimeConfigTargetEditable(configFilePath string, allowCreate bool) (bool, error) {
	normalizedConfigFilePath := strings.TrimSpace(configFilePath)
	if normalizedConfigFilePath == "" {
		return false, nil
	}
	configFileExists, err := fileExists(normalizedConfigFilePath)
	if err != nil {
		return false, err
	}
	if !configFileExists {
		return allowCreate, nil
	}
	configFileHandle, err := os.OpenFile(normalizedConfigFilePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false, nil
	}
	if closeErr := configFileHandle.Close(); closeErr != nil {
		return false, closeErr
	}
	return true, nil
}

func buildEditableRuntimeConfigPatch(userLayer map[string]any) map[string]any {
	if len(userLayer) == 0 {
		return map[string]any{}
	}
	editablePatch := map[string]any{}
	visitedYAMLPaths := map[string]struct{}{}
	for _, yamlPath := range runtimeConfigEditableFieldYAMLPaths {
		if _, visited := visitedYAMLPaths[yamlPath]; visited {
			continue
		}
		visitedYAMLPaths[yamlPath] = struct{}{}
		fieldValue, exists := readRuntimeConfigYAMLPath(userLayer, yamlPath)
		if !exists {
			continue
		}
		setRuntimeConfigYAMLPath(editablePatch, yamlPath, cloneRuntimeConfigLayerValue(fieldValue))
	}
	return editablePatch
}

func buildEditableRuntimeConfigRestorePreview(
	layerMaps runtimeConfigLayerMaps,
) (map[string]any, error) {
	editableTarget, err := resolveEditableRuntimeConfigTarget(layerMaps)
	if err != nil {
		return nil, err
	}
	restorePreview := map[string]any{}
	if len(editableTarget.layer) == 0 {
		return restorePreview, nil
	}
	fallbackLayerMaps := layerMaps
	switch editableTarget.source {
	case runtimeConfigSourceExplicit:
		fallbackLayerMaps.explicitLayer = map[string]any{}
	case runtimeConfigSourceLocal:
		fallbackLayerMaps.localLayer = map[string]any{}
	case runtimeConfigSourceUser:
		fallbackLayerMaps.userLayer = map[string]any{}
	case runtimeConfigSourceSystem:
		fallbackLayerMaps.systemLayer = map[string]any{}
	default:
		return restorePreview, nil
	}
	fallbackConfig, err := buildRuntimeConfigFromLayerMaps(fallbackLayerMaps)
	if err != nil {
		return nil, err
	}
	fallbackSources, err := buildRuntimeConfigFieldSources(fallbackLayerMaps)
	if err != nil {
		return nil, err
	}
	for fieldKey, yamlPath := range runtimeConfigEditableFieldYAMLPaths {
		if _, exists := readRuntimeConfigYAMLPath(editableTarget.layer, yamlPath); !exists {
			continue
		}
		restorePreview[fieldKey] = map[string]any{
			"source": fallbackSources[fieldKey],
			"value":  readRuntimeConfigFieldSnapshotValue(fallbackConfig, fieldKey),
		}
	}
	return restorePreview, nil
}

func readRuntimeConfigFieldSnapshotValue(runtimeConfig Config, fieldKey string) any {
	switch strings.TrimSpace(fieldKey) {
	case "ingress.http_addr":
		return runtimeConfig.Ingress.HTTPAddr
	case "ingress.grpc_addr":
		return runtimeConfig.Ingress.GRPCAddr
	case "ingress.https_addr":
		return runtimeConfig.Ingress.HTTPSAddr
	case "ingress.tls_sni_addr":
		return runtimeConfig.Ingress.TLSSNIAddr
	case "ingress.tcp_port_range":
		return runtimeConfig.Ingress.TCPPortRange
	case "ingress.base_domain":
		return strings.TrimSpace(runtimeConfig.Ingress.BaseDomain)
	case "admin.enabled":
		return runtimeConfig.Admin.Enabled
	case "admin.listen_addr":
		return runtimeConfig.Admin.ListenAddr
	case "admin.allow_shared_listener":
		return runtimeConfig.Admin.AllowSharedListener
	case "admin.base_path":
		return normalizeAdminUIBasePath(runtimeConfig.Admin.BasePath)
	case "admin.ui_enabled":
		return runtimeConfig.Admin.UIEnabled
	case "connector_auth.token_store.driver":
		return strings.TrimSpace(runtimeConfig.ConnectorAuth.TokenStore.Driver)
	case "connector_auth.token_store.file.path":
		return strings.TrimSpace(runtimeConfig.ConnectorAuth.TokenStore.File.Path)
	case "control_plane.listen_addr":
		return runtimeConfig.ControlPlane.ListenAddr
	case "control_plane.grpc_h2_listen_addr":
		return runtimeConfig.ControlPlane.GRPCH2ListenAddr
	case "control_plane.quic_listen_addr":
		return runtimeConfig.ControlPlane.QUICListenAddr
	case "control_plane.heartbeat_timeout_ms":
		return uint64(runtimeConfig.ControlPlane.HeartbeatTimeout.Milliseconds())
	case "control_plane.tls_mode":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSMode)
	case "control_plane.tls_cert_source":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSCertSource)
	case "control_plane.tls_cert_file":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSCertFile)
	case "control_plane.tls_key_file":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSKeyFile)
	case "control_plane.tls_ca_cert_file":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSCACertFile)
	case "control_plane.tls_ca_key_file":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSCAKeyFile)
	case "control_plane.tls_server_common_name":
		return strings.TrimSpace(runtimeConfig.ControlPlane.TLSServerCommonName)
	case "control_plane.tls_server_san_dns":
		return append([]string(nil), runtimeConfig.ControlPlane.TLSServerSANDNS...)
	case "control_plane.tls_server_san_ips":
		return append([]string(nil), runtimeConfig.ControlPlane.TLSServerSANIPs...)
	case "control_plane.tls_server_cert_ttl_ms":
		return uint64(runtimeConfig.ControlPlane.TLSServerCertTTL.Milliseconds())
	case "control_plane.tls_server_cert_renew_before_ms":
		return uint64(runtimeConfig.ControlPlane.TLSServerCertRenewBefore.Milliseconds())
	case "observability.log_level":
		return runtimeConfig.Observability.LogLevel
	case "observability.metrics_addr":
		return runtimeConfig.Observability.MetricsAddr
	case "default_scope.namespace":
		return strings.TrimSpace(runtimeConfig.DefaultScope.Namespace)
	case "default_scope.environment":
		return strings.TrimSpace(runtimeConfig.DefaultScope.Environment)
	default:
		return nil
	}
}

func lookupRuntimeConfigPatchYAMLPath(patchKey string) (string, bool) {
	normalizedPatchKey := strings.TrimSpace(patchKey)
	if normalizedPatchKey == "control_plane.heartbeat_timeout" {
		return "control_plane.heartbeat_timeout", true
	}
	yamlPath, exists := runtimeConfigEditableFieldYAMLPaths[normalizedPatchKey]
	return yamlPath, exists
}

func runtimeEnvOverrideKeys() (map[string]struct{}, error) {
	configProbe := DefaultConfig()
	return applyRuntimeConfigEnvOverridesInPlace(&configProbe)
}

func applyRuntimeConfigEnvOverridesInPlace(runtimeConfig *Config) (map[string]struct{}, error) {
	if runtimeConfig == nil {
		return nil, errors.New("apply runtime config env overrides: nil config")
	}
	appliedFieldKeys := make(map[string]struct{})
	if err := applyEditableRuntimeConfigEnvOverrides(runtimeConfig, appliedFieldKeys); err != nil {
		return nil, err
	}
	if err := applyControlPlaneTLSEnvOverridesWithTracking(runtimeConfig, appliedFieldKeys); err != nil {
		return nil, err
	}
	return appliedFieldKeys, nil
}

func applyEditableRuntimeConfigEnvOverrides(runtimeConfig *Config, appliedFieldKeys map[string]struct{}) error {
	if runtimeConfig == nil {
		return errors.New("apply editable runtime config env overrides: nil config")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressHTTPAddr); ok {
		runtimeConfig.Ingress.HTTPAddr = value
		appliedFieldKeys["ingress.http_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressGRPCAddr); ok {
		runtimeConfig.Ingress.GRPCAddr = value
		appliedFieldKeys["ingress.grpc_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressHTTPSAddr); ok {
		runtimeConfig.Ingress.HTTPSAddr = value
		appliedFieldKeys["ingress.https_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressTLSSNIAddr); ok {
		runtimeConfig.Ingress.TLSSNIAddr = value
		appliedFieldKeys["ingress.tls_sni_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressTCPPortRange); ok {
		runtimeConfig.Ingress.TCPPortRange = value
		appliedFieldKeys["ingress.tcp_port_range"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envIngressBaseDomain); ok {
		runtimeConfig.Ingress.BaseDomain = value
		appliedFieldKeys["ingress.base_domain"] = struct{}{}
	}
	if value, applied, err := lookupBoolEnv(os.LookupEnv, envAdminEnabled); err != nil {
		return err
	} else if applied {
		runtimeConfig.Admin.Enabled = value
		appliedFieldKeys["admin.enabled"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envAdminListenAddr); ok {
		runtimeConfig.Admin.ListenAddr = value
		appliedFieldKeys["admin.listen_addr"] = struct{}{}
	}
	if value, applied, err := lookupBoolEnv(os.LookupEnv, envAdminAllowSharedListener); err != nil {
		return err
	} else if applied {
		runtimeConfig.Admin.AllowSharedListener = value
		appliedFieldKeys["admin.allow_shared_listener"] = struct{}{}
	}
	if value, applied, err := lookupBoolEnv(os.LookupEnv, envAdminUIEnabled); err != nil {
		return err
	} else if applied {
		runtimeConfig.Admin.UIEnabled = value
		appliedFieldKeys["admin.ui_enabled"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envAdminBasePath); ok {
		runtimeConfig.Admin.BasePath = normalizeAdminUIBasePath(value)
		appliedFieldKeys["admin.base_path"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneListenAddr); ok {
		runtimeConfig.ControlPlane.ListenAddr = value
		appliedFieldKeys["control_plane.listen_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneGRPCH2ListenAddr); ok {
		runtimeConfig.ControlPlane.GRPCH2ListenAddr = value
		appliedFieldKeys["control_plane.grpc_h2_listen_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneQUICListenAddr); ok {
		runtimeConfig.ControlPlane.QUICListenAddr = value
		appliedFieldKeys["control_plane.quic_listen_addr"] = struct{}{}
	}
	if value, applied, err := lookupHeartbeatTimeoutEnv(os.LookupEnv); err != nil {
		return err
	} else if applied {
		runtimeConfig.ControlPlane.HeartbeatTimeout = value
		appliedFieldKeys["control_plane.heartbeat_timeout_ms"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envObservabilityLogLevel); ok {
		runtimeConfig.Observability.LogLevel = value
		appliedFieldKeys["observability.log_level"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envObservabilityMetricsAddr); ok {
		runtimeConfig.Observability.MetricsAddr = value
		appliedFieldKeys["observability.metrics_addr"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envDefaultScopeNamespace); ok {
		runtimeConfig.DefaultScope.Namespace = value
		appliedFieldKeys["default_scope.namespace"] = struct{}{}
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envDefaultScopeEnvironment); ok {
		runtimeConfig.DefaultScope.Environment = value
		appliedFieldKeys["default_scope.environment"] = struct{}{}
	}
	return nil
}

func applyControlPlaneTLSEnvOverridesWithTracking(runtimeConfig *Config, appliedFieldKeys map[string]struct{}) error {
	if runtimeConfig == nil {
		return errors.New("apply control plane tls env overrides: nil config")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSMode); ok {
		runtimeConfig.ControlPlane.TLSMode = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_mode")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSCertSource); ok {
		runtimeConfig.ControlPlane.TLSCertSource = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_cert_source")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSCertFile); ok {
		runtimeConfig.ControlPlane.TLSCertFile = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_cert_file")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSKeyFile); ok {
		runtimeConfig.ControlPlane.TLSKeyFile = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_key_file")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSCACertFile); ok {
		runtimeConfig.ControlPlane.TLSCACertFile = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_ca_cert_file")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSCAKeyFile); ok {
		runtimeConfig.ControlPlane.TLSCAKeyFile = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_ca_key_file")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envControlPlaneTLSServerCommonName); ok {
		runtimeConfig.ControlPlane.TLSServerCommonName = value
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_common_name")
	}
	if sanDNSList, hasValue := commaSeparatedEnvListFromLookup(os.LookupEnv, envControlPlaneTLSServerSANDNS); hasValue {
		runtimeConfig.ControlPlane.TLSServerSANDNS = sanDNSList
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_san_dns")
	}
	if sanIPList, hasValue := commaSeparatedEnvListFromLookup(os.LookupEnv, envControlPlaneTLSServerSANIPs); hasValue {
		runtimeConfig.ControlPlane.TLSServerSANIPs = sanIPList
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_san_ips")
	}
	serverCertTTL, applied, err := lookupDurationEnv(os.LookupEnv, envControlPlaneTLSServerCertTTL)
	if err != nil {
		return err
	}
	if applied {
		runtimeConfig.ControlPlane.TLSServerCertTTL = serverCertTTL
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_cert_ttl_ms")
	}
	serverCertRenewBefore, applied, err := lookupDurationEnv(os.LookupEnv, envControlPlaneTLSServerCertRenewBefore)
	if err != nil {
		return err
	}
	if applied {
		runtimeConfig.ControlPlane.TLSServerCertRenewBefore = serverCertRenewBefore
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_cert_renew_before_ms")
	}
	return nil
}

func applyRuntimeConfigPatch(
	configCandidate *Config,
	editableLayer map[string]any,
	editableConfigFilePath string,
	patchKey string,
	patchValue any,
) error {
	if configCandidate == nil {
		return errors.New("apply runtime config patch: nil config")
	}
	if editableLayer == nil {
		return errors.New("apply runtime config patch: nil editable layer")
	}
	normalizedPatchKey := strings.TrimSpace(patchKey)
	if patchValue == nil {
		yamlPath, exists := lookupRuntimeConfigPatchYAMLPath(normalizedPatchKey)
		if !exists {
			return fmt.Errorf("unsupported patch key=%s", normalizedPatchKey)
		}
		deleteRuntimeConfigYAMLPath(editableLayer, yamlPath)
		return nil
	}
	switch normalizedPatchKey {
	case "ingress.http_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.HTTPAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.grpc_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.GRPCAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.https_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.HTTPSAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.tls_sni_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.TLSSNIAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.tcp_port_range":
		portRange, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.TCPPortRange = portRange
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], portRange)
	case "ingress.base_domain":
		baseDomain, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.BaseDomain = baseDomain
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], baseDomain)
	case "admin.enabled":
		enabled, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.Enabled = enabled
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], enabled)
	case "admin.listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "admin.allow_shared_listener":
		allowSharedListener, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.AllowSharedListener = allowSharedListener
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], allowSharedListener)
	case "admin.ui_enabled":
		enabled, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.UIEnabled = enabled
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], enabled)
	case "admin.base_path":
		basePath, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		normalizedBasePath := normalizeAdminUIBasePath(basePath)
		configCandidate.Admin.BasePath = normalizedBasePath
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], normalizedBasePath)
	case "control_plane.listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "control_plane.grpc_h2_listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.GRPCH2ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "control_plane.quic_listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.QUICListenAddr = listenAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "control_plane.heartbeat_timeout":
		heartbeatTimeout, err := parsePatchDuration(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.HeartbeatTimeout = heartbeatTimeout
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths["control_plane.heartbeat_timeout_ms"], heartbeatTimeout.String())
	case "control_plane.heartbeat_timeout_ms":
		heartbeatTimeout, err := parsePatchDurationMillis(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.HeartbeatTimeout = heartbeatTimeout
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], heartbeatTimeout.String())
	case "control_plane.tls_mode":
		tlsMode, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSMode = tlsMode
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsMode)
		applyManagedCAControlPlaneDefaults(configCandidate, editableLayer, editableConfigFilePath)
	case "control_plane.tls_cert_source":
		tlsCertSource, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSCertSource = tlsCertSource
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsCertSource)
		applyManagedCAControlPlaneDefaults(configCandidate, editableLayer, editableConfigFilePath)
	case "control_plane.tls_cert_file":
		tlsCertFile, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSCertFile = tlsCertFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsCertFile)
	case "control_plane.tls_key_file":
		tlsKeyFile, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSKeyFile = tlsKeyFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsKeyFile)
	case "control_plane.tls_ca_cert_file":
		tlsCACertFile, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSCACertFile = tlsCACertFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsCACertFile)
	case "control_plane.tls_ca_key_file":
		tlsCAKeyFile, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSCAKeyFile = tlsCAKeyFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsCAKeyFile)
	case "control_plane.tls_server_common_name":
		tlsServerCommonName, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSServerCommonName = tlsServerCommonName
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsServerCommonName)
	case "control_plane.tls_server_san_dns":
		tlsServerSANDNS, err := parsePatchStringList(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSServerSANDNS = append([]string(nil), tlsServerSANDNS...)
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], append([]string(nil), tlsServerSANDNS...))
	case "control_plane.tls_server_san_ips":
		tlsServerSANIPs, err := parsePatchStringList(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSServerSANIPs = append([]string(nil), tlsServerSANIPs...)
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], append([]string(nil), tlsServerSANIPs...))
	case "control_plane.tls_server_cert_ttl_ms":
		tlsServerCertTTL, err := parsePatchDurationMillis(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSServerCertTTL = tlsServerCertTTL
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsServerCertTTL.String())
	case "control_plane.tls_server_cert_renew_before_ms":
		tlsServerCertRenewBefore, err := parsePatchDurationMillis(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.TLSServerCertRenewBefore = tlsServerCertRenewBefore
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], tlsServerCertRenewBefore.String())
	case "observability.log_level":
		logLevel, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		normalizedLogLevel := strings.TrimSpace(logLevel)
		configCandidate.Observability.LogLevel = normalizedLogLevel
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], normalizedLogLevel)
	case "observability.metrics_addr":
		metricsAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Observability.MetricsAddr = metricsAddr
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], metricsAddr)
	case "default_scope.namespace":
		namespace, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.DefaultScope.Namespace = namespace
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], namespace)
	case "default_scope.environment":
		environment, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.DefaultScope.Environment = environment
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths[patchKey], environment)
	default:
		return fmt.Errorf("unsupported patch key=%s", patchKey)
	}
	return nil
}

func applyManagedCAControlPlaneDefaults(
	configCandidate *Config,
	editableLayer map[string]any,
	editableConfigFilePath string,
) {
	if configCandidate == nil || editableLayer == nil {
		return
	}
	normalizedTLSMode := strings.ToLower(strings.TrimSpace(configCandidate.ControlPlane.TLSMode))
	if normalizedTLSMode == "" || normalizedTLSMode == "plaintext" {
		return
	}
	normalizedTLSCertSource := strings.ToLower(strings.TrimSpace(configCandidate.ControlPlane.TLSCertSource))
	if normalizedTLSCertSource != "managed_ca" {
		return
	}
	defaultCACertFile, defaultCAKeyFile := defaultManagedCAFilePaths(editableConfigFilePath)
	if strings.TrimSpace(configCandidate.ControlPlane.TLSCACertFile) == "" && strings.TrimSpace(defaultCACertFile) != "" {
		configCandidate.ControlPlane.TLSCACertFile = defaultCACertFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths["control_plane.tls_ca_cert_file"], defaultCACertFile)
	}
	if strings.TrimSpace(configCandidate.ControlPlane.TLSCAKeyFile) == "" && strings.TrimSpace(defaultCAKeyFile) != "" {
		configCandidate.ControlPlane.TLSCAKeyFile = defaultCAKeyFile
		setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths["control_plane.tls_ca_key_file"], defaultCAKeyFile)
	}
	normalizedSANDNS := normalizeNonEmptyStringSlice(configCandidate.ControlPlane.TLSServerSANDNS)
	normalizedSANIPs := normalizeNonEmptyStringSlice(configCandidate.ControlPlane.TLSServerSANIPs)
	if len(normalizedSANDNS) == 0 && len(normalizedSANIPs) == 0 {
		defaultSANDNS, defaultSANIPs := defaultManagedCASANs(configCandidate)
		if len(defaultSANDNS) > 0 {
			configCandidate.ControlPlane.TLSServerSANDNS = append([]string(nil), defaultSANDNS...)
			setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths["control_plane.tls_server_san_dns"], append([]string(nil), defaultSANDNS...))
		}
		if len(defaultSANIPs) > 0 {
			configCandidate.ControlPlane.TLSServerSANIPs = append([]string(nil), defaultSANIPs...)
			setRuntimeConfigYAMLPath(editableLayer, runtimeConfigFieldYAMLPaths["control_plane.tls_server_san_ips"], append([]string(nil), defaultSANIPs...))
		}
		if strings.TrimSpace(configCandidate.ControlPlane.TLSServerCommonName) == "" {
			defaultServerCommonName := defaultManagedCAServerCommonName(defaultSANDNS, defaultSANIPs)
			if defaultServerCommonName != "" {
				configCandidate.ControlPlane.TLSServerCommonName = defaultServerCommonName
				setRuntimeConfigYAMLPath(
					editableLayer,
					runtimeConfigFieldYAMLPaths["control_plane.tls_server_common_name"],
					defaultServerCommonName,
				)
			}
		}
	}
}

func defaultManagedCAFilePaths(runtimeConfigFilePath string) (string, string) {
	configDirectory := strings.TrimSpace(filepath.Dir(strings.TrimSpace(runtimeConfigFilePath)))
	if configDirectory == "" || configDirectory == "." {
		return "", ""
	}
	return filepath.Join(configDirectory, "root-ca.crt"), filepath.Join(configDirectory, "root-ca.key")
}

func defaultManagedCASANs(configCandidate *Config) ([]string, []string) {
	if configCandidate == nil {
		return []string{"localhost"}, []string{"127.0.0.1"}
	}
	sanDNSSet := map[string]struct{}{}
	sanIPSet := map[string]struct{}{}
	for _, listenAddr := range []string{
		configCandidate.ControlPlane.ListenAddr,
		configCandidate.ControlPlane.GRPCH2ListenAddr,
		configCandidate.ControlPlane.QUICListenAddr,
	} {
		hostText := managedCAHostFromListenAddr(listenAddr)
		if hostText == "" {
			continue
		}
		if parsedIP := net.ParseIP(hostText); parsedIP != nil {
			if parsedIP.IsUnspecified() {
				continue
			}
			sanIPSet[parsedIP.String()] = struct{}{}
			continue
		}
		sanDNSSet[hostText] = struct{}{}
	}
	if len(sanDNSSet) == 0 && len(sanIPSet) == 0 {
		sanDNSSet["localhost"] = struct{}{}
		sanIPSet["127.0.0.1"] = struct{}{}
	}
	sanDNSNames := make([]string, 0, len(sanDNSSet))
	for sanDNSName := range sanDNSSet {
		sanDNSNames = append(sanDNSNames, sanDNSName)
	}
	sanIPTexts := make([]string, 0, len(sanIPSet))
	for sanIPText := range sanIPSet {
		sanIPTexts = append(sanIPTexts, sanIPText)
	}
	sort.Strings(sanDNSNames)
	sort.Strings(sanIPTexts)
	return sanDNSNames, sanIPTexts
}

func managedCAHostFromListenAddr(listenAddr string) string {
	normalizedListenAddr := strings.TrimSpace(listenAddr)
	if normalizedListenAddr == "" {
		return ""
	}
	hostText, _, err := net.SplitHostPort(normalizedListenAddr)
	if err != nil {
		return ""
	}
	hostText = strings.TrimSpace(hostText)
	if hostText == "" {
		return ""
	}
	return strings.Trim(hostText, "[]")
}

func defaultManagedCAServerCommonName(sanDNSNames []string, sanIPTexts []string) string {
	if len(sanDNSNames) > 0 {
		return strings.TrimSpace(sanDNSNames[0])
	}
	if len(sanIPTexts) > 0 {
		return strings.TrimSpace(sanIPTexts[0])
	}
	return ""
}

func cloneRuntimeConfigLayerMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneRuntimeConfigLayerValue(value)
	}
	return cloned
}

func cloneRuntimeConfigLayerValue(value any) any {
	switch typedValue := value.(type) {
	case map[string]any:
		return cloneRuntimeConfigLayerMap(typedValue)
	case []any:
		cloned := make([]any, len(typedValue))
		for index, item := range typedValue {
			cloned[index] = cloneRuntimeConfigLayerValue(item)
		}
		return cloned
	default:
		return typedValue
	}
}

func setRuntimeConfigYAMLPath(record map[string]any, dottedPath string, value any) {
	segments := strings.Split(strings.TrimSpace(dottedPath), ".")
	if len(segments) == 0 {
		return
	}
	current := record
	for index, segment := range segments {
		if index == len(segments)-1 {
			current[segment] = value
			return
		}
		nextRecord, ok := current[segment].(map[string]any)
		if !ok {
			nextRecord = map[string]any{}
			current[segment] = nextRecord
		}
		current = nextRecord
	}
}

func deleteRuntimeConfigYAMLPath(record map[string]any, dottedPath string) {
	segments := strings.Split(strings.TrimSpace(dottedPath), ".")
	deleteRuntimeConfigYAMLPathSegments(record, segments)
}

func deleteRuntimeConfigYAMLPathSegments(record map[string]any, segments []string) bool {
	if len(record) == 0 || len(segments) == 0 {
		return len(record) == 0
	}
	segment := strings.TrimSpace(segments[0])
	if segment == "" {
		return len(record) == 0
	}
	if len(segments) == 1 {
		delete(record, segment)
		return len(record) == 0
	}
	nextRecord, ok := record[segment].(map[string]any)
	if !ok {
		return len(record) == 0
	}
	if deleteRuntimeConfigYAMLPathSegments(nextRecord, segments[1:]) {
		delete(record, segment)
	}
	return len(record) == 0
}

func readRuntimeConfigYAMLPath(record map[string]any, dottedPath string) (any, bool) {
	segments := strings.Split(strings.TrimSpace(dottedPath), ".")
	if len(segments) == 0 {
		return nil, false
	}
	var current any = record
	for _, segment := range segments {
		currentRecord, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		nextValue, exists := currentRecord[segment]
		if !exists {
			return nil, false
		}
		current = nextValue
	}
	return current, true
}

func lookupNonEmptyEnv(lookupEnv envLookupFn, key string) (string, bool) {
	rawValue, exists := lookupEnv(key)
	if !exists {
		return "", false
	}
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return "", false
	}
	return normalizedValue, true
}

func lookupBoolEnv(lookupEnv envLookupFn, key string) (bool, bool, error) {
	rawValue, exists := lookupEnv(key)
	if !exists {
		return false, false, nil
	}
	normalizedValue := strings.ToLower(strings.TrimSpace(rawValue))
	if normalizedValue == "" {
		return false, false, nil
	}
	if normalizedValue == "true" || normalizedValue == "1" {
		return true, true, nil
	}
	if normalizedValue == "false" || normalizedValue == "0" {
		return false, true, nil
	}
	return false, false, fmt.Errorf("parse %s failed: expect bool value", key)
}

func lookupHeartbeatTimeoutEnv(lookupEnv envLookupFn) (time.Duration, bool, error) {
	if rawValue, exists := lookupEnv(envControlPlaneHeartbeatTimeoutMS); exists {
		normalizedValue := strings.TrimSpace(rawValue)
		if normalizedValue != "" {
			parsedMillis, err := strconv.Atoi(normalizedValue)
			if err != nil {
				return 0, false, fmt.Errorf("parse %s failed: %w", envControlPlaneHeartbeatTimeoutMS, err)
			}
			if parsedMillis <= 0 {
				return 0, false, fmt.Errorf("parse %s failed: expect positive integer", envControlPlaneHeartbeatTimeoutMS)
			}
			return time.Duration(parsedMillis) * time.Millisecond, true, nil
		}
	}
	return lookupDurationEnv(lookupEnv, envControlPlaneHeartbeatTimeout)
}

func lookupDurationEnv(lookupEnv envLookupFn, key string) (time.Duration, bool, error) {
	rawValue, exists := lookupEnv(key)
	if !exists {
		return 0, false, nil
	}
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return 0, false, nil
	}
	parsedDuration, err := time.ParseDuration(normalizedValue)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s failed: %w", key, err)
	}
	if parsedDuration <= 0 {
		return 0, false, fmt.Errorf("parse %s failed: expect positive duration", key)
	}
	return parsedDuration, true, nil
}

func commaSeparatedEnvListFromLookup(lookupEnv envLookupFn, key string) ([]string, bool) {
	rawValue, exists := lookupEnv(key)
	if !exists {
		return nil, false
	}
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return nil, true
	}
	rawParts := strings.Split(normalizedValue, ",")
	normalizedParts := make([]string, 0, len(rawParts))
	for _, rawPart := range rawParts {
		normalizedPart := strings.TrimSpace(rawPart)
		if normalizedPart == "" {
			continue
		}
		normalizedParts = append(normalizedParts, normalizedPart)
	}
	return normalizedParts, true
}

func fileExists(filePath string) (bool, error) {
	normalizedFilePath := strings.TrimSpace(filePath)
	if normalizedFilePath == "" {
		return false, nil
	}
	_, err := os.Stat(normalizedFilePath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func joinUnixPath(base string, elements ...string) string {
	normalizedBase := strings.TrimRight(strings.TrimSpace(base), "/")
	if normalizedBase == "" {
		normalizedBase = "/"
	}
	parts := []string{normalizedBase}
	for _, element := range elements {
		normalizedElement := strings.Trim(element, "/")
		if normalizedElement == "" {
			continue
		}
		parts = append(parts, normalizedElement)
	}
	return strings.Join(parts, "/")
}

func joinWindowsPath(base string, elements ...string) string {
	normalizedBase := strings.TrimRight(strings.TrimSpace(base), `\/`)
	parts := []string{normalizedBase}
	for _, element := range elements {
		normalizedElement := strings.Trim(element, `\/`)
		if normalizedElement == "" {
			continue
		}
		parts = append(parts, normalizedElement)
	}
	return strings.Join(parts, `\`)
}

func markRuntimeConfigFieldApplied(appliedFieldKeys map[string]struct{}, fieldKey string) {
	if appliedFieldKeys == nil {
		return
	}
	appliedFieldKeys[strings.TrimSpace(fieldKey)] = struct{}{}
}
