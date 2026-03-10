package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConsulConfig 定义 Consul 发现参数。
type ConsulConfig struct {
	Addr           string
	Datacenter     string
	ServicePattern string
	Timeout        time.Duration
}

// ConsulResolver 通过 Consul Health API 查询健康实例。
type ConsulResolver struct {
	baseURL        string
	datacenter     string
	servicePattern string
	httpClient     *http.Client
	enabled        bool
}

// NewConsulResolver 创建 Consul 发现器。
func NewConsulResolver(cfg ConsulConfig) *ConsulResolver {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	resolver := &ConsulResolver{
		datacenter:     strings.TrimSpace(cfg.Datacenter),
		servicePattern: strings.TrimSpace(cfg.ServicePattern),
		httpClient:     &http.Client{Timeout: timeout},
	}
	if resolver.servicePattern == "" {
		resolver.servicePattern = "${service}"
	}
	baseURL, ok := normalizeHTTPBaseURL(cfg.Addr)
	if !ok {
		return resolver
	}
	resolver.baseURL = baseURL
	resolver.enabled = true
	return resolver
}

// Name 返回 resolver 名称。
func (r *ConsulResolver) Name() string { return "consul" }

// Resolve 查询 `/v1/health/service/{service}?passing=true` 并返回首个可用实例。
func (r *ConsulResolver) Resolve(ctx context.Context, query Query) (Endpoint, bool, error) {
	if r == nil || !r.enabled {
		return Endpoint{}, false, nil
	}
	normalizedQuery := query.Normalize()
	serviceName := renderServiceName(r.servicePattern, normalizedQuery)
	if serviceName == "" {
		return Endpoint{}, false, nil
	}

	requestURL := fmt.Sprintf("%s/v1/health/service/%s", r.baseURL, url.PathEscape(serviceName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("create consul request failed: %w", err)
	}
	params := req.URL.Query()
	params.Set("passing", "true")
	if r.datacenter != "" {
		params.Set("dc", r.datacenter)
	}
	req.URL.RawQuery = params.Encode()

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("send consul request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return Endpoint{}, false, fmt.Errorf("consul returned status %d", resp.StatusCode)
	}

	var entries []consulServiceEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return Endpoint{}, false, fmt.Errorf("decode consul response failed: %w", err)
	}
	for _, entry := range entries {
		host := strings.TrimSpace(entry.Service.Address)
		if host == "" {
			host = strings.TrimSpace(entry.Node.Address)
		}
		if host == "" || entry.Service.Port <= 0 {
			continue
		}
		if !matchConsulEntry(entry, normalizedQuery) {
			continue
		}
		return Endpoint{
			Host:     host,
			Port:     entry.Service.Port,
			Source:   "consul",
			Metadata: copyMetadata(entry.Service.Meta),
		}, true, nil
	}
	return Endpoint{}, false, nil
}

type consulServiceEntry struct {
	Node struct {
		Address string `json:"Address"`
	} `json:"Node"`
	Service struct {
		Address string            `json:"Address"`
		Port    int               `json:"Port"`
		Tags    []string          `json:"Tags"`
		Meta    map[string]string `json:"Meta"`
	} `json:"Service"`
}

func matchConsulEntry(entry consulServiceEntry, query Query) bool {
	meta := entry.Service.Meta
	if value, ok := pickMetadata(meta, "env", "x-env"); ok && normalizeToken(value) != query.Env {
		return false
	}
	if value, ok := pickMetadata(meta, "protocol", "x-protocol"); ok && normalizeProtocol(value) != query.Protocol {
		return false
	}

	// 标签支持 env:xxx / protocol:xxx，两类标签任一冲突都视为不匹配。
	tagEnv, hasTagEnv := pickConsulTagValue(entry.Service.Tags, "env")
	if hasTagEnv && normalizeToken(tagEnv) != query.Env {
		return false
	}
	tagProtocol, hasTagProtocol := pickConsulTagValue(entry.Service.Tags, "protocol")
	if hasTagProtocol && normalizeProtocol(tagProtocol) != query.Protocol {
		return false
	}
	return true
}

func pickConsulTagValue(tags []string, key string) (string, bool) {
	prefix := strings.ToLower(strings.TrimSpace(key)) + ":"
	for _, tag := range tags {
		normalizedTag := strings.ToLower(strings.TrimSpace(tag))
		if strings.HasPrefix(normalizedTag, prefix) {
			rawTag := strings.TrimSpace(tag)
			separatorIndex := strings.Index(rawTag, ":")
			if separatorIndex <= 0 || separatorIndex >= len(rawTag)-1 {
				return "", false
			}
			return strings.TrimSpace(rawTag[separatorIndex+1:]), true
		}
	}
	return "", false
}
