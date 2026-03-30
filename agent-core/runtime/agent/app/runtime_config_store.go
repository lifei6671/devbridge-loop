package app

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type agentRuntimeConfigStore struct {
	mutex sync.RWMutex

	runtimeConfig    Config
	runtimeLayers    runtimeConfigLayerMaps
	saveConfigFunc   func(layer map[string]any, configFilePath string) error
	configVersion    uint64
	lastUpdatedAt    time.Time
	lastUpdatedBy    string
	lastIpcTransport string
	lastIpcEndpoint  string
}

func newAgentRuntimeConfigStore(config Config) *agentRuntimeConfigStore {
	userConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeConfigFilePath))
	systemConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeSystemConfigFilePath))
	localConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeLocalConfigFilePath))
	explicitConfigLayer, _ := maybeLoadRuntimeConfigLayerMap(strings.TrimSpace(config.RuntimeExplicitConfigFilePath))
	return &agentRuntimeConfigStore{
		runtimeConfig: config,
		runtimeLayers: runtimeConfigLayerMaps{
			systemConfigFilePath:   strings.TrimSpace(config.RuntimeSystemConfigFilePath),
			systemLayer:            cloneRuntimeConfigLayerMap(systemConfigLayer),
			userConfigFilePath:     strings.TrimSpace(config.RuntimeConfigFilePath),
			userLayer:              cloneRuntimeConfigLayerMap(userConfigLayer),
			localConfigFilePath:    strings.TrimSpace(config.RuntimeLocalConfigFilePath),
			localLayer:             cloneRuntimeConfigLayerMap(localConfigLayer),
			explicitConfigFilePath: strings.TrimSpace(config.RuntimeExplicitConfigFilePath),
			explicitLayer:          cloneRuntimeConfigLayerMap(explicitConfigLayer),
		},
		saveConfigFunc: saveRuntimeConfigLayerMapToFile,
		configVersion:  1,
		lastUpdatedAt:  time.Now().UTC(),
	}
}

func (store *agentRuntimeConfigStore) currentConfig() Config {
	if store == nil {
		return Config{}
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return store.runtimeConfig
}

func (store *agentRuntimeConfigStore) snapshot(ipcTransport string, ipcEndpoint string) map[string]any {
	if store == nil {
		return map[string]any{}
	}
	trimmedIpcTransport := strings.TrimSpace(ipcTransport)
	trimmedIpcEndpoint := strings.TrimSpace(ipcEndpoint)

	store.mutex.Lock()
	configCopy := store.runtimeConfig
	configVersion := store.configVersion
	lastUpdatedAt := store.lastUpdatedAt
	lastUpdatedBy := store.lastUpdatedBy
	if trimmedIpcTransport != "" {
		store.lastIpcTransport = trimmedIpcTransport
	}
	if trimmedIpcEndpoint != "" {
		store.lastIpcEndpoint = trimmedIpcEndpoint
	}
	effectiveIpcTransport := store.lastIpcTransport
	effectiveIpcEndpoint := store.lastIpcEndpoint
	store.mutex.Unlock()

	editableTarget, err := resolveEditableRuntimeConfigTarget(configCopy)
	if err != nil {
		editableTarget = runtimeConfigEditableTarget{}
	}
	configDocument, err := buildPersistedConfigDocumentMap(configCopy)
	if err != nil {
		configDocument = map[string]any{}
	}
	redactSensitiveConfigSecrets(configDocument)
	return map[string]any{
		"config_version":               configVersion,
		"config_file_path":             strings.TrimSpace(editableTarget.path),
		"config_file_source":           strings.TrimSpace(editableTarget.source),
		"base_config_file_path":        strings.TrimSpace(configCopy.RuntimeBaseConfigFilePath),
		"runtime_config_file_path":     strings.TrimSpace(configCopy.RuntimeConfigFilePath),
		"runtime_local_config_path":    strings.TrimSpace(configCopy.RuntimeLocalConfigFilePath),
		"runtime_system_config_path":   strings.TrimSpace(configCopy.RuntimeSystemConfigFilePath),
		"runtime_explicit_config_path": strings.TrimSpace(configCopy.RuntimeExplicitConfigFilePath),
		"updated_at_ms":                uint64(lastUpdatedAt.UnixMilli()),
		"updated_by":                   strings.TrimSpace(lastUpdatedBy),
		"reload_required":              true,
		"applied_to_runtime":           false,
		"config":                       configDocument,
		"agent_id":                     configCopy.AgentID,
		"bridge_addr":                  configCopy.BridgeAddr,
		"bridge_transport":             configCopy.BridgeTransport,
		"tunnel_pool_min_idle":         configCopy.TunnelPool.MinIdle,
		"tunnel_pool_max_idle":         configCopy.TunnelPool.MaxIdle,
		"tunnel_pool_max_inflight":     configCopy.TunnelPool.MaxInflight,
		"tunnel_pool_ttl_ms":           durationToMillis(configCopy.TunnelPool.TTL),
		"tunnel_pool_max_reuse":        configCopy.TunnelPool.MaxReuse,
		"tunnel_pool_recycle_ack_ms":   durationToMillis(configCopy.TunnelPool.RecycleAckTO),
		"tunnel_pool_open_rate":        configCopy.TunnelPool.OpenRate,
		"tunnel_pool_open_burst":       configCopy.TunnelPool.OpenBurst,
		"tunnel_pool_reconcile_gap_ms": durationToMillis(configCopy.TunnelPool.ReconcileGap),
		"ipc_transport":                effectiveIpcTransport,
		"ipc_endpoint":                 effectiveIpcEndpoint,
		"source":                       "agent.runtime.config.store",
	}
}

func (store *agentRuntimeConfigStore) update(configCandidate Config, actor string) (map[string]any, error) {
	if store == nil {
		return nil, fmt.Errorf("update config store: store is nil")
	}
	store.mutex.Lock()
	currentConfig := store.runtimeConfig

	configCandidate.RuntimeConfigFilePath = currentConfig.RuntimeConfigFilePath
	configCandidate.RuntimeBaseConfigFilePath = currentConfig.RuntimeBaseConfigFilePath
	configCandidate.RuntimeSystemConfigFilePath = currentConfig.RuntimeSystemConfigFilePath
	configCandidate.RuntimeLocalConfigFilePath = currentConfig.RuntimeLocalConfigFilePath
	configCandidate.RuntimeExplicitConfigFilePath = currentConfig.RuntimeExplicitConfigFilePath
	if strings.TrimSpace(configCandidate.Session.AuthToken) == "" {
		configCandidate.Session.AuthToken = currentConfig.Session.AuthToken
	}
	if strings.TrimSpace(configCandidate.UI.Web.Auth.Password) == "" {
		configCandidate.UI.Web.Auth.Password = currentConfig.UI.Web.Auth.Password
	}
	configCandidate = configCandidate.Normalize()
	if err := configCandidate.Validate(); err != nil {
		store.mutex.Unlock()
		return nil, err
	}

	editableTarget, err := resolveEditableRuntimeConfigTarget(configCandidate)
	if err != nil {
		store.mutex.Unlock()
		return nil, err
	}
	if strings.TrimSpace(editableTarget.path) == "" {
		store.mutex.Unlock()
		return nil, fmt.Errorf("no editable config file available")
	}
	currentConfigDocument, err := buildPersistedConfigDocumentMap(currentConfig)
	if err != nil {
		store.mutex.Unlock()
		return nil, fmt.Errorf("build current config document failed: %w", err)
	}
	candidateConfigDocument, err := buildPersistedConfigDocumentMap(configCandidate)
	if err != nil {
		store.mutex.Unlock()
		return nil, fmt.Errorf("build candidate config document failed: %w", err)
	}
	changedLayer := diffRuntimeConfigLayerMap(currentConfigDocument, candidateConfigDocument)
	editableLayerCandidate := cloneRuntimeConfigLayerMap(store.runtimeLayerBySource(editableTarget.source))
	editableLayerCandidate = mergeRuntimeConfigLayerMap(editableLayerCandidate, changedLayer)
	if store.saveConfigFunc != nil {
		if err := store.saveConfigFunc(editableLayerCandidate, editableTarget.path); err != nil {
			store.mutex.Unlock()
			return nil, fmt.Errorf("persist config file failed: %w", err)
		}
	}
	store.setRuntimeLayerBySource(editableTarget.source, editableLayerCandidate)

	store.runtimeConfig = configCandidate
	store.configVersion++
	store.lastUpdatedAt = time.Now().UTC()
	store.lastUpdatedBy = strings.TrimSpace(actor)
	lastIpcTransport := store.lastIpcTransport
	lastIpcEndpoint := store.lastIpcEndpoint
	store.mutex.Unlock()
	return store.snapshot(lastIpcTransport, lastIpcEndpoint), nil
}

func (store *agentRuntimeConfigStore) runtimeLayerBySource(source string) map[string]any {
	if store == nil {
		return map[string]any{}
	}
	switch strings.TrimSpace(source) {
	case runtimeConfigSourceExplicit:
		return store.runtimeLayers.explicitLayer
	case runtimeConfigSourceLocal:
		return store.runtimeLayers.localLayer
	case runtimeConfigSourceUser:
		return store.runtimeLayers.userLayer
	case runtimeConfigSourceSystem:
		return store.runtimeLayers.systemLayer
	default:
		return map[string]any{}
	}
}

func (store *agentRuntimeConfigStore) setRuntimeLayerBySource(source string, layer map[string]any) {
	if store == nil {
		return
	}
	switch strings.TrimSpace(source) {
	case runtimeConfigSourceExplicit:
		store.runtimeLayers.explicitLayer = cloneRuntimeConfigLayerMap(layer)
	case runtimeConfigSourceLocal:
		store.runtimeLayers.localLayer = cloneRuntimeConfigLayerMap(layer)
	case runtimeConfigSourceUser:
		store.runtimeLayers.userLayer = cloneRuntimeConfigLayerMap(layer)
	case runtimeConfigSourceSystem:
		store.runtimeLayers.systemLayer = cloneRuntimeConfigLayerMap(layer)
	}
}

func redactSensitiveConfigSecrets(configDocument map[string]any) {
	if configDocument == nil {
		return
	}
	sessionDocument, ok := configDocument["session"].(map[string]any)
	if ok {
		sessionDocument["auth_token"] = ""
	}
	uiDocument, ok := configDocument["ui"].(map[string]any)
	if !ok {
		return
	}
	webDocument, ok := uiDocument["web"].(map[string]any)
	if !ok {
		return
	}
	authDocument, ok := webDocument["auth"].(map[string]any)
	if !ok {
		return
	}
	authDocument["password"] = ""
}
