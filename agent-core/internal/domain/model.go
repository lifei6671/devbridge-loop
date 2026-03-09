package domain

import "time"

// ErrorCode defines normalized error categories.
type ErrorCode string

const (
	ErrorRouteNotFound        ErrorCode = "ROUTE_NOT_FOUND"
	ErrorRouteExtractFailed   ErrorCode = "ROUTE_EXTRACT_FAILED"
	ErrorTunnelOffline        ErrorCode = "TUNNEL_OFFLINE"
	ErrorLocalEndpointDown    ErrorCode = "LOCAL_ENDPOINT_UNREACHABLE"
	ErrorUpstreamTimeout      ErrorCode = "UPSTREAM_TIMEOUT"
	ErrorInvalidEnv           ErrorCode = "INVALID_ENV"
	ErrorStaleEpoch           ErrorCode = "STALE_EPOCH_EVENT"
	ErrorDuplicateEventIgnore ErrorCode = "DUPLICATE_EVENT_IGNORED"
)

// LocalRegistration stores one local instance registration.
type LocalRegistration struct {
	ServiceName       string            `json:"serviceName"`
	Env               string            `json:"env"`
	InstanceID        string            `json:"instanceId"`
	Metadata          map[string]string `json:"metadata"`
	Healthy           bool              `json:"healthy"`
	RegisterTime      time.Time         `json:"registerTime"`
	LastHeartbeatTime time.Time         `json:"lastHeartbeatTime"`
	TTLSeconds        int               `json:"ttlSeconds"`
	Endpoints         []LocalEndpoint   `json:"endpoints"`
}

// LocalEndpoint stores one endpoint exposed by a local instance.
type LocalEndpoint struct {
	Protocol   string `json:"protocol"`
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`
	TargetHost string `json:"targetHost"`
	TargetPort int    `json:"targetPort"`
	Status     string `json:"status"`
}

// ActiveIntercept mirrors bridge-side active intercept view.
type ActiveIntercept struct {
	Env         string    `json:"env"`
	ServiceName string    `json:"serviceName"`
	Protocol    string    `json:"protocol"`
	TunnelID    string    `json:"tunnelId"`
	InstanceID  string    `json:"instanceId"`
	TargetPort  int       `json:"targetPort"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// DiscoverRequest describes discover query parameters.
type DiscoverRequest struct {
	ServiceName string `json:"serviceName"`
	Env         string `json:"env"`
	Protocol    string `json:"protocol"`
}

// DiscoverResponse describes discover result with route fallback metadata.
type DiscoverResponse struct {
	Matched      bool        `json:"matched"`
	ResolvedEnv  string      `json:"resolvedEnv"`
	Resolution   string      `json:"resolution"`
	RouteTarget  RouteTarget `json:"routeTarget"`
	ResourceHint string      `json:"resourceHint"`
	ServiceKey   string      `json:"serviceKey,omitempty"`
	InstanceKey  string      `json:"instanceKey,omitempty"`
	EndpointKey  string      `json:"endpointKey,omitempty"`
}

// TunnelState is surfaced to UI/Rust host.
type TunnelState struct {
	Connected         bool      `json:"connected"`
	SessionEpoch      int64     `json:"sessionEpoch"`
	ResourceVersion   int64     `json:"resourceVersion"`
	LastHeartbeatAt   time.Time `json:"lastHeartbeatAt"`
	BridgeAddress     string    `json:"bridgeAddress"`
	ReconnectBackoffM []int     `json:"reconnectBackoffMs"`
}

// StateSummary aggregates key runtime state for UI.
type StateSummary struct {
	AgentStatus         string    `json:"agentStatus"`
	BridgeStatus        string    `json:"bridgeStatus"`
	TunnelStatus        string    `json:"tunnelStatus"`
	CurrentEnv          string    `json:"currentEnv"`
	RDName              string    `json:"rdName"`
	RegistrationCount   int       `json:"registrationCount"`
	ActiveIntercepts    int       `json:"activeIntercepts"`
	LastUpdateAt        time.Time `json:"lastUpdateAt"`
	DefaultTTLSeconds   int       `json:"defaultTTLSeconds"`
	ScanIntervalSeconds int       `json:"scanIntervalSeconds"`
}

// ErrorEntry stores recent runtime errors for diagnostics.
type ErrorEntry struct {
	Code       ErrorCode         `json:"code"`
	Message    string            `json:"message"`
	Context    map[string]string `json:"context"`
	OccurredAt time.Time         `json:"occurredAt"`
}
