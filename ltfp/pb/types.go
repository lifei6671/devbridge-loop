package pb

import "encoding/json"

// ControlEnvelope 描述控制通道上的统一消息封装。
type ControlEnvelope struct {
	VersionMajor    uint32             `json:"versionMajor"`
	VersionMinor    uint32             `json:"versionMinor"`
	MessageType     ControlMessageType `json:"messageType"`
	RequestID       string             `json:"requestId,omitempty"`
	SessionID       string             `json:"sessionId,omitempty"`
	SessionEpoch    uint64             `json:"sessionEpoch,omitempty"`
	ConnectorID     string             `json:"connectorId,omitempty"`
	ResourceType    string             `json:"resourceType,omitempty"`
	ResourceID      string             `json:"resourceId,omitempty"`
	EventID         string             `json:"eventId,omitempty"`
	ResourceVersion uint64             `json:"resourceVersion,omitempty"`
	Payload         json.RawMessage    `json:"payload,omitempty"`
}

// ConnectorHello 描述连接器发起握手时上报的信息。
type ConnectorHello struct {
	ConnectorID       string            `json:"connectorId"`
	Namespace         string            `json:"namespace"`
	Environment       string            `json:"environment"`
	NodeName          string            `json:"nodeName"`
	Version           string            `json:"version"`
	SupportedBindings []string          `json:"supportedBindings,omitempty"`
	Capabilities      []string          `json:"capabilities,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// ConnectorWelcome 描述服务端握手返回的协商参数。
type ConnectorWelcome struct {
	SelectedBinding      string            `json:"selectedBinding"`
	VersionMajor         uint32            `json:"versionMajor"`
	VersionMinor         uint32            `json:"versionMinor"`
	HeartbeatIntervalSec uint32            `json:"heartbeatIntervalSec"`
	Capabilities         []string          `json:"capabilities,omitempty"`
	AssignedSessionEpoch uint64            `json:"assignedSessionEpoch"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

// ConnectorAuth 描述连接器认证请求。
type ConnectorAuth struct {
	AuthMethod  string            `json:"authMethod"`
	AuthPayload map[string]string `json:"authPayload,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ConnectorAuthAck 描述认证结果。
type ConnectorAuthAck struct {
	Success      bool              `json:"success"`
	SessionID    string            `json:"sessionId,omitempty"`
	SessionEpoch uint64            `json:"sessionEpoch,omitempty"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Heartbeat 描述控制通道心跳消息。
type Heartbeat struct {
	TimestampUnix int64             `json:"timestampUnix"`
	SessionState  SessionState      `json:"sessionState,omitempty"`
	LoadHint      string            `json:"loadHint,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// ServiceEndpoint 描述服务的 upstream endpoint。
type ServiceEndpoint struct {
	EndpointID     string            `json:"endpointId,omitempty"`
	Protocol       string            `json:"protocol"`
	Host           string            `json:"host"`
	Port           uint32            `json:"port"`
	TLSMode        string            `json:"tlsMode,omitempty"`
	ServerName     string            `json:"serverName,omitempty"`
	DialTimeoutMS  uint32            `json:"dialTimeoutMs,omitempty"`
	ReadTimeoutMS  uint32            `json:"readTimeoutMs,omitempty"`
	WriteTimeoutMS uint32            `json:"writeTimeoutMs,omitempty"`
	Weight         uint32            `json:"weight,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// ServiceExposure 描述服务对外暴露方式。
type ServiceExposure struct {
	IngressMode IngressMode `json:"ingressMode"`
	Host        string      `json:"host,omitempty"`
	ListenPort  uint32      `json:"listenPort,omitempty"`
	SNIName     string      `json:"sniName,omitempty"`
	PathPrefix  string      `json:"pathPrefix,omitempty"`
	AllowExport bool        `json:"allowExport,omitempty"`
}

// HealthCheckConfig 描述健康检查配置。
type HealthCheckConfig struct {
	Type               string `json:"type,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
	IntervalSec        uint32 `json:"intervalSec,omitempty"`
	TimeoutSec         uint32 `json:"timeoutSec,omitempty"`
	HealthyThreshold   uint32 `json:"healthyThreshold,omitempty"`
	UnhealthyThreshold uint32 `json:"unhealthyThreshold,omitempty"`
}

// DiscoveryPolicy 描述导出到第三方发现系统的策略。
type DiscoveryPolicy struct {
	Enabled      bool              `json:"enabled"`
	Providers    []string          `json:"providers,omitempty"`
	ExternalName string            `json:"externalName,omitempty"`
	Namespace    string            `json:"namespace,omitempty"`
	Group        string            `json:"group,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// PublishService 描述服务发布请求。
type PublishService struct {
	ServiceID       string            `json:"serviceId,omitempty"`
	ServiceKey      string            `json:"serviceKey"`
	Namespace       string            `json:"namespace"`
	Environment     string            `json:"environment"`
	ServiceName     string            `json:"serviceName"`
	ServiceType     string            `json:"serviceType,omitempty"`
	Endpoints       []ServiceEndpoint `json:"endpoints"`
	Exposure        ServiceExposure   `json:"exposure"`
	HealthCheck     HealthCheckConfig `json:"healthCheck,omitempty"`
	DiscoveryPolicy DiscoveryPolicy   `json:"discoveryPolicy,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// PublishServiceAck 描述发布服务响应。
type PublishServiceAck struct {
	Accepted                bool              `json:"accepted"`
	ServiceID               string            `json:"serviceId,omitempty"`
	ServiceKey              string            `json:"serviceKey,omitempty"`
	AcceptedResourceVersion uint64            `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64            `json:"currentResourceVersion,omitempty"`
	ErrorCode               string            `json:"errorCode,omitempty"`
	ErrorMessage            string            `json:"errorMessage,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

// UnpublishService 描述服务下线请求。
type UnpublishService struct {
	ServiceID   string `json:"serviceId,omitempty"`
	ServiceKey  string `json:"serviceKey,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Environment string `json:"environment,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// UnpublishServiceAck 描述服务下线响应。
type UnpublishServiceAck struct {
	Accepted                bool              `json:"accepted"`
	ServiceID               string            `json:"serviceId,omitempty"`
	ServiceKey              string            `json:"serviceKey,omitempty"`
	AcceptedResourceVersion uint64            `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64            `json:"currentResourceVersion,omitempty"`
	ErrorCode               string            `json:"errorCode,omitempty"`
	ErrorMessage            string            `json:"errorMessage,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

// EndpointHealthStatus 描述 endpoint 级健康状态。
type EndpointHealthStatus struct {
	EndpointID   string       `json:"endpointId,omitempty"`
	HealthStatus HealthStatus `json:"healthStatus"`
	Reason       string       `json:"reason,omitempty"`
}

// ServiceHealthReport 描述服务健康上报。
type ServiceHealthReport struct {
	ServiceID           string                 `json:"serviceId,omitempty"`
	ServiceKey          string                 `json:"serviceKey,omitempty"`
	ServiceHealthStatus HealthStatus           `json:"serviceHealthStatus"`
	EndpointStatuses    []EndpointHealthStatus `json:"endpointStatuses,omitempty"`
	CheckTimeUnix       int64                  `json:"checkTimeUnix"`
	Reason              string                 `json:"reason,omitempty"`
	Metadata            map[string]string      `json:"metadata,omitempty"`
}

// ConnectorServiceTarget 描述 connector service 目标。
type ConnectorServiceTarget struct {
	ServiceKey string            `json:"serviceKey"`
	Selector   map[string]string `json:"selector,omitempty"`
}

// ExternalServiceTarget 描述 external service 目标。
type ExternalServiceTarget struct {
	Provider        string            `json:"provider,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Environment     string            `json:"environment,omitempty"`
	ServiceName     string            `json:"serviceName"`
	Group           string            `json:"group,omitempty"`
	Selector        map[string]string `json:"selector,omitempty"`
	CacheTTLSeconds uint32            `json:"cacheTtlSec,omitempty"`
	StaleIfErrorSec uint32            `json:"staleIfErrorSec,omitempty"`
}

// HybridGroupTarget 描述 hybrid 目标。
type HybridGroupTarget struct {
	PrimaryConnectorService ConnectorServiceTarget `json:"primaryConnectorService"`
	FallbackExternalService ExternalServiceTarget  `json:"fallbackExternalService"`
	FallbackPolicy          FallbackPolicy         `json:"fallbackPolicy"`
}

// RouteTarget 描述 route 指向的目标。
type RouteTarget struct {
	Type             RouteTargetType         `json:"type"`
	ConnectorService *ConnectorServiceTarget `json:"connectorService,omitempty"`
	ExternalService  *ExternalServiceTarget  `json:"externalService,omitempty"`
	HybridGroup      *HybridGroupTarget      `json:"hybridGroup,omitempty"`
}

// RouteMatch 描述路由匹配条件。
type RouteMatch struct {
	Protocol   string `json:"protocol,omitempty"`
	Host       string `json:"host,omitempty"`
	Authority  string `json:"authority,omitempty"`
	ListenPort uint32 `json:"listenPort,omitempty"`
	PathPrefix string `json:"pathPrefix,omitempty"`
	SNI        string `json:"sni,omitempty"`
}

// RouteAssign 描述可选扩展 route 下发消息。
type RouteAssign struct {
	RouteID     string            `json:"routeId"`
	Namespace   string            `json:"namespace"`
	Environment string            `json:"environment"`
	Match       RouteMatch        `json:"match"`
	Target      RouteTarget       `json:"target"`
	Priority    uint32            `json:"priority,omitempty"`
	PolicyJSON  string            `json:"policyJson,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// RouteAssignAck 描述 route 下发 ACK。
type RouteAssignAck struct {
	Accepted                bool              `json:"accepted"`
	RouteID                 string            `json:"routeId,omitempty"`
	AcceptedResourceVersion uint64            `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64            `json:"currentResourceVersion,omitempty"`
	ErrorCode               string            `json:"errorCode,omitempty"`
	ErrorMessage            string            `json:"errorMessage,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

// RouteRevoke 描述 route 撤销消息。
type RouteRevoke struct {
	RouteID     string `json:"routeId"`
	Namespace   string `json:"namespace"`
	Environment string `json:"environment"`
	Reason      string `json:"reason,omitempty"`
}

// RouteRevokeAck 描述 route 撤销 ACK。
type RouteRevokeAck struct {
	Accepted                bool              `json:"accepted"`
	RouteID                 string            `json:"routeId,omitempty"`
	AcceptedResourceVersion uint64            `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64            `json:"currentResourceVersion,omitempty"`
	ErrorCode               string            `json:"errorCode,omitempty"`
	ErrorMessage            string            `json:"errorMessage,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

// RouteStatusReport 描述 route 状态上报消息。
type RouteStatusReport struct {
	RouteID string `json:"routeId"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ControlError 描述控制面错误消息。
type ControlError struct {
	Scope     string            `json:"scope,omitempty"`
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// TrafficOpen 描述 connector path 的流量打开请求。
type TrafficOpen struct {
	TrafficID             string            `json:"trafficId"`
	RouteID               string            `json:"routeId,omitempty"`
	ServiceID             string            `json:"serviceId"`
	SourceAddr            string            `json:"sourceAddr,omitempty"`
	ProtocolHint          string            `json:"protocolHint,omitempty"`
	TraceID               string            `json:"traceId,omitempty"`
	EndpointSelectionHint map[string]string `json:"endpointSelectionHint,omitempty"`
	Metadata              map[string]string `json:"metadata,omitempty"`
}

// TrafficOpenAck 描述流量打开结果。
type TrafficOpenAck struct {
	TrafficID    string            `json:"trafficId"`
	Success      bool              `json:"success"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// TrafficClose 描述正常关闭流量消息。
type TrafficClose struct {
	TrafficID string `json:"trafficId"`
	Reason    string `json:"reason,omitempty"`
}

// TrafficReset 描述异常中断流量消息。
type TrafficReset struct {
	TrafficID    string `json:"trafficId"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// StreamPayload 描述数据通道 oneof 负载。
type StreamPayload struct {
	OpenReq *TrafficOpen    `json:"openReq,omitempty"`
	OpenAck *TrafficOpenAck `json:"openAck,omitempty"`
	Data    []byte          `json:"data,omitempty"`
	Close   *TrafficClose   `json:"close,omitempty"`
	Reset   *TrafficReset   `json:"reset,omitempty"`
}

// ActivePayloadCount 统计数据面 oneof 实际被设置的字段数量。
func (payload StreamPayload) ActivePayloadCount() int {
	count := 0
	// oneof 语义要求只有一个字段生效，这里用于校验前快速计数。
	if payload.OpenReq != nil {
		count++
	}
	// OpenAck 表示 pre-open 结果。
	if payload.OpenAck != nil {
		count++
	}
	// Data 字段允许二进制内容，因此以长度判断是否存在。
	if len(payload.Data) > 0 {
		count++
	}
	// Close 字段表示正常结束。
	if payload.Close != nil {
		count++
	}
	// Reset 字段表示异常结束。
	if payload.Reset != nil {
		count++
	}
	return count
}
