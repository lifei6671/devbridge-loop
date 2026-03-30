package app

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	runtimeDefaultConfigFileName = "agent.yaml"

	runtimeConfigSourceDefault  = "default"
	runtimeConfigSourceSystem   = "system"
	runtimeConfigSourceUser     = "user"
	runtimeConfigSourceLocal    = "local"
	runtimeConfigSourceExplicit = "explicit"

	linuxRuntimeConfigDirName   = "devbridge"
	windowsRuntimeConfigDirName = "DevBridge"

	envAgentID                  = "DEV_AGENT_CFG_AGENT_ID"
	envBridgeAddr               = "DEV_AGENT_CFG_BRIDGE_ADDR"
	envBridgeTransport          = "DEV_AGENT_CFG_BRIDGE_TRANSPORT"
	envBridgeTLSEnabled         = "DEV_AGENT_CFG_BRIDGE_TLS_ENABLED"
	envBridgeTLSRootCAFile      = "DEV_AGENT_CFG_BRIDGE_TLS_ROOT_CA_FILE"
	envBridgeTLSServerName      = "DEV_AGENT_CFG_BRIDGE_TLS_SERVER_NAME"
	envBridgeAuthMethod         = "DEV_AGENT_CFG_BRIDGE_AUTH_METHOD"
	envBridgeAuthToken          = "DEV_AGENT_CFG_BRIDGE_AUTH_TOKEN"
	envBridgeClientCapVersion   = "DEV_AGENT_CFG_BRIDGE_CLIENT_CAP_VERSION"
	envTunnelPoolMinIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MIN_IDLE"
	envTunnelPoolMaxIdle        = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_IDLE"
	envTunnelPoolMaxInflight    = "DEV_AGENT_CFG_TUNNEL_POOL_MAX_INFLIGHT"
	envTunnelPoolTTLMS          = "DEV_AGENT_CFG_TUNNEL_POOL_TTL_MS"
	envTunnelPoolOpenRate       = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_RATE"
	envTunnelPoolOpenBurst      = "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_BURST"
	envTunnelPoolReconcileGapMS = "DEV_AGENT_CFG_TUNNEL_POOL_RECONCILE_GAP_MS"
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
	path   string
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

// ApplyRuntimeConfigEnvOverrides 将环境变量覆盖应用到配置副本，并执行 Normalize / Validate。
func ApplyRuntimeConfigEnvOverrides(runtimeConfig Config) (Config, error) {
	resolvedConfig := runtimeConfig
	if err := applyRuntimeConfigEnvOverridesInPlace(&resolvedConfig); err != nil {
		return Config{}, err
	}
	resolvedConfig = resolvedConfig.Normalize()
	if err := resolvedConfig.Validate(); err != nil {
		return Config{}, err
	}
	return resolvedConfig, nil
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
	return "/etc/devbridge/agent.yaml", nil
}

func resolveRuntimeExplicitConfigFilePath(explicitBaseConfigFilePath string) (string, error) {
	normalizedExplicitBaseConfigFilePath := strings.TrimSpace(explicitBaseConfigFilePath)
	if normalizedExplicitBaseConfigFilePath == "" {
		return "", nil
	}
	return filepath.Abs(normalizedExplicitBaseConfigFilePath)
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
	if !configFileExists {
		return map[string]any{}, nil
	}
	return loadRuntimeConfigLayerMapFromFile(normalizedConfigFilePath)
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
	if err := applyRuntimeConfigEnvOverridesInPlace(&resolvedConfig); err != nil {
		return Config{}, err
	}
	if err := applyRuntimeConfigLayerMap(&resolvedConfig, layerMaps.explicitLayer); err != nil {
		return Config{}, err
	}
	resolvedConfig = resolvedConfig.Normalize()
	if err := resolvedConfig.Validate(); err != nil {
		return Config{}, err
	}
	return resolvedConfig, nil
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

func cloneRuntimeConfigLayerMap(layer map[string]any) map[string]any {
	if len(layer) == 0 {
		return map[string]any{}
	}
	clonedLayer := make(map[string]any, len(layer))
	for key, value := range layer {
		clonedLayer[key] = cloneRuntimeConfigLayerValue(value)
	}
	return clonedLayer
}

func cloneRuntimeConfigLayerValue(value any) any {
	switch typedValue := value.(type) {
	case map[string]any:
		return cloneRuntimeConfigLayerMap(typedValue)
	case []any:
		clonedValues := make([]any, 0, len(typedValue))
		for _, item := range typedValue {
			clonedValues = append(clonedValues, cloneRuntimeConfigLayerValue(item))
		}
		return clonedValues
	default:
		return typedValue
	}
}

func mergeRuntimeConfigLayerMap(target map[string]any, overlay map[string]any) map[string]any {
	if target == nil {
		target = map[string]any{}
	}
	for key, value := range overlay {
		overlayMap, overlayIsMap := value.(map[string]any)
		if !overlayIsMap {
			target[key] = cloneRuntimeConfigLayerValue(value)
			continue
		}
		existingMap, _ := target[key].(map[string]any)
		target[key] = mergeRuntimeConfigLayerMap(cloneRuntimeConfigLayerMap(existingMap), overlayMap)
	}
	return target
}

func diffRuntimeConfigLayerMap(current map[string]any, next map[string]any) map[string]any {
	if len(next) == 0 {
		return map[string]any{}
	}
	diff := make(map[string]any)
	for key, nextValue := range next {
		currentValue, exists := current[key]
		if !exists {
			diff[key] = cloneRuntimeConfigLayerValue(nextValue)
			continue
		}
		nextMap, nextIsMap := nextValue.(map[string]any)
		currentMap, currentIsMap := currentValue.(map[string]any)
		if nextIsMap && currentIsMap {
			nestedDiff := diffRuntimeConfigLayerMap(currentMap, nextMap)
			if len(nestedDiff) > 0 {
				diff[key] = nestedDiff
			}
			continue
		}
		if reflect.DeepEqual(currentValue, nextValue) {
			continue
		}
		diff[key] = cloneRuntimeConfigLayerValue(nextValue)
	}
	return diff
}

func applyRuntimeConfigEnvOverridesInPlace(runtimeConfig *Config) error {
	if runtimeConfig == nil {
		return errors.New("apply runtime config env overrides: nil config")
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envAgentID); ok {
		runtimeConfig.AgentID = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeAddr); ok {
		runtimeConfig.BridgeAddr = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeTransport); ok {
		runtimeConfig.BridgeTransport = value
	}
	if value, applied, err := lookupBoolEnv(os.LookupEnv, envBridgeTLSEnabled); err != nil {
		return err
	} else if applied {
		runtimeConfig.BridgeTLS.Enabled = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeTLSRootCAFile); ok {
		runtimeConfig.BridgeTLS.RootCAFile = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeTLSServerName); ok {
		runtimeConfig.BridgeTLS.ServerName = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeAuthMethod); ok {
		runtimeConfig.Session.AuthMethod = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeAuthToken); ok {
		runtimeConfig.Session.AuthToken = value
	}
	if value, ok := lookupNonEmptyEnv(os.LookupEnv, envBridgeClientCapVersion); ok {
		runtimeConfig.Session.ClientCapVersion = value
	}
	if value, applied, err := lookupIntEnv(os.LookupEnv, envTunnelPoolMinIdle); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.MinIdle = value
	}
	if value, applied, err := lookupIntEnv(os.LookupEnv, envTunnelPoolMaxIdle); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.MaxIdle = value
	}
	if value, applied, err := lookupIntEnv(os.LookupEnv, envTunnelPoolMaxInflight); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.MaxInflight = value
	}
	if value, applied, err := lookupDurationFromMSEnv(os.LookupEnv, envTunnelPoolTTLMS); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.TTL = value
	}
	if value, applied, err := lookupFloat64Env(os.LookupEnv, envTunnelPoolOpenRate); err != nil {
		return err
	} else if applied {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s 必须是有限数值", envTunnelPoolOpenRate)
		}
		runtimeConfig.TunnelPool.OpenRate = value
	}
	if value, applied, err := lookupIntEnv(os.LookupEnv, envTunnelPoolOpenBurst); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.OpenBurst = value
	}
	if value, applied, err := lookupDurationFromMSEnv(os.LookupEnv, envTunnelPoolReconcileGapMS); err != nil {
		return err
	} else if applied {
		runtimeConfig.TunnelPool.ReconcileGap = value
	}
	return nil
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
	tempFile, err := os.CreateTemp(configFileDirectory, ".agent-config-*.tmp")
	if err != nil {
		return fmt.Errorf("write runtime config bytes: create temp file failed: %w", err)
	}
	tempFilePath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempFilePath)
	}()
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
	if renameErr := runtimeConfigReplaceFile(tempFilePath, absoluteConfigFilePath); renameErr != nil {
		return fmt.Errorf("write runtime config bytes: replace target file failed: %w", renameErr)
	}
	return nil
}

func resolveEditableRuntimeConfigTarget(config Config) (runtimeConfigEditableTarget, error) {
	explicitConfigEditable, err := isRuntimeConfigTargetEditable(config.RuntimeExplicitConfigFilePath, true)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat explicit config failed: %w", err)
	}
	if explicitConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceExplicit,
			path:   strings.TrimSpace(config.RuntimeExplicitConfigFilePath),
		}, nil
	}
	localConfigEditable, err := isRuntimeConfigTargetEditable(config.RuntimeLocalConfigFilePath, false)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat local config failed: %w", err)
	}
	if localConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceLocal,
			path:   strings.TrimSpace(config.RuntimeLocalConfigFilePath),
		}, nil
	}
	userConfigEditable, err := isRuntimeConfigTargetEditable(config.RuntimeConfigFilePath, true)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat user config failed: %w", err)
	}
	if userConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceUser,
			path:   strings.TrimSpace(config.RuntimeConfigFilePath),
		}, nil
	}
	if strings.TrimSpace(config.RuntimeConfigFilePath) != "" {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceUser,
			path:   strings.TrimSpace(config.RuntimeConfigFilePath),
		}, nil
	}
	systemConfigEditable, err := isRuntimeConfigTargetEditable(config.RuntimeSystemConfigFilePath, false)
	if err != nil {
		return runtimeConfigEditableTarget{}, fmt.Errorf("resolve editable runtime config target: stat system config failed: %w", err)
	}
	if systemConfigEditable {
		return runtimeConfigEditableTarget{
			source: runtimeConfigSourceSystem,
			path:   strings.TrimSpace(config.RuntimeSystemConfigFilePath),
		}, nil
	}
	return runtimeConfigEditableTarget{
		source: runtimeConfigSourceDefault,
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

func lookupNonEmptyEnv(lookupEnv envLookupFn, key string) (string, bool) {
	if lookupEnv == nil {
		return "", false
	}
	rawValue, ok := lookupEnv(key)
	if !ok {
		return "", false
	}
	normalizedValue := strings.TrimSpace(rawValue)
	if normalizedValue == "" {
		return "", false
	}
	return normalizedValue, true
}

func lookupBoolEnv(lookupEnv envLookupFn, key string) (bool, bool, error) {
	value, ok := lookupNonEmptyEnv(lookupEnv, key)
	if !ok {
		return false, false, nil
	}
	parsedValue, err := strconv.ParseBool(value)
	if err != nil {
		return false, false, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, true, nil
}

func lookupIntEnv(lookupEnv envLookupFn, key string) (int, bool, error) {
	value, ok := lookupNonEmptyEnv(lookupEnv, key)
	if !ok {
		return 0, false, nil
	}
	parsedValue, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, true, nil
}

func lookupFloat64Env(lookupEnv envLookupFn, key string) (float64, bool, error) {
	value, ok := lookupNonEmptyEnv(lookupEnv, key)
	if !ok {
		return 0, false, nil
	}
	parsedValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return parsedValue, true, nil
}

func lookupDurationFromMSEnv(lookupEnv envLookupFn, key string) (time.Duration, bool, error) {
	value, ok := lookupNonEmptyEnv(lookupEnv, key)
	if !ok {
		return 0, false, nil
	}
	parsedValue, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("解析 %s 失败: %w", key, err)
	}
	return time.Duration(parsedValue) * time.Millisecond, true, nil
}

func fileExists(filePath string) (bool, error) {
	fileInfo, err := os.Stat(filePath)
	if err == nil {
		return !fileInfo.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat file failed: %w", err)
}

func joinUnixPath(base string, elements ...string) string {
	parts := []string{strings.TrimRight(strings.TrimSpace(base), "/")}
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
	trimmedBase := strings.TrimSpace(base)
	trimmedBase = strings.TrimRight(trimmedBase, `\`)
	trimmedBase = strings.TrimRight(trimmedBase, `/`)
	parts := []string{trimmedBase}
	for _, element := range elements {
		normalizedElement := strings.TrimSpace(element)
		normalizedElement = strings.Trim(normalizedElement, `\`)
		normalizedElement = strings.Trim(normalizedElement, `/`)
		if normalizedElement == "" {
			continue
		}
		parts = append(parts, normalizedElement)
	}
	return strings.Join(parts, `\`)
}
