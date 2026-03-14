package app

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// adminRuntimeConfigStore 管理管理面配置快照与乐观并发版本号。
type adminRuntimeConfigStore struct {
	mutex sync.RWMutex

	runtimeConfig  Config
	configVersion  uint64
	lastUpdatedAt  time.Time
	lastOperatorID string
}

// newAdminRuntimeConfigStore 构建管理面配置快照存储。
func newAdminRuntimeConfigStore(config Config) *adminRuntimeConfigStore {
	return &adminRuntimeConfigStore{
		runtimeConfig: config,
		configVersion: 1,
		lastUpdatedAt: time.Now().UTC(),
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
	store.mutex.RUnlock()
	return buildAdminConfigSnapshot(configCopy, configVersion, lastUpdatedAt, lastOperatorID)
}

// buildAdminConfigSnapshot 把配置结构转换为管理面稳定快照结构。
func buildAdminConfigSnapshot(
	configCopy Config,
	configVersion uint64,
	lastUpdatedAt time.Time,
	lastOperatorID string,
) map[string]any {
	authTokens := make([]map[string]any, 0, len(configCopy.Admin.AuthTokens))
	for _, tokenConfig := range configCopy.Admin.AuthTokens {
		authTokens = append(authTokens, map[string]any{
			"name":  strings.TrimSpace(tokenConfig.Name),
			"role":  strings.TrimSpace(tokenConfig.Role),
			"token": maskAdminToken(tokenConfig.Token),
		})
	}
	return map[string]any{
		"config_version": configVersion,
		"ingress": map[string]any{
			"http_addr":      configCopy.Ingress.HTTPAddr,
			"grpc_addr":      configCopy.Ingress.GRPCAddr,
			"https_addr":     configCopy.Ingress.HTTPSAddr,
			"tls_sni_addr":   configCopy.Ingress.TLSSNIAddr,
			"tcp_port_range": configCopy.Ingress.TCPPortRange,
		},
		"admin": map[string]any{
			"enabled":     configCopy.Admin.Enabled,
			"listen_addr": configCopy.Admin.ListenAddr,
			"base_path":   normalizeAdminUIBasePath(configCopy.Admin.BasePath),
			"ui_enabled":  configCopy.Admin.UIEnabled,
			"auth_mode":   configCopy.Admin.AuthMode,
			"auth_tokens": authTokens,
		},
		"control_plane": map[string]any{
			"listen_addr":          configCopy.ControlPlane.ListenAddr,
			"grpc_h2_listen_addr":  configCopy.ControlPlane.GRPCH2ListenAddr,
			"heartbeat_timeout_ms": uint64(configCopy.ControlPlane.HeartbeatTimeout.Milliseconds()),
		},
		"observability": map[string]any{
			"log_level":    configCopy.Observability.LogLevel,
			"metrics_addr": configCopy.Observability.MetricsAddr,
		},
		"updated_at_ms": uint64(lastUpdatedAt.UnixMilli()),
		"updated_by":    strings.TrimSpace(lastOperatorID),
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
	// 通过版本自增表达“已执行一次受控重载请求”。
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
	// 仅允许白名单字段，避免管理接口变成任意配置写入器。
	configCandidate := store.runtimeConfig
	for _, patchKey := range sortedPatchKeys(request.Patch) {
		patchValue := request.Patch[patchKey]
		switch patchKey {
		case "admin.ui_enabled":
			enabled, err := parsePatchBool(patchValue)
			if err != nil {
				return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, err)
			}
			configCandidate.Admin.UIEnabled = enabled
		case "admin.base_path":
			basePath, err := parsePatchString(patchValue)
			if err != nil {
				return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, err)
			}
			configCandidate.Admin.BasePath = normalizeAdminUIBasePath(basePath)
		case "observability.log_level":
			logLevel, err := parsePatchString(patchValue)
			if err != nil {
				return adminapi.ConfigUpdateResult{}, fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, err)
			}
			configCandidate.Observability.LogLevel = strings.TrimSpace(logLevel)
		default:
			return adminapi.ConfigUpdateResult{}, fmt.Errorf(
				"%w: unsupported patch key=%s",
				adminapi.ErrAdminInvalidArgument,
				patchKey,
			)
		}
	}
	store.runtimeConfig = configCandidate
	store.configVersion++
	store.lastUpdatedAt = normalizedNow
	store.lastOperatorID = strings.TrimSpace(actor)
	configCopy := store.runtimeConfig
	configVersion := store.configVersion
	lastUpdatedAt := store.lastUpdatedAt
	lastOperatorID := store.lastOperatorID
	return adminapi.ConfigUpdateResult{
		ConfigVersion:   configVersion,
		Snapshot:        buildAdminConfigSnapshot(configCopy, configVersion, lastUpdatedAt, lastOperatorID),
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
	case registry.SessionStale, registry.SessionClosed:
		currentState = sessionRuntime.State
		resultLabel = "already_terminal"
	default:
		currentState = registry.SessionDraining
		resultLabel = "drained"
	}

	updatedServiceCount := 0
	if shouldDrainConnectorServices(dataPlane.sessionRegistry, sessionRuntime) && dataPlane.serviceRegistry != nil {
		// 仅在目标 session 仍是 connector 当前会话时才批量摘流，避免影响新代会话。
		updatedServiceCount = dataPlane.serviceRegistry.MarkLifecycleByConnector(
			normalizedNow,
			sessionRuntime.ConnectorID,
			pb.ServiceStatusInactive,
			pb.HealthStatusUnknown,
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
	value, ok := rawValue.(bool)
	if !ok {
		return false, fmt.Errorf("expect bool value, got=%T", rawValue)
	}
	return value, nil
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

// sortedPatchKeys 返回排序后的 patch key，保证处理顺序可预测。
func sortedPatchKeys(patch map[string]any) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}
