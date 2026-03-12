package pb

import "strings"

// ForwardMode 定义请求级转发意图模式。
type ForwardMode string

const (
	// ForwardModePortMapping 表示端口映射转发模式。
	ForwardModePortMapping ForwardMode = "port_mapping"
	// ForwardModeAutoNegotiation 表示自动协商转发模式。
	ForwardModeAutoNegotiation ForwardMode = "auto_negotiation"
)

// ForwardSource 定义转发决策候选来源。
type ForwardSource string

const (
	// ForwardSourceTunnel 表示通过 tunnel 路径转发。
	ForwardSourceTunnel ForwardSource = "tunnel"
	// ForwardSourceLocalRoute 表示命中本地 route 规则。
	ForwardSourceLocalRoute ForwardSource = "local_route"
	// ForwardSourceServiceDiscovery 表示命中服务发现路径。
	ForwardSourceServiceDiscovery ForwardSource = "service_discovery"
)

// NegotiationProfile 描述请求级协商输入配置。
type NegotiationProfile struct {
	VersionMajor     uint32            `json:"versionMajor"`
	VersionMinor     uint32            `json:"versionMinor"`
	RequiredFeatures []string          `json:"requiredFeatures,omitempty"`
	OptionalFeatures []string          `json:"optionalFeatures,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// NegotiationResult 描述请求级协商结果。
type NegotiationResult struct {
	Accepted           bool              `json:"accepted"`
	NegotiatedFeatures []string          `json:"negotiatedFeatures,omitempty"`
	MissingRequired    []string          `json:"missingRequired,omitempty"`
	ErrorCode          string            `json:"errorCode,omitempty"`
	ErrorMessage       string            `json:"errorMessage,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// ForwardIntent 描述请求级转发意图。
type ForwardIntent struct {
	RequestID          string             `json:"requestId"`
	TrafficID          string             `json:"trafficId,omitempty"`
	Mode               ForwardMode        `json:"mode"`
	Namespace          string             `json:"namespace,omitempty"`
	Environment        string             `json:"environment,omitempty"`
	ServiceName        string             `json:"serviceName,omitempty"`
	ProtocolHint       string             `json:"protocolHint,omitempty"`
	SourceOrder        []ForwardSource    `json:"sourceOrder,omitempty"`
	NegotiationProfile NegotiationProfile `json:"negotiationProfile,omitempty"`
	Metadata           map[string]string  `json:"metadata,omitempty"`
}

// ForwardDecision 描述请求级转发决策结果。
type ForwardDecision struct {
	Accepted          bool              `json:"accepted"`
	RequestID         string            `json:"requestId"`
	TrafficID         string            `json:"trafficId,omitempty"`
	Mode              ForwardMode       `json:"mode"`
	SelectedSource    ForwardSource     `json:"selectedSource,omitempty"`
	SelectedServiceID string            `json:"selectedServiceId,omitempty"`
	SelectedRouteID   string            `json:"selectedRouteId,omitempty"`
	NegotiationResult NegotiationResult `json:"negotiationResult,omitempty"`
	ErrorCode         string            `json:"errorCode,omitempty"`
	ErrorMessage      string            `json:"errorMessage,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// IsKnownForwardMode 判断转发模式是否在协议定义集合中。
func IsKnownForwardMode(mode ForwardMode) bool {
	normalized := strings.TrimSpace(string(mode))
	// 使用 switch 固定白名单，避免无效模式进入运行态。
	switch ForwardMode(normalized) {
	case ForwardModePortMapping, ForwardModeAutoNegotiation:
		return true
	default:
		return false
	}
}

// IsKnownForwardSource 判断转发来源是否在协议定义集合中。
func IsKnownForwardSource(source ForwardSource) bool {
	normalized := strings.TrimSpace(string(source))
	// 使用 switch 固定白名单，避免无效来源污染决策结果。
	switch ForwardSource(normalized) {
	case ForwardSourceTunnel, ForwardSourceLocalRoute, ForwardSourceServiceDiscovery:
		return true
	default:
		return false
	}
}
