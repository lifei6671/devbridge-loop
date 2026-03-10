package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NacosConfig 定义 Nacos 服务发现参数。
type NacosConfig struct {
	ServerAddr     string
	Namespace      string
	Group          string
	ServicePattern string
	Username       string
	Password       string
	Timeout        time.Duration
}

// NacosResolver 通过 Nacos OpenAPI 查询目标实例。
type NacosResolver struct {
	baseURL        string
	namespace      string
	group          string
	servicePattern string
	username       string
	password       string
	httpClient     *http.Client
	enabled        bool
}

// NewNacosResolver 创建 Nacos 发现器。
func NewNacosResolver(cfg NacosConfig) *NacosResolver {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	resolver := &NacosResolver{
		namespace:      strings.TrimSpace(cfg.Namespace),
		group:          strings.TrimSpace(cfg.Group),
		servicePattern: strings.TrimSpace(cfg.ServicePattern),
		username:       strings.TrimSpace(cfg.Username),
		password:       strings.TrimSpace(cfg.Password),
		httpClient:     &http.Client{Timeout: timeout},
	}
	if resolver.group == "" {
		resolver.group = "DEFAULT_GROUP"
	}
	if resolver.servicePattern == "" {
		resolver.servicePattern = "${service}"
	}

	baseURL, ok := normalizeHTTPBaseURL(cfg.ServerAddr)
	if !ok {
		return resolver
	}
	resolver.baseURL = baseURL
	resolver.enabled = true
	return resolver
}

// Name 返回 resolver 名称。
func (r *NacosResolver) Name() string { return "nacos" }

// Resolve 调用 Nacos `/nacos/v1/ns/instance/list` 查询实例。
func (r *NacosResolver) Resolve(ctx context.Context, query Query) (Endpoint, bool, error) {
	if r == nil || !r.enabled {
		return Endpoint{}, false, nil
	}
	normalizedQuery := query.Normalize()

	endpoint := r.baseURL + "/nacos/v1/ns/instance/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("create nacos request failed: %w", err)
	}

	params := req.URL.Query()
	params.Set("serviceName", renderServiceName(r.servicePattern, normalizedQuery))
	params.Set("healthyOnly", "true")
	if r.group != "" {
		params.Set("groupName", r.group)
	}
	if r.namespace != "" {
		params.Set("namespaceId", r.namespace)
	}
	if r.username != "" {
		params.Set("username", r.username)
	}
	if r.password != "" {
		params.Set("password", r.password)
	}
	req.URL.RawQuery = params.Encode()

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("send nacos request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return Endpoint{}, false, fmt.Errorf("nacos returned status %d", resp.StatusCode)
	}

	var payload nacosInstanceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Endpoint{}, false, fmt.Errorf("decode nacos response failed: %w", err)
	}

	for _, host := range payload.Hosts {
		// Nacos 同时返回 healthy/enabled，任一不满足直接跳过，避免引流到异常实例。
		if !host.Healthy || !host.Enabled {
			continue
		}
		if strings.TrimSpace(host.IP) == "" || host.Port <= 0 {
			continue
		}
		if value, ok := pickMetadata(host.Metadata, "env", "x-env"); ok && normalizeToken(value) != normalizedQuery.Env {
			continue
		}
		if value, ok := pickMetadata(host.Metadata, "protocol", "x-protocol"); ok && normalizeProtocol(value) != normalizedQuery.Protocol {
			continue
		}
		return Endpoint{
			Host:     strings.TrimSpace(host.IP),
			Port:     host.Port,
			Source:   "nacos",
			Metadata: copyMetadata(host.Metadata),
		}, true, nil
	}
	return Endpoint{}, false, nil
}

type nacosInstanceListResponse struct {
	Hosts []nacosInstanceHost `json:"hosts"`
}

type nacosInstanceHost struct {
	IP       string            `json:"ip"`
	Port     int               `json:"port"`
	Healthy  bool              `json:"healthy"`
	Enabled  bool              `json:"enabled"`
	Metadata map[string]string `json:"metadata"`
}

func normalizeHTTPBaseURL(rawValue string) (string, bool) {
	value := strings.TrimSpace(rawValue)
	if value == "" {
		return "", false
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", false
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func pickMetadata(metadata map[string]string, keys ...string) (string, bool) {
	if len(metadata) == 0 {
		return "", false
	}
	for _, key := range keys {
		if value, ok := metadata[key]; ok && strings.TrimSpace(value) != "" {
			return value, true
		}
		if value, ok := metadata[strings.ToUpper(key)]; ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func parsePortFromString(value string) int {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return port
}
