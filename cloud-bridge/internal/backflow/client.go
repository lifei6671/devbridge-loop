package backflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

// Error 表示 bridge 调用 agent 回流接口失败。
type Error struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

func (e *Error) Error() string {
	if strings.TrimSpace(e.ErrorCode) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.ErrorCode, e.Message)
}

// Client 负责将入口请求转发给 agent 本地回流接口。
type Client struct {
	httpClient *http.Client
}

// NewClient 创建回流调用客户端。
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
	}
}

// ForwardHTTP 调用 agent 的 /api/v1/backflow/http 完成回流。
func (c *Client) ForwardHTTP(ctx context.Context, baseURL string, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	endpoint, err := buildBackflowEndpoint(baseURL)
	if err != nil {
		return domain.BackflowHTTPResponse{}, &Error{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  domain.IngressErrorTunnelOffline,
			Message:    err.Error(),
		}
	}

	body, err := json.Marshal(request)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("encode backflow request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("create backflow request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("send backflow request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("read backflow response: %w", err)
	}

	var payload domain.BackflowHTTPResponse
	if len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, &payload); err != nil {
			return domain.BackflowHTTPResponse{}, fmt.Errorf("decode backflow response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		status := payload.StatusCode
		if status == 0 {
			status = resp.StatusCode
		}
		return payload, &Error{
			StatusCode: status,
			ErrorCode:  firstNonEmpty(payload.ErrorCode, domain.IngressErrorTunnelOffline),
			Message:    firstNonEmpty(payload.Message, fmt.Sprintf("agent backflow status code %d", resp.StatusCode)),
		}
	}

	return payload, nil
}

func buildBackflowEndpoint(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("backflow base url is empty")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse backflow base url: %w", err)
	}

	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/api/v1/backflow/http") {
		parsed.Path = path
	} else {
		parsed.Path = path + "/api/v1/backflow/http"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
