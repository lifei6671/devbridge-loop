package serviceregistry

import "context"

// Registry 定义统一的服务注册与发现能力。
type Registry interface {
	Registrar
	Discovery
}

// Registrar 定义服务注册能力。
type Registrar interface {
	// Register 将 `service` 注册到注册中心。
	// 当注册中心对输入 Service 做了规范化或增强时，应返回新的 Service。
	Register(ctx context.Context, service Service) (registered Service, err error)

	// Deregister 将 `service` 下线并从注册中心移除。
	Deregister(ctx context.Context, service Service) error
}

// Discovery 定义服务发现能力。
type Discovery interface {
	// Search 按条件检索并返回服务列表。
	Search(ctx context.Context, in SearchInput) (result []Service, err error)

	// Watch 监听指定条件的变更。
	// `key` 为服务 key 的前缀。
	Watch(ctx context.Context, key string) (watcher Watcher, err error)
}

// Watcher 定义服务变更监听器。
type Watcher interface {
	// Proceed 以阻塞方式推进一次监听。
	// 当被 `key` 命中的服务发生变化时，返回完整服务列表。
	Proceed() (services []Service, err error)

	// Close 关闭监听器。
	Close() error
}

// Service 定义服务实体能力。
type Service interface {
	// GetName 返回服务名。
	// 服务名是必填项，且应在服务集合内唯一。
	GetName() string

	// GetVersion 返回服务版本。
	// 建议采用 GNU 风格版本号，例如：v1.0.0、v2.0.1、v2.1.0-rc。
	// 一个服务可同时部署多个版本。
	// 若未设置版本，默认版本应为 "latest"。
	GetVersion() string

	// GetKey 格式化并返回服务唯一 key。
	// 该 key 常用于 KV 型注册中心。
	GetKey() string

	// GetValue 格式化并返回服务 value。
	// 该 value 常用于 KV 型注册中心。
	GetValue() string

	// GetPrefix 格式化并返回服务 key 前缀。
	// 该前缀常用于 KV 型注册中心的服务检索。
	//
	// 以 etcd 为例，前缀检索通常类似：
	// `etcdctl get /services/prod/hello.svc --prefix`
	GetPrefix() string

	// GetMetadata 返回服务元数据。
	// Metadata 是用于描述服务扩展属性的键值映射。
	GetMetadata() Metadata

	// GetEndpoints 返回服务 endpoint 列表。
	// Endpoints 包含服务的多个 host/port 信息。
	GetEndpoints() Endpoints
}

// Endpoint 定义服务地址端点。
type Endpoint interface {
	// Host 返回服务的 IPv4/IPv6 地址。
	Host() string

	// Port 返回服务端口。
	Port() int

	// String 将 endpoint 格式化为字符串。
	String() string
}

// Endpoints 表示多个 Endpoint 的集合。
type Endpoints []Endpoint

// Metadata 存储服务自定义键值对。
type Metadata map[string]any

// SearchInput 是服务检索输入参数。
type SearchInput struct {
	Prefix   string   // 按 key 前缀检索。
	Name     string   // 按服务名检索。
	Version  string   // 按版本检索。
	Metadata Metadata // 当结果有多个时，按 metadata 进一步过滤。
}
