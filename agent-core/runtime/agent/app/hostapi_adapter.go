package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/hostapi"
)

type runtimeHostAPI struct {
	runtime      *Runtime
	ipcTransport string
	ipcEndpoint  string
}

func newRuntimeHostAPI(runtimeInstance *Runtime, ipcTransport string, ipcEndpoint string) *runtimeHostAPI {
	return &runtimeHostAPI{
		runtime:      runtimeInstance,
		ipcTransport: ipcTransport,
		ipcEndpoint:  ipcEndpoint,
	}
}

func (api *runtimeHostAPI) AgentSnapshot(context.Context) (map[string]any, error) {
	return api.runtime.agentSnapshotPayload(), nil
}

func (api *runtimeHostAPI) SessionSnapshot(context.Context) (map[string]any, error) {
	return api.runtime.sessionSnapshotPayload(), nil
}

func (api *runtimeHostAPI) RequestBridgeReconnect(context.Context, bool) error {
	api.runtime.requestBridgeReconnect(true)
	return nil
}

func (api *runtimeHostAPI) RequestBridgeDrain(context.Context) error {
	api.runtime.requestBridgeDrain()
	return nil
}

func (api *runtimeHostAPI) Shutdown(ctx context.Context) error {
	return api.runtime.Shutdown(ctx)
}

func (api *runtimeHostAPI) AddService(_ context.Context, input hostapi.AddServiceInput) (map[string]any, error) {
	return api.runtime.addOrUpdateService(runtimeServiceAddInput{
		InstanceID:             input.InstanceID,
		Scope:                  input.Scope,
		ServiceName:            input.ServiceName,
		Protocol:               input.Protocol,
		Host:                   input.Host,
		Port:                   input.Port,
		SNIName:                input.SNIName,
		Exposure:               input.Exposure,
		HealthCheckIntervalSec: input.HealthCheckIntervalSec,
		HealthCheckMode:        input.HealthCheckMode,
		HealthCheckPath:        input.HealthCheckPath,
		RouteHint:              input.RouteHint,
	})
}

func (api *runtimeHostAPI) ListServices(context.Context) (map[string]any, error) {
	return api.runtime.serviceListPayload(), nil
}

func (api *runtimeHostAPI) DeleteService(_ context.Context, input hostapi.DeleteServiceInput) (map[string]any, error) {
	return api.runtime.removeService(runtimeServiceDeleteInput{
		LogicalServiceID: input.LogicalServiceID,
		InstanceID:       input.InstanceID,
	})
}

func (api *runtimeHostAPI) ListTunnels(context.Context) (map[string]any, error) {
	return api.runtime.tunnelListPayload(), nil
}

func (api *runtimeHostAPI) TrafficStatsSnapshot(context.Context) (map[string]any, error) {
	return api.runtime.trafficStatsSnapshotPayload(), nil
}

func (api *runtimeHostAPI) ConfigSnapshot(context.Context, hostapi.ConfigSnapshotInput) (map[string]any, error) {
	if api.runtime != nil && api.runtime.configStore != nil {
		return api.runtime.configStore.snapshot(api.ipcTransport, api.ipcEndpoint), nil
	}
	return api.runtime.configSnapshotPayload(api.ipcTransport, api.ipcEndpoint), nil
}

func (api *runtimeHostAPI) UpdateConfig(_ context.Context, input hostapi.ConfigUpdateInput) (map[string]any, error) {
	if api == nil || api.runtime == nil || api.runtime.configStore == nil {
		return nil, fmt.Errorf("update config: runtime config store is not initialized")
	}
	baseConfig := api.runtime.configStore.currentConfig()
	if err := applyConfigDocumentJSON(&baseConfig, input.Config); err != nil {
		return nil, err
	}
	if _, err := api.runtime.configStore.update(baseConfig, input.UpdatedBy); err != nil {
		return nil, err
	}
	return api.runtime.configStore.snapshot(api.ipcTransport, api.ipcEndpoint), nil
}

func applyConfigDocumentJSON(config *Config, rawDocument json.RawMessage) error {
	if config == nil {
		return fmt.Errorf("apply config document: nil config")
	}
	if len(rawDocument) == 0 {
		return fmt.Errorf("apply config document: empty config payload")
	}
	layer := map[string]any{}
	if err := json.Unmarshal(rawDocument, &layer); err != nil {
		return fmt.Errorf("apply config document: decode json failed: %w", err)
	}
	if len(layer) == 0 {
		return fmt.Errorf("apply config document: empty config payload")
	}
	if err := applyRuntimeConfigLayerMap(config, layer); err != nil {
		return fmt.Errorf("apply config document: %w", err)
	}
	return nil
}

func (api *runtimeHostAPI) DiagnoseSnapshot(context.Context) (map[string]any, error) {
	return api.runtime.diagnoseSnapshotPayload(), nil
}

func (api *runtimeHostAPI) DiagnoseLogs(context.Context) (map[string]any, error) {
	return api.runtime.diagnoseLogsPayload(), nil
}
