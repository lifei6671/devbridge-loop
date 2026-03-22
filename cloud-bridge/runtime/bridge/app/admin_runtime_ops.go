package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	bridgecontrol "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/control"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// adminRuntimeConfigStore 管理管理面配置快照与乐观并发版本号。
type adminRuntimeConfigStore struct {
	mutex sync.RWMutex

	runtimeConfig   Config
	configFilePath  string
	baseConfigLayer map[string]any
	userConfigLayer map[string]any
	saveConfigFunc  func(layer map[string]any, configFilePath string) error
	configVersion   uint64
	lastUpdatedAt   time.Time
	lastOperatorID  string
}

// newAdminRuntimeConfigStore 构建管理面配置快照存储。
func newAdminRuntimeConfigStore(config Config) *adminRuntimeConfigStore {
	userConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeConfigFilePath))
	baseConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeBaseConfigFilePath))
	return &adminRuntimeConfigStore{
		runtimeConfig:   config,
		configFilePath:  strings.TrimSpace(config.RuntimeConfigFilePath),
		baseConfigLayer: cloneRuntimeConfigLayerMap(baseConfigLayer),
		userConfigLayer: cloneRuntimeConfigLayerMap(userConfigLayer),
		saveConfigFunc:  saveRuntimeConfigLayerMapToFile,
		configVersion:   1,
		lastUpdatedAt:   time.Now().UTC(),
	}
}

// snapshot 生成当前配置快照（仅管理面读取语义，不直接保证运行态热生效）。
func (store *adminRuntimeConfigStore) snapshot() map[string]any {
	if store == nil {
		return map[string]any{}
	}
	store.mutex.RLock()
	configCopy := store.runtimeConfig
	configVersion := store.configVersion
	lastUpdatedAt := store.lastUpdatedAt
	lastOperatorID := store.lastOperatorID
	baseConfigLayer := cloneRuntimeConfigLayerMap(store.baseConfigLayer)
	userConfigLayer := cloneRuntimeConfigLayerMap(store.userConfigLayer)
	store.mutex.RUnlock()
	return buildAdminConfigSnapshot(configCopy, configVersion, lastUpdatedAt, lastOperatorID, baseConfigLayer, userConfigLayer)
}

// buildAdminConfigSnapshot 把配置结构转换为管理面稳定快照结构。
func buildAdminConfigSnapshot(
	configCopy Config,
	configVersion uint64,
	lastUpdatedAt time.Time,
	lastOperatorID string,
	baseConfigLayer map[string]any,
	userConfigLayer map[string]any,
) map[string]any {
	authTokens := make([]map[string]any, 0, len(configCopy.Admin.AuthTokens))
	for _, tokenConfig := range configCopy.Admin.AuthTokens {
		authTokens = append(authTokens, map[string]any{
			"name":  strings.TrimSpace(tokenConfig.Name),
			"role":  strings.TrimSpace(tokenConfig.Role),
			"token": maskAdminToken(tokenConfig.Token),
		})
	}
	fieldSources, err := buildRuntimeConfigFieldSources(baseConfigLayer, userConfigLayer)
	if err != nil {
		fieldSources = map[string]any{}
	}
	restorePreview, err := buildEditableRuntimeConfigRestorePreview(
		strings.TrimSpace(configCopy.RuntimeBaseConfigFilePath),
		baseConfigLayer,
		userConfigLayer,
	)
	if err != nil {
		restorePreview = map[string]any{}
	}
	return map[string]any{
		"config_version":        configVersion,
		"config_file_path":      strings.TrimSpace(configCopy.RuntimeConfigFilePath),
		"base_config_file_path": strings.TrimSpace(configCopy.RuntimeBaseConfigFilePath),
		"field_sources":         fieldSources,
		"editable_user_patch":   buildEditableRuntimeConfigPatch(userConfigLayer),
		"field_restore_preview": restorePreview,
		"ingress": map[string]any{
			"http_addr":      configCopy.Ingress.HTTPAddr,
			"grpc_addr":      configCopy.Ingress.GRPCAddr,
			"https_addr":     configCopy.Ingress.HTTPSAddr,
			"tls_sni_addr":   configCopy.Ingress.TLSSNIAddr,
			"tcp_port_range": configCopy.Ingress.TCPPortRange,
			"base_domain":    strings.TrimSpace(configCopy.Ingress.BaseDomain),
		},
		"admin": map[string]any{
			"enabled":               configCopy.Admin.Enabled,
			"listen_addr":           configCopy.Admin.ListenAddr,
			"allow_shared_listener": configCopy.Admin.AllowSharedListener,
			"base_path":             normalizeAdminUIBasePath(configCopy.Admin.BasePath),
			"ui_enabled":            configCopy.Admin.UIEnabled,
			"auth_mode":             configCopy.Admin.AuthMode,
			"auth_tokens":           authTokens,
			"cookie_token_name":     strings.TrimSpace(configCopy.Admin.CookieTokenName),
			"csrf_cookie_name":      strings.TrimSpace(configCopy.Admin.CSRFCookieName),
			"csrf_header_name":      strings.TrimSpace(configCopy.Admin.CSRFHeaderName),
			"allowed_origins":       append([]string(nil), configCopy.Admin.AllowedOrigins...),
		},
		"control_plane": map[string]any{
			"listen_addr":                     configCopy.ControlPlane.ListenAddr,
			"grpc_h2_listen_addr":             configCopy.ControlPlane.GRPCH2ListenAddr,
			"heartbeat_timeout_ms":            uint64(configCopy.ControlPlane.HeartbeatTimeout.Milliseconds()),
			"tls_mode":                        configCopy.ControlPlane.TLSMode,
			"tls_cert_source":                 strings.TrimSpace(configCopy.ControlPlane.TLSCertSource),
			"tls_cert_file":                   strings.TrimSpace(configCopy.ControlPlane.TLSCertFile),
			"tls_key_file":                    strings.TrimSpace(configCopy.ControlPlane.TLSKeyFile),
			"tls_ca_cert_file":                strings.TrimSpace(configCopy.ControlPlane.TLSCACertFile),
			"tls_ca_key_file":                 strings.TrimSpace(configCopy.ControlPlane.TLSCAKeyFile),
			"tls_server_common_name":          strings.TrimSpace(configCopy.ControlPlane.TLSServerCommonName),
			"tls_server_san_dns":              append([]string(nil), configCopy.ControlPlane.TLSServerSANDNS...),
			"tls_server_san_ips":              append([]string(nil), configCopy.ControlPlane.TLSServerSANIPs...),
			"tls_server_cert_ttl_ms":          uint64(configCopy.ControlPlane.TLSServerCertTTL.Milliseconds()),
			"tls_server_cert_renew_before_ms": uint64(configCopy.ControlPlane.TLSServerCertRenewBefore.Milliseconds()),
		},
		"observability": map[string]any{
			"log_level":    configCopy.Observability.LogLevel,
			"metrics_addr": configCopy.Observability.MetricsAddr,
		},
		"default_scope": map[string]any{
			"namespace":   strings.TrimSpace(configCopy.DefaultScope.Namespace),
			"environment": strings.TrimSpace(configCopy.DefaultScope.Environment),
		},
		"fallback_policies": buildAdminFallbackPolicySnapshot(configCopy.FallbackPolicies),
		"updated_at_ms":     uint64(lastUpdatedAt.UnixMilli()),
		"updated_by":        strings.TrimSpace(lastOperatorID),
	}
}

// reload 模拟配置重载动作并推进配置版本号，便于审计与并发控制验证。
func (store *adminRuntimeConfigStore) reload(now time.Time, actor string) (adminapi.ReloadConfigResult, error) {
	if store == nil {
		return adminapi.ReloadConfigResult{}, adminapi.ErrAdminOperationNotSupported
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	store.mutex.Lock()
	store.configVersion++
	store.lastUpdatedAt = normalizedNow
	store.lastOperatorID = strings.TrimSpace(actor)
	configVersion := store.configVersion
	store.mutex.Unlock()
	return adminapi.ReloadConfigResult{
		ConfigVersion: configVersion,
		ReloadedAtMS:  uint64(normalizedNow.UnixMilli()),
		Source:        "runtime.config.store",
	}, nil
}

// update 执行带 if_match_version 的配置更新（当前仅支持少量受控字段）。
func (store *adminRuntimeConfigStore) update(
	now time.Time,
	request adminapi.ConfigUpdateRequest,
	actor string,
) (adminapi.ConfigUpdateResult, error) {
	if store == nil {
		return adminapi.ConfigUpdateResult{}, adminapi.ErrAdminOperationNotSupported
	}
	if request.IfMatchVersion == 0 {
		return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: if_match_version is required", adminapi.ErrAdminInvalidArgument)
	}
	if len(request.Patch) == 0 {
		return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: patch is required", adminapi.ErrAdminInvalidArgument)
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	if request.IfMatchVersion != store.configVersion {
		return adminapi.ConfigUpdateResult{}, fmt.Errorf(
			"%w: current=%d requested=%d",
			adminapi.ErrAdminVersionConflict,
			store.configVersion,
			request.IfMatchVersion,
		)
	}
	configCandidate := store.runtimeConfig
	userConfigLayerCandidate := cloneRuntimeConfigLayerMap(store.userConfigLayer)
	for _, patchKey := range sortedPatchKeys(request.Patch) {
		patchValue := request.Patch[patchKey]
		if err := applyRuntimeConfigPatch(&configCandidate, userConfigLayerCandidate, patchKey, patchValue); err != nil {
			return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, err)
		}
	}
	resolvedConfig, err := buildRuntimeConfigFromLayers(
		strings.TrimSpace(store.runtimeConfig.RuntimeBaseConfigFilePath),
		store.baseConfigLayer,
		strings.TrimSpace(store.configFilePath),
		userConfigLayerCandidate,
	)
	if err != nil {
		return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, err)
	}
	configFilePath := strings.TrimSpace(store.configFilePath)
	if configFilePath != "" && store.saveConfigFunc != nil {
		if saveErr := store.saveConfigFunc(userConfigLayerCandidate, configFilePath); saveErr != nil {
			return adminapi.ConfigUpdateResult{}, fmt.Errorf("persist config file failed: %w", saveErr)
		}
	}
	store.userConfigLayer = cloneRuntimeConfigLayerMap(userConfigLayerCandidate)
	store.runtimeConfig = resolvedConfig
	store.configVersion++
	store.lastUpdatedAt = normalizedNow
	store.lastOperatorID = strings.TrimSpace(actor)
	configCopy := store.runtimeConfig
	configVersion := store.configVersion
	lastUpdatedAt := store.lastUpdatedAt
	lastOperatorID := store.lastOperatorID
	baseConfigLayer := cloneRuntimeConfigLayerMap(store.baseConfigLayer)
	userConfigLayer := cloneRuntimeConfigLayerMap(store.userConfigLayer)
	return adminapi.ConfigUpdateResult{
		ConfigVersion:   configVersion,
		Snapshot:        buildAdminConfigSnapshot(configCopy, configVersion, lastUpdatedAt, lastOperatorID, baseConfigLayer, userConfigLayer),
		ApplyMode:       "staged_requires_restart",
		RequiresRestart: true,
	}, nil
}

// drainSessionForAdmin 把 session 标记为 DRAINING，并同步收敛 service/tunnel 状态。
func drainSessionForAdmin(
	now time.Time,
	dataPlane *runtimeDataPlane,
	sessionID string,
	reason string,
) (adminapi.DrainResult, error) {
	if dataPlane == nil || dataPlane.sessionRegistry == nil {
		return adminapi.DrainResult{}, adminapi.ErrAdminOperationNotSupported
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" {
		return adminapi.DrainResult{}, fmt.Errorf("%w: empty session_id", adminapi.ErrAdminInvalidArgument)
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	sessionRuntime, exists := dataPlane.sessionRegistry.GetBySession(normalizedSessionID)
	if !exists {
		return adminapi.DrainResult{}, fmt.Errorf("%w: session_id=%s", adminapi.ErrAdminResourceNotFound, normalizedSessionID)
	}
	previousState := sessionRuntime.State
	currentState := sessionRuntime.State
	resultLabel := "already_terminal"

	switch sessionRuntime.State {
	case registry.SessionActive:
		if !dataPlane.sessionRegistry.MarkState(normalizedNow, normalizedSessionID, registry.SessionDraining) {
			return adminapi.DrainResult{}, fmt.Errorf("%w: session_id=%s", adminapi.ErrAdminResourceNotFound, normalizedSessionID)
		}
		currentState = registry.SessionDraining
		resultLabel = "drained"
	case registry.SessionDraining:
		currentState = registry.SessionDraining
		resultLabel = "already_draining"
	case registry.SessionStale, registry.SessionFailed, registry.SessionClosed:
		currentState = sessionRuntime.State
		resultLabel = "already_terminal"
	default:
		currentState = registry.SessionDraining
		resultLabel = "drained"
	}

	updatedServiceCount := 0
	if shouldDrainConnectorServices(dataPlane.sessionRegistry, sessionRuntime) && dataPlane.serviceRegistry != nil {
		affectedServiceIDs := dataPlane.serviceRegistry.ListLogicalServiceIDsByRuntime(
			sessionRuntime.ConnectorID,
			"",
		)
		// 仅收敛目标 session 对应实例，避免同 connector 下其他会话被误摘流。
		updatedServiceCount = dataPlane.serviceRegistry.MarkLifecycleByConnectorAndSession(
			normalizedNow,
			sessionRuntime.ConnectorID,
			sessionRuntime.SessionID,
			pb.ServiceStatusInactive,
			pb.HealthStatusUnknown,
		)
		// 管理面手动 drain 后立即刷新可用实例快照，防止指标滞后。
		bridgecontrol.RefreshServiceAvailabilityMetricsByServiceIDs(
			dataPlane.connectorMetrics,
			dataPlane.serviceRegistry,
			affectedServiceIDs,
		)
	}
	purgedTunnelCount := 0
	if dataPlane.tunnelRegistry != nil {
		drainReason := strings.TrimSpace(reason)
		if drainReason == "" {
			drainReason = "admin_manual_drain"
		}
		purgedTunnels := dataPlane.tunnelRegistry.PurgeBySession(
			normalizedNow,
			normalizedSessionID,
			"admin_drain:"+drainReason,
		)
		purgedTunnelCount = len(purgedTunnels)
	}
	return adminapi.DrainResult{
		SessionID:           normalizedSessionID,
		ConnectorID:         strings.TrimSpace(sessionRuntime.ConnectorID),
		PreviousState:       string(previousState),
		CurrentState:        string(currentState),
		UpdatedServiceCount: updatedServiceCount,
		PurgedTunnelCount:   purgedTunnelCount,
		Result:              resultLabel,
	}, nil
}

// drainConnectorForAdmin 解析 connector 当前会话并执行 drain。
func drainConnectorForAdmin(
	now time.Time,
	dataPlane *runtimeDataPlane,
	connectorID string,
	reason string,
) (adminapi.DrainResult, error) {
	if dataPlane == nil || dataPlane.sessionRegistry == nil {
		return adminapi.DrainResult{}, adminapi.ErrAdminOperationNotSupported
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return adminapi.DrainResult{}, fmt.Errorf("%w: empty connector_id", adminapi.ErrAdminInvalidArgument)
	}
	sessionRuntime, exists := dataPlane.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !exists {
		return adminapi.DrainResult{}, fmt.Errorf("%w: connector_id=%s", adminapi.ErrAdminResourceNotFound, normalizedConnectorID)
	}
	drainResult, err := drainSessionForAdmin(now, dataPlane, sessionRuntime.SessionID, reason)
	if err != nil {
		return adminapi.DrainResult{}, err
	}
	drainResult.ConnectorID = normalizedConnectorID
	return drainResult, nil
}

// shouldDrainConnectorServices 判断 session 是否仍是 connector 当前会话。
func shouldDrainConnectorServices(
	sessionRegistry *registry.SessionRegistry,
	sessionRuntime registry.SessionRuntime,
) bool {
	if sessionRegistry == nil {
		return false
	}
	normalizedConnectorID := strings.TrimSpace(sessionRuntime.ConnectorID)
	if normalizedConnectorID == "" {
		return false
	}
	currentSession, exists := sessionRegistry.GetByConnector(normalizedConnectorID)
	if !exists {
		return false
	}
	return strings.TrimSpace(currentSession.SessionID) == strings.TrimSpace(sessionRuntime.SessionID) &&
		currentSession.Epoch == sessionRuntime.Epoch
}

// parsePatchBool 解析配置补丁中的布尔值字段。
func parsePatchBool(rawValue any) (bool, error) {
	switch value := rawValue.(type) {
	case bool:
		return value, nil
	case string:
		normalizedValue := strings.ToLower(strings.TrimSpace(value))
		if normalizedValue == "true" || normalizedValue == "1" {
			return true, nil
		}
		if normalizedValue == "false" || normalizedValue == "0" {
			return false, nil
		}
		return false, fmt.Errorf("expect bool value, got=%q", value)
	default:
		return false, fmt.Errorf("expect bool value, got=%T", rawValue)
	}
}

// parsePatchString 解析配置补丁中的字符串字段。
func parsePatchString(rawValue any) (string, error) {
	value, ok := rawValue.(string)
	if !ok {
		return "", fmt.Errorf("expect string value, got=%T", rawValue)
	}
	normalizedValue := strings.TrimSpace(value)
	if normalizedValue == "" {
		return "", fmt.Errorf("string value is empty")
	}
	return normalizedValue, nil
}

// parsePatchDuration 解析配置补丁中的 duration 字符串字段（例如 30s / 1500ms）。
func parsePatchDuration(rawValue any) (time.Duration, error) {
	rawText, err := parsePatchString(rawValue)
	if err != nil {
		return 0, err
	}
	durationValue, parseErr := time.ParseDuration(rawText)
	if parseErr != nil {
		return 0, fmt.Errorf("invalid duration value=%q", rawText)
	}
	if durationValue <= 0 {
		return 0, fmt.Errorf("duration value must be > 0")
	}
	return durationValue, nil
}

// parsePatchDurationMillis 解析以毫秒为单位的时长补丁值。
func parsePatchDurationMillis(rawValue any) (time.Duration, error) {
	switch value := rawValue.(type) {
	case float64:
		if value <= 0 {
			return 0, fmt.Errorf("duration_ms must be > 0")
		}
		return time.Duration(value) * time.Millisecond, nil
	case int:
		if value <= 0 {
			return 0, fmt.Errorf("duration_ms must be > 0")
		}
		return time.Duration(value) * time.Millisecond, nil
	case int64:
		if value <= 0 {
			return 0, fmt.Errorf("duration_ms must be > 0")
		}
		return time.Duration(value) * time.Millisecond, nil
	case string:
		normalizedValue := strings.TrimSpace(value)
		if normalizedValue == "" {
			return 0, fmt.Errorf("duration_ms value is empty")
		}
		parsedInt, parseErr := strconv.ParseInt(normalizedValue, 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("invalid duration_ms value=%q", normalizedValue)
		}
		if parsedInt <= 0 {
			return 0, fmt.Errorf("duration_ms must be > 0")
		}
		return time.Duration(parsedInt) * time.Millisecond, nil
	default:
		return 0, fmt.Errorf("expect number/string duration_ms, got=%T", rawValue)
	}
}

func buildAdminFallbackPolicySnapshot(policies []pb.ScopeFallbackPolicy) []map[string]any {
	if len(policies) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(policies))
	for _, policy := range policies {
		chain := make([]map[string]any, 0, len(policy.Chain))
		for _, step := range policy.Chain {
			chain = append(chain, map[string]any{
				"target_scope": map[string]any{
					"namespace":   strings.TrimSpace(step.TargetScope.Namespace),
					"environment": strings.TrimSpace(step.TargetScope.Environment),
				},
			})
		}
		result = append(result, map[string]any{
			"policy_id": strings.TrimSpace(policy.PolicyID),
			"namespace": strings.TrimSpace(policy.Namespace),
			"enabled":   policy.Enabled,
			"chain":     chain,
			"external": map[string]any{
				"enabled": policy.External.Enabled,
			},
		})
	}
	return result
}

// sortedPatchKeys 返回排序后的 patch key，保证处理顺序可预测。
func sortedPatchKeys(patch map[string]any) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}
