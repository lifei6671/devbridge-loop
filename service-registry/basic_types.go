package serviceregistry

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
)

const (
	defaultVersion = "latest"
)

// BasicEndpoint 是 Endpoint 的默认实现。
type BasicEndpoint struct {
	HostValue string `json:"host"`
	PortValue int    `json:"port"`
}

// NewBasicEndpoint 创建默认 endpoint。
func NewBasicEndpoint(host string, port int) BasicEndpoint {
	return BasicEndpoint{
		HostValue: strings.TrimSpace(host),
		PortValue: port,
	}
}

// Host 返回 endpoint 主机地址。
func (e BasicEndpoint) Host() string {
	return strings.TrimSpace(e.HostValue)
}

// Port 返回 endpoint 端口号。
func (e BasicEndpoint) Port() int {
	return e.PortValue
}

// String 返回 host:port 字符串。
func (e BasicEndpoint) String() string {
	host := strings.TrimSpace(e.HostValue)
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", e.PortValue))
}

// BasicService 是 Service 的默认实现。
type BasicService struct {
	Name      string   `json:"name"`
	Version   string   `json:"version,omitempty"`
	Key       string   `json:"key,omitempty"`
	Value     string   `json:"value,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Metadata  Metadata `json:"metadata,omitempty"`
	Endpoints Endpoints
}

// ServiceOption 用于构造 BasicService。
type ServiceOption func(*BasicService)

// WithServiceVersion 设置版本号。
func WithServiceVersion(version string) ServiceOption {
	return func(service *BasicService) {
		service.Version = strings.TrimSpace(version)
	}
}

// WithServiceMetadata 设置元数据。
func WithServiceMetadata(metadata Metadata) ServiceOption {
	return func(service *BasicService) {
		service.Metadata = copyMetadata(metadata)
	}
}

// WithServiceEndpoints 设置 endpoint 列表。
func WithServiceEndpoints(endpoints Endpoints) ServiceOption {
	return func(service *BasicService) {
		service.Endpoints = copyEndpoints(endpoints)
	}
}

// WithServiceKey 指定服务唯一 key（覆盖默认格式）。
func WithServiceKey(key string) ServiceOption {
	return func(service *BasicService) {
		service.Key = strings.TrimSpace(key)
	}
}

// WithServiceValue 指定服务 value（覆盖默认格式）。
func WithServiceValue(value string) ServiceOption {
	return func(service *BasicService) {
		service.Value = strings.TrimSpace(value)
	}
}

// WithServicePrefix 指定服务前缀（覆盖默认格式）。
func WithServicePrefix(prefix string) ServiceOption {
	return func(service *BasicService) {
		service.Prefix = strings.TrimSpace(prefix)
	}
}

// NewBasicService 创建默认 Service。
func NewBasicService(name string, options ...ServiceOption) *BasicService {
	service := &BasicService{
		Name:      strings.TrimSpace(name),
		Version:   defaultVersion,
		Metadata:  Metadata{},
		Endpoints: Endpoints{},
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		option(service)
	}
	service.Version = normalizeVersion(service.Version)
	if service.Metadata == nil {
		service.Metadata = Metadata{}
	}
	if service.Endpoints == nil {
		service.Endpoints = Endpoints{}
	}
	return service
}

// GetName 返回服务名。
func (s *BasicService) GetName() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.Name)
}

// GetVersion 返回服务版本；未设置时返回 latest。
func (s *BasicService) GetVersion() string {
	if s == nil {
		return defaultVersion
	}
	return normalizeVersion(s.Version)
}

// GetKey 返回服务唯一 key。
func (s *BasicService) GetKey() string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.Key) != "" {
		return strings.TrimSpace(s.Key)
	}
	instanceID := strings.TrimSpace(stringMetadataValue(s.Metadata, "instanceId"))
	if instanceID == "" {
		instanceID = strings.TrimSpace(s.GetName())
	}
	return fmt.Sprintf("%s/%s/%s", s.GetPrefix(), s.GetVersion(), instanceID)
}

// GetValue 返回服务 value；默认是一个稳定 JSON 字符串。
func (s *BasicService) GetValue() string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.Value) != "" {
		return strings.TrimSpace(s.Value)
	}

	endpoints := make([]string, 0, len(s.Endpoints))
	for _, endpoint := range s.Endpoints {
		if endpoint == nil {
			continue
		}
		endpoints = append(endpoints, endpoint.String())
	}
	sort.Strings(endpoints)

	payload := map[string]any{
		"name":      s.GetName(),
		"version":   s.GetVersion(),
		"metadata":  copyMetadata(s.Metadata),
		"endpoints": endpoints,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 兜底路径：理论上不应失败，失败时至少保证 value 可读。
		return fmt.Sprintf("%s|%s|%v|%v", s.GetName(), s.GetVersion(), payload["metadata"], endpoints)
	}
	return string(encoded)
}

// GetPrefix 返回服务 key 前缀。
func (s *BasicService) GetPrefix() string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.Prefix) != "" {
		return strings.TrimSpace(s.Prefix)
	}
	env := strings.TrimSpace(stringMetadataValue(s.Metadata, "env"))
	if env == "" {
		env = "default"
	}
	name := strings.TrimSpace(s.GetName())
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("/services/%s/%s", env, name)
}

// GetMetadata 返回元数据副本，防止调用方篡改内部状态。
func (s *BasicService) GetMetadata() Metadata {
	if s == nil {
		return Metadata{}
	}
	return copyMetadata(s.Metadata)
}

// GetEndpoints 返回 endpoint 副本，避免外部引用修改内部数组。
func (s *BasicService) GetEndpoints() Endpoints {
	if s == nil {
		return Endpoints{}
	}
	return copyEndpoints(s.Endpoints)
}

func normalizeVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return defaultVersion
	}
	return trimmed
}

func copyMetadata(metadata Metadata) Metadata {
	if len(metadata) == 0 {
		return Metadata{}
	}
	copied := make(Metadata, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func copyEndpoints(endpoints Endpoints) Endpoints {
	if len(endpoints) == 0 {
		return Endpoints{}
	}
	copied := make(Endpoints, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == nil {
			continue
		}
		copied = append(copied, NewBasicEndpoint(endpoint.Host(), endpoint.Port()))
	}
	return copied
}

func stringMetadataValue(metadata Metadata, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	value, exists := metadata[key]
	if !exists {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}
