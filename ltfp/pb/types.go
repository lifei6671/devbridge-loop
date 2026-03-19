package pb

import "encoding/json"

// Scope 描述服务或路由所属的独立作用域。
type Scope struct {
	Namespace   string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Environment string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

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
	Namespace         string            `json:"namespace,omitempty"`
	Environment       string            `json:"environment,omitempty"`
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
	TLSMode              string            `json:"tlsMode,omitempty"`
	Capabilities         []string          `json:"capabilities,omitempty"`
	AssignedSessionEpoch uint64            `json:"assignedSessionEpoch"`
	TunnelMaxReuseCount  int32             `json:"tunnelMaxReuseCount,omitempty"`
	TunnelRecycleTimeout uint32            `json:"tunnelRecycleTimeoutSec,omitempty"`
	TunnelIdleTTLSec     uint32            `json:"tunnelIdleTtlSec,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
}

// ConnectorAuth 描述连接器认证请求。
type ConnectorAuth struct {
	AuthMethod       string            `json:"authMethod"`
	Token            string            `json:"token,omitempty"`
	ClientCapVersion string            `json:"clientCapVersion,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
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

// ServiceSelector 描述 Route 引用逻辑服务的方式。
type ServiceSelector struct {
	LogicalServiceID string            `json:"logicalServiceId,omitempty"`
	ServiceName      string            `json:"serviceName,omitempty"`
	Scope            Scope             `json:"scope,omitempty"`
	MatchLabels      map[string]string `json:"matchLabels,omitempty"`
	InstanceLabels   map[string]string `json:"instanceLabels,omitempty"`
}

// HeaderMatcher 描述 Header 级匹配条件。
type HeaderMatcher struct {
	Name    string `json:"name"`
	Exact   string `json:"exact,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Regex   string `json:"regex,omitempty"`
	Present *bool  `json:"present,omitempty"`
}

// QueryMatcher 描述 Query 参数级匹配条件。
type QueryMatcher struct {
	Name    string `json:"name"`
	Exact   string `json:"exact,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Regex   string `json:"regex,omitempty"`
	Present *bool  `json:"present,omitempty"`
}

// ScopeFallbackPolicy 描述按 namespace 生效的作用域降级策略。
type ScopeFallbackPolicy struct {
	PolicyID  string                 `json:"policyId" yaml:"policy_id"`
	Namespace string                 `json:"namespace" yaml:"namespace"`
	Enabled   bool                   `json:"enabled" yaml:"enabled"`
	Chain     []FallbackStep         `json:"chain,omitempty" yaml:"chain,omitempty"`
	External  ExternalFallbackConfig `json:"external,omitempty" yaml:"external,omitempty"`
}

// FallbackStep 描述单个降级目标 scope。
type FallbackStep struct {
	TargetScope Scope `json:"targetScope" yaml:"target_scope"`
}

// ExternalFallbackConfig 描述是否允许本地降级链 miss 后查询外部发现系统。
type ExternalFallbackConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// RouteHint 描述服务注册时附带的自动路由匹配补充条件。
type RouteHint struct {
	MatchHeaders []HeaderMatcher `json:"matchHeaders,omitempty"`
	MatchQueries []QueryMatcher  `json:"matchQueries,omitempty"`
	Priority     uint32          `json:"priority,omitempty"`
}

// PublishService 描述服务发布请求。
type PublishService struct {
	InstanceID      string            `json:"instanceId,omitempty"`
	ServiceName     string            `json:"serviceName"`
	Scope           Scope             `json:"scope"`
	Labels          map[string]string `json:"labels,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ServiceType     string            `json:"serviceType,omitempty"`
	Endpoints       []ServiceEndpoint `json:"endpoints"`
	Exposure        ServiceExposure   `json:"exposure"`
	HealthCheck     HealthCheckConfig `json:"healthCheck,omitempty"`
	DiscoveryPolicy DiscoveryPolicy   `json:"discoveryPolicy,omitempty"`
	RouteHint       RouteHint         `json:"routeHint,omitempty"`
}

// PublishServiceAck 描述发布服务响应。
type PublishServiceAck struct {
	Accepted                bool   `json:"accepted"`
	LogicalServiceID        string `json:"logicalServiceId,omitempty"`
	InstanceID              string `json:"instanceId,omitempty"`
	ServiceName             string `json:"serviceName,omitempty"`
	Scope                   Scope  `json:"scope,omitempty"`
	AcceptedResourceVersion uint64 `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64 `json:"currentResourceVersion,omitempty"`
	ErrorCode               string `json:"errorCode,omitempty"`
	ErrorMessage            string `json:"errorMessage,omitempty"`
}

// UnpublishService 描述服务下线请求。
type UnpublishService struct {
	InstanceID       string `json:"instanceId,omitempty"`
	LogicalServiceID string `json:"logicalServiceId,omitempty"`
	ServiceName      string `json:"serviceName,omitempty"`
	Scope            Scope  `json:"scope,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

// UnpublishServiceAck 描述服务下线响应。
type UnpublishServiceAck struct {
	Accepted                bool   `json:"accepted"`
	LogicalServiceID        string `json:"logicalServiceId,omitempty"`
	InstanceID              string `json:"instanceId,omitempty"`
	AcceptedResourceVersion uint64 `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64 `json:"currentResourceVersion,omitempty"`
	ErrorCode               string `json:"errorCode,omitempty"`
	ErrorMessage            string `json:"errorMessage,omitempty"`
}

// EndpointHealthStatus 描述 endpoint 级健康状态。
type EndpointHealthStatus struct {
	EndpointID   string       `json:"endpointId,omitempty"`
	HealthStatus HealthStatus `json:"healthStatus"`
	Reason       string       `json:"reason,omitempty"`
}

// ServiceHealthReport 描述服务健康上报。
type ServiceHealthReport struct {
	InstanceID          string                 `json:"instanceId"`
	LogicalServiceID    string                 `json:"logicalServiceId,omitempty"`
	ServiceHealthStatus HealthStatus           `json:"serviceHealthStatus"`
	EndpointStatuses    []EndpointHealthStatus `json:"endpointStatuses,omitempty"`
	CheckTimeUnix       int64                  `json:"checkTimeUnix"`
	Reason              string                 `json:"reason,omitempty"`
	Metadata            map[string]string      `json:"metadata,omitempty"`
}

// TunnelPoolReport 描述 Agent 向 Bridge 上报 tunnel 池状态的控制面消息。
type TunnelPoolReport struct {
	SessionID       string            `json:"sessionId,omitempty"`
	SessionEpoch    uint64            `json:"sessionEpoch,omitempty"`
	IdleCount       int               `json:"idleCount"`
	InUseCount      int               `json:"inUseCount"`
	TargetIdleCount int               `json:"targetIdleCount"`
	Trigger         string            `json:"trigger,omitempty"`
	TimestampUnix   int64             `json:"timestampUnix"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// TunnelDialAnnounce 描述 Agent 成功建立 tunnel 后向 Bridge 宣告 tunnel_id 的控制面消息。
type TunnelDialAnnounce struct {
	SessionID     string `json:"sessionId,omitempty"`
	SessionEpoch  uint64 `json:"sessionEpoch,omitempty"`
	TunnelID      string `json:"tunnelId,omitempty"`
	DialLocalAddr string `json:"dialLocalAddr,omitempty"`
	TimestampUnix int64  `json:"timestampUnix"`
}

// TunnelRefillRequest 描述 Bridge 请求 Agent 扩容 tunnel 池的控制面消息。
type TunnelRefillRequest struct {
	SessionID          string            `json:"sessionId,omitempty"`
	SessionEpoch       uint64            `json:"sessionEpoch,omitempty"`
	RequestID          string            `json:"requestId"`
	RequestedIdleDelta int               `json:"requestedIdleDelta"`
	Reason             string            `json:"reason,omitempty"`
	TimestampUnix      int64             `json:"timestampUnix"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// ConnectorServiceTarget 描述 connector service 目标。
type ConnectorServiceTarget struct {
	Selector          ServiceSelector   `json:"selector"`
	InstanceSelector  map[string]string `json:"instanceSelector,omitempty"`
	LoadBalancePolicy string            `json:"loadBalancePolicy,omitempty"`
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

// RouteTarget 描述 route 指向的目标。
type RouteTarget struct {
	Type             RouteTargetType         `json:"type"`
	ConnectorService *ConnectorServiceTarget `json:"connectorService,omitempty"`
	ExternalService  *ExternalServiceTarget  `json:"externalService,omitempty"`
}

// RouteMatch 描述路由匹配条件。
type RouteMatch struct {
	Protocol   string          `json:"protocol,omitempty"`
	Host       string          `json:"host,omitempty"`
	Authority  string          `json:"authority,omitempty"`
	ListenPort uint32          `json:"listenPort,omitempty"`
	PathPrefix string          `json:"pathPrefix,omitempty"`
	SNI        string          `json:"sni,omitempty"`
	Headers    []HeaderMatcher `json:"headers,omitempty"`
	Queries    []QueryMatcher  `json:"queries,omitempty"`
}

// RouteAssign 描述可选扩展 route 下发消息。
type RouteAssign struct {
	RouteID    string            `json:"routeId"`
	Scope      Scope             `json:"scope,omitempty"`
	Match      RouteMatch        `json:"match"`
	Target     RouteTarget       `json:"target"`
	Priority   uint32            `json:"priority,omitempty"`
	PolicyJSON string            `json:"policyJson,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// RouteAssignAck 描述 route 下发 ACK。
type RouteAssignAck struct {
	Accepted                bool              `json:"accepted"`
	RouteID                 string            `json:"routeId,omitempty"`
	AcceptedResourceVersion uint64            `json:"acceptedResourceVersion,omitempty"`
	CurrentResourceVersion  uint64            `json:"currentResourceVersion,omitempty"`
	ErrorCode               string            `json:"errorCode,omitempty"`
	ErrorMessage            string            `json:"errorMessage,omitempty"`
	Warnings                []string          `json:"warnings,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
}

// RouteRevoke 描述 route 撤销消息。
type RouteRevoke struct {
	RouteID string `json:"routeId"`
	Scope   Scope  `json:"scope,omitempty"`
	Reason  string `json:"reason,omitempty"`
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
	LogicalServiceID      string            `json:"logicalServiceId"`
	InstanceID            string            `json:"instanceId"`
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

// TrafficCloseAck 描述正常关闭确认消息。
type TrafficCloseAck struct {
	TrafficID    string            `json:"trafficId"`
	Accepted     bool              `json:"accepted"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// TrafficReset 描述异常中断流量消息。
type TrafficReset struct {
	TrafficID    string `json:"trafficId"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// TunnelRecycle 描述服务端发起的 tunnel 回收请求。
type TunnelRecycle struct {
	TunnelID              string            `json:"tunnelId"`
	RecycleSeq            uint64            `json:"recycleSeq"`
	IsFinal               bool              `json:"isFinal,omitempty"`
	CompletedTrafficCount int32             `json:"completedTrafficCount,omitempty"`
	Metadata              map[string]string `json:"metadata,omitempty"`
}

// TunnelRecycleAck 描述 Agent 对 tunnel 回收请求的确认。
type TunnelRecycleAck struct {
	TunnelID     string            `json:"tunnelId"`
	RecycleSeq   uint64            `json:"recycleSeq"`
	Accepted     bool              `json:"accepted"`
	ErrorCode    string            `json:"errorCode,omitempty"`
	ErrorMessage string            `json:"errorMessage,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// StreamPayload 描述数据通道 oneof 负载。
type StreamPayload struct {
	OpenReq    *TrafficOpen      `json:"openReq,omitempty"`
	OpenAck    *TrafficOpenAck   `json:"openAck,omitempty"`
	Data       []byte            `json:"data,omitempty"`
	Close      *TrafficClose     `json:"close,omitempty"`
	CloseAck   *TrafficCloseAck  `json:"closeAck,omitempty"`
	Reset      *TrafficReset     `json:"reset,omitempty"`
	Recycle    *TunnelRecycle    `json:"recycle,omitempty"`
	RecycleAck *TunnelRecycleAck `json:"recycleAck,omitempty"`
}

// ActivePayloadCount 统计数据面 oneof 实际被设置的字段数量。
func (payload StreamPayload) ActivePayloadCount() int {
	count := 0
	if payload.OpenReq != nil {
		count++
	}
	if payload.OpenAck != nil {
		count++
	}
	if len(payload.Data) > 0 {
		count++
	}
	if payload.Close != nil {
		count++
	}
	if payload.CloseAck != nil {
		count++
	}
	if payload.Reset != nil {
		count++
	}
	if payload.Recycle != nil {
		count++
	}
	if payload.RecycleAck != nil {
		count++
	}
	return count
}
