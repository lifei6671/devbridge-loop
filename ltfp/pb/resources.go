package pb

import "time"

// Connector 描述 connector 的静态与能力属性。
type Connector struct {
	ConnectorID  string            `json:"connectorId"`
	Namespace    string            `json:"namespace"`
	Environment  string            `json:"environment"`
	NodeName     string            `json:"nodeName,omitempty"`
	DisplayName  string            `json:"displayName,omitempty"`
	Version      string            `json:"version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Status       string            `json:"status,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Session 描述 connector 会话运行状态。
type Session struct {
	SessionID     string            `json:"sessionId"`
	ConnectorID   string            `json:"connectorId"`
	SessionEpoch  uint64            `json:"sessionEpoch"`
	BindingType   string            `json:"bindingType,omitempty"`
	State         SessionState      `json:"state"`
	Authenticated bool              `json:"authenticated"`
	CreatedAt     time.Time         `json:"createdAt,omitempty"`
	LastSeenAt    time.Time         `json:"lastSeenAt,omitempty"`
	RemoteAddr    string            `json:"remoteAddr,omitempty"`
	LastHeartbeat time.Time         `json:"lastHeartbeat,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Service 描述 connector 发布后的服务快照。
type Service struct {
	ServiceID       string            `json:"serviceId"`
	ServiceKey      string            `json:"serviceKey"`
	Namespace       string            `json:"namespace"`
	Environment     string            `json:"environment"`
	ConnectorID     string            `json:"connectorId"`
	ServiceName     string            `json:"serviceName"`
	ServiceType     string            `json:"serviceType,omitempty"`
	Status          ServiceStatus     `json:"status"`
	ResourceVersion uint64            `json:"resourceVersion"`
	Endpoints       []ServiceEndpoint `json:"endpoints,omitempty"`
	Exposure        ServiceExposure   `json:"exposure,omitempty"`
	HealthCheck     HealthCheckConfig `json:"healthCheck,omitempty"`
	HealthStatus    HealthStatus      `json:"healthStatus"`
	DiscoveryPolicy DiscoveryPolicy   `json:"discoveryPolicy,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Route 描述路由配置快照。
type Route struct {
	RouteID         string            `json:"routeId"`
	Namespace       string            `json:"namespace"`
	Environment     string            `json:"environment"`
	ResourceVersion uint64            `json:"resourceVersion"`
	Match           RouteMatch        `json:"match"`
	Target          RouteTarget       `json:"target"`
	PolicyJSON      string            `json:"policyJson,omitempty"`
	Priority        uint32            `json:"priority,omitempty"`
	Status          string            `json:"status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// TrafficState 描述流量状态机阶段。
type TrafficState string

const (
	// TrafficStateOpening 表示流量正在执行 open。
	TrafficStateOpening TrafficState = "OPENING"
	// TrafficStateOpen 表示流量已进入稳定转发阶段。
	TrafficStateOpen TrafficState = "OPEN"
	// TrafficStateClosing 表示流量正在执行优雅关闭。
	TrafficStateClosing TrafficState = "CLOSING"
	// TrafficStateClosed 表示流量已关闭。
	TrafficStateClosed TrafficState = "CLOSED"
	// TrafficStateReset 表示流量异常中断。
	TrafficStateReset TrafficState = "RESET"
)

// Traffic 描述运行态流量信息。
type Traffic struct {
	TrafficID   string            `json:"trafficId"`
	RouteID     string            `json:"routeId,omitempty"`
	TargetKind  RouteTargetType   `json:"targetKind"`
	ServiceID   string            `json:"serviceId,omitempty"`
	ConnectorID string            `json:"connectorId,omitempty"`
	SourceAddr  string            `json:"sourceAddr,omitempty"`
	TargetAddr  string            `json:"targetAddr,omitempty"`
	TraceID     string            `json:"traceId,omitempty"`
	State       TrafficState      `json:"state"`
	StartedAt   time.Time         `json:"startedAt,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// DiscoveryProjection 描述导出到第三方发现系统的投影视图元信息。
type DiscoveryProjection struct {
	ProjectionID string            `json:"projectionId"`
	ServiceID    string            `json:"serviceId"`
	Provider     string            `json:"provider"`
	Namespace    string            `json:"namespace,omitempty"`
	Environment  string            `json:"environment,omitempty"`
	ExportedAddr string            `json:"exportedAddr,omitempty"`
	Status       string            `json:"status,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}
