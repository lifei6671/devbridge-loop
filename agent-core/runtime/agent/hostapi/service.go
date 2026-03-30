package hostapi

import (
	"context"
	"encoding/json"
	"fmt"
)

type Service struct {
	runtime RuntimeHost
}

func NewService(runtime RuntimeHost) *Service {
	return &Service{runtime: runtime}
}

func (service *Service) Handle(ctx context.Context, request Request) (Response, *Failure) {
	if service == nil || service.runtime == nil {
		return Response{}, &Failure{Code: "INTERNAL_ERROR", Message: "hostapi runtime is not configured"}
	}
	switch request.Method {
	case MethodAppBootstrap, MethodAgentSnapshot:
		payload, err := service.runtime.AgentSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodAppShutdown:
		if err := service.runtime.Shutdown(ctx); err != nil {
			return Response{}, &Failure{Code: "INTERNAL_ERROR", Message: err.Error()}
		}
		return Response{
			Method: request.Method,
			Payload: map[string]any{
				"accepted": true,
			},
		}, nil
	case MethodSessionSnapshot:
		payload, err := service.runtime.SessionSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodSessionReconnect:
		if err := service.runtime.RequestBridgeReconnect(ctx, true); err != nil {
			return Response{}, &Failure{Code: "INTERNAL_ERROR", Message: err.Error()}
		}
		payload, err := service.runtime.SessionSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodSessionDrain:
		if err := service.runtime.RequestBridgeDrain(ctx); err != nil {
			return Response{}, &Failure{Code: "INTERNAL_ERROR", Message: err.Error()}
		}
		payload, err := service.runtime.SessionSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodServiceAdd:
		payload, failure := service.handleServiceAdd(ctx, request.Payload)
		return Response{Method: request.Method, Payload: payload}, failure
	case MethodServiceList:
		payload, err := service.runtime.ListServices(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodServiceDelete:
		payload, failure := service.handleServiceDelete(ctx, request.Payload)
		return Response{Method: request.Method, Payload: payload}, failure
	case MethodTunnelList:
		payload, err := service.runtime.ListTunnels(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodTrafficStatsSnapshot:
		payload, err := service.runtime.TrafficStatsSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodConfigSnapshot:
		payload, err := service.runtime.ConfigSnapshot(ctx, ConfigSnapshotInput{})
		return mapResponse(request.Method, payload, err)
	case MethodConfigUpdate:
		payload, failure := service.handleConfigUpdate(ctx, request.Payload)
		return Response{Method: request.Method, Payload: payload}, failure
	case MethodDiagnoseSnapshot:
		payload, err := service.runtime.DiagnoseSnapshot(ctx)
		return mapResponse(request.Method, payload, err)
	case MethodDiagnoseLogs:
		payload, err := service.runtime.DiagnoseLogs(ctx)
		return mapResponse(request.Method, payload, err)
	default:
		return Response{}, &Failure{
			Code:    "METHOD_NOT_ALLOWED",
			Message: fmt.Sprintf("method %s is not allowed", request.Method),
		}
	}
}

func (service *Service) handleServiceAdd(ctx context.Context, rawPayload json.RawMessage) (map[string]any, *Failure) {
	payload, err := decodePayload[serviceAddPayload](rawPayload)
	if err != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: "invalid service.add payload"}
	}
	response, addErr := service.runtime.AddService(ctx, payload.toInput())
	if addErr != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: addErr.Error()}
	}
	return response, nil
}

func (service *Service) handleServiceDelete(ctx context.Context, rawPayload json.RawMessage) (map[string]any, *Failure) {
	payload, err := decodePayload[serviceDeletePayload](rawPayload)
	if err != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: "invalid service.delete payload"}
	}
	response, deleteErr := service.runtime.DeleteService(ctx, payload.toInput())
	if deleteErr != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: deleteErr.Error()}
	}
	return response, nil
}

func (service *Service) handleConfigUpdate(ctx context.Context, rawPayload json.RawMessage) (map[string]any, *Failure) {
	payload, err := decodePayload[configUpdatePayload](rawPayload)
	if err != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: "invalid config.update payload"}
	}
	if len(payload.Config) == 0 {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: "config.update requires config payload"}
	}
	response, updateErr := service.runtime.UpdateConfig(ctx, payload.toInput())
	if updateErr != nil {
		return nil, &Failure{Code: "INVALID_REQUEST", Message: updateErr.Error()}
	}
	return response, nil
}

func mapResponse(method Method, payload map[string]any, err error) (Response, *Failure) {
	if err != nil {
		return Response{}, &Failure{Code: "INTERNAL_ERROR", Message: err.Error()}
	}
	return Response{Method: method, Payload: payload}, nil
}

func decodePayload[T any](payload json.RawMessage) (T, error) {
	var value T
	if len(payload) == 0 {
		return value, nil
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, err
	}
	return value, nil
}
