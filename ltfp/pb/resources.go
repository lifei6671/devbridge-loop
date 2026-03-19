package pb

import "time"

// Connector 描述 connector 的静态与能力属性。
type Connector struct {
	ConnectorID  string            `json:"connectorId"`
	Namespace    string            `json:"namespace,omitempty"`
	Environment  string            `json:"environment,omitempty"`
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

// LogicalService 描述逻辑服务快照。
type LogicalService struct {
	LogicalServiceID     string            `json:"logicalServiceId"`
	ServiceName          string            `json:"serviceName"`
	Scope                Scope             `json:"scope"`
	Status               ServiceStatus     `json:"status"`
	ActiveInstanceCount  int32             `json:"activeInstanceCount,omitempty"`
	HealthyInstanceCount int32             `json:"healthyInstanceCount,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	ResourceVersion      uint64            `json:"resourceVersion"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

// ServiceInstance 描述逻辑服务下的单个实例快照。
type ServiceInstance struct {
	InstanceID       string            `json:"instanceId"`
	LogicalServiceID string            `json:"logicalServiceId"`
	ConnectorID      string            `json:"connectorId"`
	SessionID        string            `json:"sessionId,omitempty"`
	SessionEpoch     uint64            `json:"sessionEpoch,omitempty"`
	InstanceStatus   ServiceStatus     `json:"instanceStatus"`
	HealthStatus     HealthStatus      `json:"healthStatus"`
	ResourceVersion  uint64            `json:"resourceVersion"`
	Endpoints        []ServiceEndpoint `json:"endpoints,omitempty"`
	Exposure         ServiceExposure   `json:"exposure,omitempty"`
	HealthCheck      HealthCheckConfig `json:"healthCheck,omitempty"`
	DiscoveryPolicy  DiscoveryPolicy   `json:"discoveryPolicy,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// Route 描述路由配置快照。
type Route struct {
	RouteID         string            `json:"routeId"`
	Scope           Scope             `json:"scope,omitempty"`
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
	TrafficID         string            `json:"trafficId"`
	RouteID           string            `json:"routeId,omitempty"`
	TargetKind        RouteTargetType   `json:"targetKind"`
	LogicalServiceID  string            `json:"logicalServiceId,omitempty"`
	InstanceID        string            `json:"instanceId,omitempty"`
	ConnectorID       string            `json:"connectorId,omitempty"`
	SourceAddr        string            `json:"sourceAddr,omitempty"`
	TargetAddr        string            `json:"targetAddr,omitempty"`
	TraceID           string            `json:"traceId,omitempty"`
	RequestScope      Scope             `json:"requestScope,omitempty"`
	MatchedScope      Scope             `json:"matchedScope,omitempty"`
	ScopeFallbackPath []Scope           `json:"scopeFallbackPath,omitempty"`
	State             TrafficState      `json:"state"`
	StartedAt         time.Time         `json:"startedAt,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// DiscoveryProjection 描述导出到第三方发现系统的投影视图元信息。
type DiscoveryProjection struct {
	ProjectionID     string            `json:"projectionId"`
	LogicalServiceID string            `json:"logicalServiceId"`
	InstanceID       string            `json:"instanceId,omitempty"`
	Provider         string            `json:"provider"`
	Namespace        string            `json:"namespace,omitempty"`
	Environment      string            `json:"environment,omitempty"`
	ExportedAddr     string            `json:"exportedAddr,omitempty"`
	Status           string            `json:"status,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}
