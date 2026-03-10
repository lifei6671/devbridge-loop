package discovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// EtcdConfig 定义 etcd 发现参数。
type EtcdConfig struct {
	Endpoints []string
	KeyPrefix string
	Timeout   time.Duration
}

// EtcdResolver 通过 etcd v3 HTTP API 查询服务地址。
type EtcdResolver struct {
	endpoints  []string
	keyPrefix  string
	httpClient *http.Client
}

// NewEtcdResolver 创建 etcd 发现器。
func NewEtcdResolver(cfg EtcdConfig) *EtcdResolver {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	endpoints := make([]string, 0, len(cfg.Endpoints))
	for _, endpoint := range cfg.Endpoints {
		if normalized, ok := normalizeHTTPBaseURL(endpoint); ok {
			endpoints = append(endpoints, normalized)
		}
	}

	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = "/devloop/services"
	}
	keyPrefix = "/" + strings.Trim(path.Clean("/"+keyPrefix), "/")

	return &EtcdResolver{
		endpoints: endpoints,
		keyPrefix: keyPrefix,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Name 返回 resolver 名称。
func (r *EtcdResolver) Name() string { return "etcd" }

// Resolve 依次读取 keyPrefix/env/service/protocol 与 keyPrefix/env/service。
func (r *EtcdResolver) Resolve(ctx context.Context, query Query) (Endpoint, bool, error) {
	if r == nil || len(r.endpoints) == 0 {
		return Endpoint{}, false, nil
	}
	normalizedQuery := query.Normalize()
	keys := []string{
		fmt.Sprintf("%s/%s/%s/%s", r.keyPrefix, normalizedQuery.Env, normalizedQuery.ServiceName, normalizedQuery.Protocol),
		fmt.Sprintf("%s/%s/%s", r.keyPrefix, normalizedQuery.Env, normalizedQuery.ServiceName),
	}

	var joinedErr error
	for _, endpoint := range r.endpoints {
		for _, key := range keys {
			result, matched, err := r.resolveFromEndpoint(ctx, endpoint, key, normalizedQuery)
			if err != nil {
				joinedErr = errors.Join(joinedErr, err)
				continue
			}
			if matched {
				return result, true, nil
			}
		}
	}
	return Endpoint{}, false, joinedErr
}

func (r *EtcdResolver) resolveFromEndpoint(ctx context.Context, endpoint string, key string, query Query) (Endpoint, bool, error) {
	requestBody, err := json.Marshal(map[string]any{
		"key":   base64.StdEncoding.EncodeToString([]byte(key)),
		"limit": 1,
	})
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("encode etcd request failed: %w", err)
	}

	requestURL := endpoint + "/v3/kv/range"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(requestBody))
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("create etcd request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("send etcd request to %s failed: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return Endpoint{}, false, fmt.Errorf("etcd %s returned status %d", endpoint, resp.StatusCode)
	}

	var payload etcdRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Endpoint{}, false, fmt.Errorf("decode etcd response failed: %w", err)
	}
	if len(payload.KVs) == 0 {
		return Endpoint{}, false, nil
	}

	rawValue, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.KVs[0].Value))
	if err != nil {
		return Endpoint{}, false, fmt.Errorf("decode etcd value failed: %w", err)
	}
	endpointResult, err := parseEtcdEndpointValue(rawValue, query)
	if err != nil {
		return Endpoint{}, false, err
	}
	endpointResult.Source = "etcd"
	return endpointResult, true, nil
}

type etcdRangeResponse struct {
	KVs []etcdKV `json:"kvs"`
}

type etcdKV struct {
	Value string `json:"value"`
}

type etcdEndpointValue struct {
	Host        string            `json:"host"`
	Address     string            `json:"address"`
	Port        int               `json:"port"`
	Env         string            `json:"env,omitempty"`
	ServiceName string            `json:"serviceName,omitempty"`
	Protocol    string            `json:"protocol,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func parseEtcdEndpointValue(rawValue []byte, query Query) (Endpoint, error) {
	trimmed := strings.TrimSpace(string(rawValue))
	if trimmed == "" {
		return Endpoint{}, errors.New("empty etcd endpoint value")
	}

	// 优先解析结构化 JSON，便于携带 env/protocol 元信息做二次过滤。
	var payload etcdEndpointValue
	if err := json.Unmarshal(rawValue, &payload); err == nil {
		host := strings.TrimSpace(payload.Host)
		if host == "" {
			host = strings.TrimSpace(payload.Address)
		}
		if host == "" || payload.Port <= 0 {
			return Endpoint{}, fmt.Errorf("invalid etcd endpoint json payload: %q", trimmed)
		}
		if env := normalizeToken(payload.Env); env != "" && env != query.Env {
			return Endpoint{}, fmt.Errorf("etcd endpoint env mismatch: %s != %s", env, query.Env)
		}
		if protocol := strings.TrimSpace(payload.Protocol); protocol != "" && normalizeProtocol(protocol) != query.Protocol {
			return Endpoint{}, fmt.Errorf("etcd endpoint protocol mismatch: %s != %s", normalizeProtocol(protocol), query.Protocol)
		}
		return Endpoint{
			Host:     host,
			Port:     payload.Port,
			Metadata: copyMetadata(payload.Metadata),
		}, nil
	}

	// 兼容极简配置：value 直接写成 host:port。
	host, port, err := splitHostPort(trimmed)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse etcd endpoint value %q failed: %w", trimmed, err)
	}
	return Endpoint{Host: host, Port: port}, nil
}

func splitHostPort(value string) (string, int, error) {
	host, rawPort, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		// 当配置为 URL 时尝试按 URL 形式再次解析，提高容错。
		parsed, parseErr := url.Parse(value)
		if parseErr != nil || strings.TrimSpace(parsed.Host) == "" {
			return "", 0, err
		}
		host, rawPort, err = net.SplitHostPort(parsed.Host)
		if err != nil {
			return "", 0, err
		}
	}

	port := parsePortFromString(rawPort)
	if strings.TrimSpace(host) == "" || port <= 0 {
		return "", 0, fmt.Errorf("invalid host/port: %s", value)
	}
	return host, port, nil
}
