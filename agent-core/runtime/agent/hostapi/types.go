package hostapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type Method string

const (
	MethodAppBootstrap         Method = "app.bootstrap"
	MethodAppShutdown          Method = "app.shutdown"
	MethodAgentSnapshot        Method = "agent.snapshot"
	MethodSessionSnapshot      Method = "session.snapshot"
	MethodSessionReconnect     Method = "session.reconnect"
	MethodSessionDrain         Method = "session.drain"
	MethodServiceAdd           Method = "service.add"
	MethodServiceList          Method = "service.list"
	MethodServiceDelete        Method = "service.delete"
	MethodTunnelList           Method = "tunnel.list"
	MethodTrafficStatsSnapshot Method = "traffic.stats.snapshot"
	MethodConfigSnapshot       Method = "config.snapshot"
	MethodConfigUpdate         Method = "config.update"
	MethodDiagnoseSnapshot     Method = "diagnose.snapshot"
	MethodDiagnoseLogs         Method = "diagnose.logs"
)

type Request struct {
	Method  Method
	Payload json.RawMessage
}

type Response struct {
	Method  Method
	Payload any
}

type Failure struct {
	Code    string
	Message string
}

type Handler interface {
	Handle(ctx context.Context, request Request) (Response, *Failure)
}

type RuntimeHost interface {
	AgentSnapshot(ctx context.Context) (map[string]any, error)
	SessionSnapshot(ctx context.Context) (map[string]any, error)
	RequestBridgeReconnect(ctx context.Context, resetBackoff bool) error
	RequestBridgeDrain(ctx context.Context) error
	Shutdown(ctx context.Context) error
	AddService(ctx context.Context, input AddServiceInput) (map[string]any, error)
	ListServices(ctx context.Context) (map[string]any, error)
	DeleteService(ctx context.Context, input DeleteServiceInput) (map[string]any, error)
	ListTunnels(ctx context.Context) (map[string]any, error)
	TrafficStatsSnapshot(ctx context.Context) (map[string]any, error)
	ConfigSnapshot(ctx context.Context, input ConfigSnapshotInput) (map[string]any, error)
	UpdateConfig(ctx context.Context, input ConfigUpdateInput) (map[string]any, error)
	DiagnoseSnapshot(ctx context.Context) (map[string]any, error)
	DiagnoseLogs(ctx context.Context) (map[string]any, error)
}

type ConfigSnapshotInput struct {
	IPCTransport string
	IPCEndpoint  string
}

type ConfigUpdateInput struct {
	UpdatedBy string
	Config    json.RawMessage
}

type AddServiceInput struct {
	InstanceID             string
	Scope                  pb.Scope
	ServiceName            string
	Protocol               string
	Host                   string
	Port                   uint32
	SNIName                string
	Exposure               pb.ServiceExposure
	HealthCheckIntervalSec uint32
	HealthCheckMode        string
	HealthCheckPath        string
	RouteHint              pb.RouteHint
}

type DeleteServiceInput struct {
	LogicalServiceID string
	InstanceID       string
}

type serviceAddPayload struct {
	InstanceID             string                 `json:"instance_id"`
	Scope                  pb.Scope               `json:"scope"`
	ServiceName            string                 `json:"service_name"`
	Protocol               string                 `json:"protocol"`
	Host                   string                 `json:"host"`
	Port                   uint32                 `json:"port"`
	SNIName                string                 `json:"sni_name"`
	Exposure               serviceExposurePayload `json:"exposure"`
	HealthCheckIntervalSec uint32                 `json:"health_check_interval_sec"`
	HealthCheckMode        string                 `json:"health_check_mode"`
	HealthCheckPath        string                 `json:"health_check_path"`
	RouteHint              routeHintPayload       `json:"route_hint"`
}

type serviceDeletePayload struct {
	LogicalServiceID string `json:"logical_service_id"`
	InstanceID       string `json:"instance_id"`
}

type configUpdatePayload struct {
	UpdatedBy string          `json:"updated_by"`
	Config    json.RawMessage `json:"config"`
}

type serviceExposurePayload struct {
	IngressMode string `json:"ingress_mode"`
	Host        string `json:"host"`
	ListenPort  uint32 `json:"listen_port"`
	SNIName     string `json:"sni_name"`
	PathPrefix  string `json:"path_prefix"`
	AllowExport bool   `json:"allow_export"`
}

type matcherPayload struct {
	Name    string `json:"name"`
	Exact   string `json:"exact,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Regex   string `json:"regex,omitempty"`
	Present *bool  `json:"present,omitempty"`
}

type routeHintPayload struct {
	MatchHeaders []matcherPayload `json:"match_headers,omitempty"`
	MatchQueries []matcherPayload `json:"match_queries,omitempty"`
	Priority     uint32           `json:"priority,omitempty"`
}

func (payload serviceAddPayload) toInput() AddServiceInput {
	return AddServiceInput{
		InstanceID:             strings.TrimSpace(payload.InstanceID),
		Scope:                  payload.Scope,
		ServiceName:            strings.TrimSpace(payload.ServiceName),
		Protocol:               strings.TrimSpace(payload.Protocol),
		Host:                   strings.TrimSpace(payload.Host),
		Port:                   payload.Port,
		SNIName:                strings.TrimSpace(payload.SNIName),
		Exposure:               payload.Exposure.toPB(),
		HealthCheckIntervalSec: payload.HealthCheckIntervalSec,
		HealthCheckMode:        strings.TrimSpace(payload.HealthCheckMode),
		HealthCheckPath:        strings.TrimSpace(payload.HealthCheckPath),
		RouteHint:              payload.RouteHint.toPB(),
	}
}

func (payload serviceDeletePayload) toInput() DeleteServiceInput {
	return DeleteServiceInput{
		LogicalServiceID: strings.TrimSpace(payload.LogicalServiceID),
		InstanceID:       strings.TrimSpace(payload.InstanceID),
	}
}

func (payload configUpdatePayload) toInput() ConfigUpdateInput {
	return ConfigUpdateInput{
		UpdatedBy: strings.TrimSpace(payload.UpdatedBy),
		Config:    append(json.RawMessage(nil), payload.Config...),
	}
}

func (payload serviceExposurePayload) toPB() pb.ServiceExposure {
	return pb.ServiceExposure{
		IngressMode: pb.IngressMode(strings.TrimSpace(payload.IngressMode)),
		Host:        strings.TrimSpace(payload.Host),
		ListenPort:  payload.ListenPort,
		SNIName:     strings.TrimSpace(payload.SNIName),
		PathPrefix:  strings.TrimSpace(payload.PathPrefix),
		AllowExport: payload.AllowExport,
	}
}

func (payload routeHintPayload) toPB() pb.RouteHint {
	matchHeaders := make([]pb.HeaderMatcher, 0, len(payload.MatchHeaders))
	for _, matcher := range payload.MatchHeaders {
		matchHeaders = append(matchHeaders, matcher.toHeaderMatcher())
	}
	matchQueries := make([]pb.QueryMatcher, 0, len(payload.MatchQueries))
	for _, matcher := range payload.MatchQueries {
		matchQueries = append(matchQueries, matcher.toQueryMatcher())
	}
	return pb.RouteHint{
		MatchHeaders: matchHeaders,
		MatchQueries: matchQueries,
		Priority:     payload.Priority,
	}
}

func (payload matcherPayload) toHeaderMatcher() pb.HeaderMatcher {
	return pb.HeaderMatcher{
		Name:    strings.TrimSpace(payload.Name),
		Exact:   strings.TrimSpace(payload.Exact),
		Prefix:  strings.TrimSpace(payload.Prefix),
		Regex:   strings.TrimSpace(payload.Regex),
		Present: payload.Present,
	}
}

func (payload matcherPayload) toQueryMatcher() pb.QueryMatcher {
	return pb.QueryMatcher{
		Name:    strings.TrimSpace(payload.Name),
		Exact:   strings.TrimSpace(payload.Exact),
		Prefix:  strings.TrimSpace(payload.Prefix),
		Regex:   strings.TrimSpace(payload.Regex),
		Present: payload.Present,
	}
}
