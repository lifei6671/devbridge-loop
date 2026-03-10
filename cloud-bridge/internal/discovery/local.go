package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LocalFileConfig 是本地文件发现配置格式。
type LocalFileConfig struct {
	Routes []LocalRoute `json:"routes"`
}

// LocalRoute 定义一条静态服务发现映射。
type LocalRoute struct {
	Env         string            `json:"env"`
	ServiceName string            `json:"serviceName"`
	Protocol    string            `json:"protocol"`
	Host        string            `json:"host,omitempty"`
	Address     string            `json:"address,omitempty"`
	Port        int               `json:"port"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// LocalFileResolver 基于本地 JSON 配置文件实现服务发现。
type LocalFileResolver struct {
	path   string
	routes []LocalRoute
}

// NewLocalFileResolver 创建本地文件发现器。
func NewLocalFileResolver(path string) (*LocalFileResolver, error) {
	resolver := &LocalFileResolver{
		path: strings.TrimSpace(path),
	}
	if resolver.path == "" {
		return resolver, nil
	}

	content, err := os.ReadFile(resolver.path)
	if err != nil {
		// 本地文件不存在时不阻塞启动，允许继续使用其他配置中心。
		if errors.Is(err, os.ErrNotExist) {
			return resolver, nil
		}
		return nil, fmt.Errorf("read local discovery file %q failed: %w", resolver.path, err)
	}

	var payload LocalFileConfig
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("decode local discovery file %q failed: %w", resolver.path, err)
	}
	resolver.routes = payload.Routes
	return resolver, nil
}

// Name 返回 resolver 名称。
func (r *LocalFileResolver) Name() string { return "local" }

// Resolve 在本地路由表中查找完全匹配 (env,service,protocol) 的记录。
func (r *LocalFileResolver) Resolve(_ context.Context, query Query) (Endpoint, bool, error) {
	if r == nil {
		return Endpoint{}, false, nil
	}
	normalizedQuery := query.Normalize()
	for _, route := range r.routes {
		normalizedRoute := normalizeLocalRoute(route)
		if normalizedRoute.Env != normalizedQuery.Env {
			continue
		}
		if normalizedRoute.ServiceName != normalizedQuery.ServiceName {
			continue
		}
		// 协议为空表示通配，便于本地配置快速兜底。
		if normalizedRoute.Protocol != "" && normalizedRoute.Protocol != normalizedQuery.Protocol {
			continue
		}
		return Endpoint{
			Host:     normalizedRoute.Host,
			Port:     normalizedRoute.Port,
			Source:   "local",
			Metadata: copyMetadata(normalizedRoute.Metadata),
		}, true, nil
	}
	return Endpoint{}, false, nil
}

func normalizeLocalRoute(route LocalRoute) LocalRoute {
	route.Env = normalizeToken(route.Env)
	route.ServiceName = normalizeToken(route.ServiceName)
	switch normalizeToken(route.Protocol) {
	case "", "*", "any":
		route.Protocol = ""
	default:
		route.Protocol = normalizeProtocol(route.Protocol)
	}
	route.Host = strings.TrimSpace(route.Host)
	route.Address = strings.TrimSpace(route.Address)
	if route.Host == "" {
		route.Host = route.Address
	}
	return route
}
