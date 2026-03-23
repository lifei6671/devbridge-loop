package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	runtimeDefaultConfigFileName = "bridge.yaml"

	runtimeConfigSourceDefault = "default"
	runtimeConfigSourceSystem  = "system"
	runtimeConfigSourceUser    = "user"
	runtimeConfigSourceEnv     = "env"

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

var runtimeConfigFieldYAMLPaths = map[string]string{
	"default_scope.namespace":            "default_scope.namespace",
	"default_scope.environment":          "default_scope.environment",
	"ingress.http_addr":                  "ingress.http_addr",
	"ingress.grpc_addr":                  "ingress.grpc_addr",
	"ingress.https_addr":                 "ingress.https_addr",
	"ingress.tls_sni_addr":               "ingress.tls_sni_addr",
	"ingress.tcp_port_range":             "ingress.tcp_port_range",
	"ingress.base_domain":                "ingress.base_domain",
	"admin.enabled":                      "admin.enabled",
	"admin.listen_addr":                  "admin.listen_addr",
	"admin.allow_shared_listener":        "admin.allow_shared_listener",
	"admin.base_path":                    "admin.base_path",
	"admin.ui_enabled":                   "admin.ui_enabled",
	"control_plane.listen_addr":          "control_plane.listen_addr",
	"control_plane.grpc_h2_listen_addr":  "control_plane.grpc_h2_listen_addr",
	"control_plane.heartbeat_timeout_ms": "control_plane.heartbeat_timeout",
	"observability.log_level":            "observability.log_level",
	"observability.metrics_addr":         "observability.metrics_addr",
}

// LoadRuntimeConfig 按“环境变量 > 用户目录 > 系统/显式基础配置 > 默认值”的顺序构建运行配置。
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
	baseConfigFilePath, err := resolveRuntimeBaseConfigFilePath(
		explicitBaseConfigFilePath,
		workingDirectory,
		runtime.GOOS,
		os.LookupEnv,
	)
	if err != nil {
		return Config{}, err
	}
	baseLayer, err := maybeLoadRuntimeConfigLayerMap(baseConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	userLayer, err := maybeLoadRuntimeConfigLayerMap(userConfigFilePath)
	if err != nil {
		return Config{}, err
	}
	return buildRuntimeConfigFromLayers(baseConfigFilePath, baseLayer, userConfigFilePath, userLayer)
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

func resolveRuntimeBaseConfigFilePath(
	explicitBaseConfigFilePath string,
	workingDirectory string,
	goos string,
	lookupEnv envLookupFn,
) (string, error) {
	normalizedExplicitBaseConfigFilePath := strings.TrimSpace(explicitBaseConfigFilePath)
	if normalizedExplicitBaseConfigFilePath != "" {
		return filepath.Abs(normalizedExplicitBaseConfigFilePath)
	}
	systemConfigFilePath, err := resolveRuntimeSystemConfigFilePath(goos, lookupEnv)
	if err != nil {
		return "", err
	}
	systemConfigExists, err := fileExists(systemConfigFilePath)
	if err != nil {
		return "", err
	}
	if systemConfigExists {
		return systemConfigFilePath, nil
	}
	legacyLocalConfigFilePath := filepath.Join(strings.TrimSpace(workingDirectory), runtimeDefaultConfigFileName)
	legacyLocalConfigExists, err := fileExists(legacyLocalConfigFilePath)
	if err != nil {
		return "", err
	}
	if legacyLocalConfigExists {
		return legacyLocalConfigFilePath, nil
	}
	return "", nil
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

func buildRuntimeConfigFromLayers(
	baseConfigFilePath string,
	baseLayer map[string]any,
	userConfigFilePath string,
	userLayer map[string]any,
) (Config, error) {
	resolvedConfig := DefaultConfig()
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, baseLayer); err != nil {
		return Config{}, err
	}
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, userLayer); err != nil {
		return Config{}, err
	}
	resolvedConfig.RuntimeConfigFilePath = strings.TrimSpace(userConfigFilePath)
	resolvedConfig.RuntimeBaseConfigFilePath = strings.TrimSpace(baseConfigFilePath)
	resolvedConfigWithEnv, err := ApplyRuntimeConfigEnvOverrides(resolvedConfig)
	if err != nil {
		return Config{}, err
	}
	resolvedConfigWithEnv.RuntimeConfigFilePath = strings.TrimSpace(userConfigFilePath)
	resolvedConfigWithEnv.RuntimeBaseConfigFilePath = strings.TrimSpace(baseConfigFilePath)
	return resolvedConfigWithEnv, nil
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
	if renameErr := os.Rename(tempFilePath, absoluteConfigFilePath); renameErr != nil {
		return fmt.Errorf("write runtime config bytes: replace target file failed: %w", renameErr)
	}
	return nil
}

func buildRuntimeConfigFieldSources(baseLayer map[string]any, userLayer map[string]any) (map[string]any, error) {
	envOverrideKeys, err := runtimeEnvOverrideKeys()
	if err != nil {
		return nil, err
	}
	fieldSources := make(map[string]any, len(runtimeConfigFieldYAMLPaths))
	for fieldKey, yamlPath := range runtimeConfigFieldYAMLPaths {
		if _, exists := envOverrideKeys[fieldKey]; exists {
			fieldSources[fieldKey] = runtimeConfigSourceEnv
			continue
		}
		if _, exists := readRuntimeConfigYAMLPath(userLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceUser
			continue
		}
		if _, exists := readRuntimeConfigYAMLPath(baseLayer, yamlPath); exists {
			fieldSources[fieldKey] = runtimeConfigSourceSystem
			continue
		}
		fieldSources[fieldKey] = runtimeConfigSourceDefault
	}
	return fieldSources, nil
}

func buildEditableRuntimeConfigPatch(userLayer map[string]any) map[string]any {
	if len(userLayer) == 0 {
		return map[string]any{}
	}
	editablePatch := map[string]any{}
	visitedYAMLPaths := map[string]struct{}{}
	for _, yamlPath := range runtimeConfigFieldYAMLPaths {
		if _, visited := visitedYAMLPaths[yamlPath]; visited {
			continue
		}
		visitedYAMLPaths[yamlPath] = struct{}{}
		fieldValue, exists := readRuntimeConfigYAMLPath(userLayer, yamlPath)
		if exists != true {
			continue
		}
		setRuntimeConfigYAMLPath(editablePatch, yamlPath, cloneRuntimeConfigLayerValue(fieldValue))
	}
	return editablePatch
}

func buildEditableRuntimeConfigRestorePreview(
	baseConfigFilePath string,
	baseLayer map[string]any,
	userLayer map[string]any,
) (map[string]any, error) {
	restorePreview := map[string]any{}
	if len(userLayer) == 0 {
		return restorePreview, nil
	}
	fallbackConfig, err := buildRuntimeConfigFromLayers(baseConfigFilePath, baseLayer, "", map[string]any{})
	if err != nil {
		return nil, err
	}
	fallbackSources, err := buildRuntimeConfigFieldSources(baseLayer, map[string]any{})
	if err != nil {
		return nil, err
	}
	for fieldKey, yamlPath := range runtimeConfigFieldYAMLPaths {
		if _, exists := readRuntimeConfigYAMLPath(userLayer, yamlPath); exists != true {
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
	case "control_plane.listen_addr":
		return runtimeConfig.ControlPlane.ListenAddr
	case "control_plane.grpc_h2_listen_addr":
		return runtimeConfig.ControlPlane.GRPCH2ListenAddr
	case "control_plane.heartbeat_timeout_ms":
		return uint64(runtimeConfig.ControlPlane.HeartbeatTimeout.Milliseconds())
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
	yamlPath, exists := runtimeConfigFieldYAMLPaths[normalizedPatchKey]
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
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_cert_ttl")
	}
	serverCertRenewBefore, applied, err := lookupDurationEnv(os.LookupEnv, envControlPlaneTLSServerCertRenewBefore)
	if err != nil {
		return err
	}
	if applied {
		runtimeConfig.ControlPlane.TLSServerCertRenewBefore = serverCertRenewBefore
		markRuntimeConfigFieldApplied(appliedFieldKeys, "control_plane.tls_server_cert_renew_before")
	}
	return nil
}

func applyRuntimeConfigPatch(configCandidate *Config, userLayer map[string]any, patchKey string, patchValue any) error {
	if configCandidate == nil {
		return errors.New("apply runtime config patch: nil config")
	}
	if userLayer == nil {
		return errors.New("apply runtime config patch: nil user layer")
	}
	normalizedPatchKey := strings.TrimSpace(patchKey)
	if patchValue == nil {
		yamlPath, exists := lookupRuntimeConfigPatchYAMLPath(normalizedPatchKey)
		if exists != true {
			return fmt.Errorf("unsupported patch key=%s", normalizedPatchKey)
		}
		deleteRuntimeConfigYAMLPath(userLayer, yamlPath)
		return nil
	}
	switch normalizedPatchKey {
	case "ingress.http_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.HTTPAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.grpc_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.GRPCAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.https_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.HTTPSAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.tls_sni_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.TLSSNIAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "ingress.tcp_port_range":
		portRange, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.TCPPortRange = portRange
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], portRange)
	case "ingress.base_domain":
		baseDomain, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Ingress.BaseDomain = baseDomain
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], baseDomain)
	case "admin.enabled":
		enabled, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.Enabled = enabled
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], enabled)
	case "admin.listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "admin.allow_shared_listener":
		allowSharedListener, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.AllowSharedListener = allowSharedListener
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], allowSharedListener)
	case "admin.ui_enabled":
		enabled, err := parsePatchBool(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Admin.UIEnabled = enabled
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], enabled)
	case "admin.base_path":
		basePath, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		normalizedBasePath := normalizeAdminUIBasePath(basePath)
		configCandidate.Admin.BasePath = normalizedBasePath
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], normalizedBasePath)
	case "control_plane.listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "control_plane.grpc_h2_listen_addr":
		listenAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.GRPCH2ListenAddr = listenAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], listenAddr)
	case "control_plane.heartbeat_timeout":
		heartbeatTimeout, err := parsePatchDuration(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.HeartbeatTimeout = heartbeatTimeout
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths["control_plane.heartbeat_timeout_ms"], heartbeatTimeout.String())
	case "control_plane.heartbeat_timeout_ms":
		heartbeatTimeout, err := parsePatchDurationMillis(patchValue)
		if err != nil {
			return err
		}
		configCandidate.ControlPlane.HeartbeatTimeout = heartbeatTimeout
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], heartbeatTimeout.String())
	case "observability.log_level":
		logLevel, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		normalizedLogLevel := strings.TrimSpace(logLevel)
		configCandidate.Observability.LogLevel = normalizedLogLevel
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], normalizedLogLevel)
	case "observability.metrics_addr":
		metricsAddr, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.Observability.MetricsAddr = metricsAddr
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], metricsAddr)
	case "default_scope.namespace":
		namespace, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.DefaultScope.Namespace = namespace
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], namespace)
	case "default_scope.environment":
		environment, err := parsePatchString(patchValue)
		if err != nil {
			return err
		}
		configCandidate.DefaultScope.Environment = environment
		setRuntimeConfigYAMLPath(userLayer, runtimeConfigFieldYAMLPaths[patchKey], environment)
	default:
		return fmt.Errorf("unsupported patch key=%s", patchKey)
	}
	return nil
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
		if ok != true {
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
	if ok != true {
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
		if ok != true {
			return nil, false
		}
		nextValue, exists := currentRecord[segment]
		if exists != true {
			return nil, false
		}
		current = nextValue
	}
	return current, true
}

func lookupNonEmptyEnv(lookupEnv envLookupFn, key string) (string, bool) {
	rawValue, exists := lookupEnv(key)
	if exists != true {
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
	if exists != true {
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
	if exists != true {
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
	if exists != true {
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
