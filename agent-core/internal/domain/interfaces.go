package domain

import "context"

// ProtocolAdapter defines protocol-specific hooks for ingress/egress/backflow.
type ProtocolAdapter interface {
	Protocol() string
	ParseIngress(ctx context.Context, req any) (RouteTarget, error)
	ProxyEgress(ctx context.Context, target RouteTarget, payload []byte) ([]byte, error)
	ForwardBackflow(ctx context.Context, target RouteTarget, payload []byte) ([]byte, error)
	MapError(err error) ErrorCode
}

// IngressResolver resolves route targets from ingress context.
type IngressResolver interface {
	Resolve(ctx context.Context, ingress IngressContext) (RouteTarget, error)
}

// Forwarder forwards traffic to local endpoints.
type Forwarder interface {
	Forward(ctx context.Context, target RouteTarget, payload []byte) ([]byte, error)
}

// EgressProxy handles outgoing proxy requests.
type EgressProxy interface {
	Proxy(ctx context.Context, target RouteTarget, payload []byte) ([]byte, error)
}

// IngressContext is a protocol-agnostic ingress request context.
type IngressContext struct {
	Host    string
	Headers map[string]string
	SNI     string
	Method  string
	Path    string
}

// RouteTarget is the canonical target for routing and forwarding.
type RouteTarget struct {
	Env         string `json:"env"`
	ServiceName string `json:"serviceName"`
	Protocol    string `json:"protocol"`
	TargetHost  string `json:"targetHost"`
	TargetPort  int    `json:"targetPort"`
}
