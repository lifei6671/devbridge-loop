package ingress

import (
	"fmt"
	"net"
	"strings"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RouteLookupRequest 描述由入口归一化后的路由请求。
type RouteLookupRequest struct {
	IngressMode pb.IngressMode
	Protocol    string
	Host        string
	Authority   string
	Path        string
	SNI         string
	ListenPort  uint32
	Namespace   string
	Environment string
	Metadata    map[string]string
}

// HTTPGatewayRequest 描述 HTTP 入口请求上下文。
type HTTPGatewayRequest struct {
	Protocol    string
	Host        string
	Authority   string
	Path        string
	Namespace   string
	Environment string
	Metadata    map[string]string
}

type l7GatewayRequest struct {
	Protocol    string
	Host        string
	Authority   string
	Path        string
	Namespace   string
	Environment string
	Metadata    map[string]string
}

// HTTPGateway 负责把 HTTP 入口请求转换为路由匹配输入。
type HTTPGateway struct {
	ListenPort uint32
}

// NewHTTPGateway 创建 L7 HTTP 入口适配器。
func NewHTTPGateway(listenPort uint32) *HTTPGateway {
	return &HTTPGateway{ListenPort: listenPort}
}

// BuildRouteLookupRequest 将 HTTP 请求转换为统一路由请求。
func (gateway *HTTPGateway) BuildRouteLookupRequest(request HTTPGatewayRequest) (RouteLookupRequest, error) {
	return buildL7RouteLookupRequest(gateway.ListenPort, "http", l7GatewayRequest{
		Protocol:    request.Protocol,
		Host:        request.Host,
		Authority:   request.Authority,
		Path:        request.Path,
		Namespace:   request.Namespace,
		Environment: request.Environment,
		Metadata:    request.Metadata,
	})
}

func buildL7RouteLookupRequest(
	listenPort uint32,
	defaultProtocol string,
	request l7GatewayRequest,
) (RouteLookupRequest, error) {
	normalizedProtocol := strings.ToLower(strings.TrimSpace(request.Protocol))
	if normalizedProtocol == "" {
		normalizedProtocol = strings.ToLower(strings.TrimSpace(defaultProtocol))
	}
	if normalizedProtocol == "" {
		return RouteLookupRequest{}, fmt.Errorf("build route lookup request: missing protocol")
	}
	normalizedRawHost := strings.ToLower(strings.TrimSpace(request.Host))
	normalizedHost := normalizeHost(request.Host)
	normalizedAuthority := strings.ToLower(strings.TrimSpace(request.Authority))
	if normalizedAuthority == "" && normalizedRawHost != "" {
		normalizedAuthority = normalizedRawHost
	}
	if normalizedAuthority == "" && normalizedHost != "" {
		normalizedAuthority = normalizedHost
	}
	if normalizedHost == "" && normalizedAuthority != "" {
		normalizedHost = normalizeHost(normalizedAuthority)
	}
	if normalizedAuthority == "" {
		return RouteLookupRequest{}, fmt.Errorf("build route lookup request: missing authority/host")
	}
	normalizedPath := normalizePath(request.Path)
	return RouteLookupRequest{
		IngressMode: pb.IngressModeL7Shared,
		Protocol:    normalizedProtocol,
		Host:        normalizedHost,
		Authority:   normalizedAuthority,
		Path:        normalizedPath,
		ListenPort:  listenPort,
		Namespace:   strings.TrimSpace(request.Namespace),
		Environment: strings.TrimSpace(request.Environment),
		Metadata:    copyStringMap(request.Metadata),
	}, nil
}

func normalizeHost(host string) string {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(normalized); err == nil {
		return parsedHost
	}
	return normalized
}

func normalizePath(path string) string {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return "/"
	}
	if !strings.HasPrefix(normalized, "/") {
		return "/" + normalized
	}
	return normalized
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}
