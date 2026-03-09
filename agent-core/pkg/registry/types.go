package registry

import (
	"context"
	"net/http"
)

// RegistryType 表示业务系统 registry 抽象中的后端类型。
type RegistryType string

const (
	// RegistryTypeNacos 表示传统 nacos 发现后端（此包不实现，保留扩展位）。
	RegistryTypeNacos RegistryType = "nacos"
	// RegistryTypeLocal 表示本地直连发现后端（此包不实现，保留扩展位）。
	RegistryTypeLocal RegistryType = "local"
	// RegistryTypeAgent 表示通过 dev-agent 进行注册与发现。
	RegistryTypeAgent RegistryType = "agent"
)

// Endpoint 表示业务服务实例对外暴露的单个协议端口。
type Endpoint struct {
	Protocol   string
	ListenHost string
	ListenPort int
	TargetHost string
	TargetPort int
	Status     string
}

// Registration 表示业务服务对 registry 的注册对象。
type Registration struct {
	ServiceName string
	Env         string
	InstanceID  string
	TTLSeconds  int
	Metadata    map[string]string
	Endpoints   []Endpoint
}

// DiscoverRequest 表示业务服务发起的服务发现请求。
type DiscoverRequest struct {
	ServiceName string
	Env         string
	Protocol    string
}

// DiscoverResponse 表示 agent discover 的归一化返回。
type DiscoverResponse struct {
	Matched      bool
	ResolvedEnv  string
	Resolution   string
	TargetHost   string
	TargetPort   int
	ResourceHint string
}

// Options 是 registry 工厂配置。
type Options struct {
	Type       RegistryType
	AgentAddr  string
	RuntimeEnv string
	HTTPClient *http.Client
}

// Adapter 定义业务系统可依赖的统一 registry 抽象。
type Adapter interface {
	Register(ctx context.Context, registration Registration) (Registration, error)
	Heartbeat(ctx context.Context, instanceID string) error
	Unregister(ctx context.Context, instanceID string) error
	Discover(ctx context.Context, request DiscoverRequest) (DiscoverResponse, error)
}
