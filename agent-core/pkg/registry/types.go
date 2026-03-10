package registry

import (
	"net/http"
	"time"

	serviceregistry "github.com/lifei6671/devbridge-loop/service-registry"
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

// 复用共享抽象层，确保 bridge/core 统一遵循同一套注册发现契约。
type Registry = serviceregistry.Registry
type Registrar = serviceregistry.Registrar
type Discovery = serviceregistry.Discovery
type Watcher = serviceregistry.Watcher
type Service = serviceregistry.Service
type Endpoint = serviceregistry.Endpoint
type Endpoints = serviceregistry.Endpoints
type Metadata = serviceregistry.Metadata
type SearchInput = serviceregistry.SearchInput
type BasicEndpoint = serviceregistry.BasicEndpoint
type BasicService = serviceregistry.BasicService
type ServiceOption = serviceregistry.ServiceOption

// Options 是 registry 工厂配置。
type Options struct {
	Type       RegistryType
	AgentAddr  string
	RuntimeEnv string
	HTTPClient *http.Client
	WatchEvery time.Duration
}

// NewService 暴露共享库默认 Service 构造器，便于调用方快速迁移。
func NewService(name string, options ...serviceregistry.ServiceOption) *serviceregistry.BasicService {
	return serviceregistry.NewBasicService(name, options...)
}

// NewEndpoint 暴露共享库默认 Endpoint 构造器。
func NewEndpoint(host string, port int) serviceregistry.BasicEndpoint {
	return serviceregistry.NewBasicEndpoint(host, port)
}

// WithServiceVersion 设置服务版本。
func WithServiceVersion(version string) ServiceOption {
	return serviceregistry.WithServiceVersion(version)
}

// WithServiceMetadata 设置服务元数据。
func WithServiceMetadata(metadata Metadata) ServiceOption {
	return serviceregistry.WithServiceMetadata(metadata)
}

// WithServiceEndpoints 设置服务 endpoint 列表。
func WithServiceEndpoints(endpoints Endpoints) ServiceOption {
	return serviceregistry.WithServiceEndpoints(endpoints)
}

// WithServiceKey 设置服务 key。
func WithServiceKey(key string) ServiceOption {
	return serviceregistry.WithServiceKey(key)
}

// WithServiceValue 设置服务 value。
func WithServiceValue(value string) ServiceOption {
	return serviceregistry.WithServiceValue(value)
}

// WithServicePrefix 设置服务 prefix。
func WithServicePrefix(prefix string) ServiceOption {
	return serviceregistry.WithServicePrefix(prefix)
}
