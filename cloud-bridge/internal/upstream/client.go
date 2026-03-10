package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/discovery"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Client 负责在 bridge 侧直连上游服务（不经过 agent tunnel）。
type Client struct {
	httpClient  *http.Client
	dialTimeout time.Duration
}

// NewClient 创建直连上游客户端。
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		httpClient:  &http.Client{Timeout: timeout},
		dialTimeout: timeout,
	}
}

// ForwardHTTP 将 ingress 请求直接转发到发现到的目标实例。
func (c *Client) ForwardHTTP(ctx context.Context, endpoint discovery.Endpoint, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	targetURL, err := buildTargetHTTPURL(endpoint, request.Path, request.RawQuery)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("build upstream url failed: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(request.Body))
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("create upstream http request failed: %w", err)
	}
	if strings.TrimSpace(request.Host) != "" {
		httpReq.Host = strings.TrimSpace(request.Host)
	}
	copyHeaders(httpReq.Header, request.Headers)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("send upstream http request failed: %w", err)
	}
	defer resp.Body.Close()

	payloadBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.BackflowHTTPResponse{}, fmt.Errorf("read upstream http response failed: %w", err)
	}

	return domain.BackflowHTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    cloneHeaders(resp.Header),
		Body:       payloadBody,
		ErrorCode:  "",
		Message:    "",
	}, nil
}

// ForwardGRPC 直连目标实例执行 gRPC Health Check。
func (c *Client) ForwardGRPC(ctx context.Context, endpoint discovery.Endpoint, request domain.BackflowGRPCRequest) (domain.BackflowGRPCResponse, error) {
	target := net.JoinHostPort(strings.TrimSpace(endpoint.Host), strconv.Itoa(endpoint.Port))
	dialCtx, cancelDial := context.WithTimeout(ctx, c.dialTimeout)
	defer cancelDial()

	conn, err := grpc.DialContext(
		dialCtx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return domain.BackflowGRPCResponse{}, fmt.Errorf("dial grpc upstream failed: %w", err)
	}
	defer conn.Close()

	timeout := time.Duration(request.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1200 * time.Millisecond
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, timeout)
	defer cancelCheck()

	startedAt := time.Now()
	response, err := healthpb.NewHealthClient(conn).Check(checkCtx, &healthpb.HealthCheckRequest{
		Service: strings.TrimSpace(request.HealthService),
	})
	if err != nil {
		return domain.BackflowGRPCResponse{}, fmt.Errorf("grpc health check failed: %w", err)
	}

	return domain.BackflowGRPCResponse{
		Status:    response.Status.String(),
		Target:    target,
		LatencyMs: time.Since(startedAt).Milliseconds(),
		ErrorCode: "",
		Message:   "",
	}, nil
}

func buildTargetHTTPURL(endpoint discovery.Endpoint, rawPath string, rawQuery string) (string, error) {
	targetHost := strings.TrimSpace(endpoint.Host)
	if targetHost == "" || endpoint.Port <= 0 {
		return "", fmt.Errorf("invalid upstream endpoint: %s:%d", endpoint.Host, endpoint.Port)
	}
	requestPath := strings.TrimSpace(rawPath)
	if requestPath == "" {
		requestPath = "/"
	}
	if !strings.HasPrefix(requestPath, "/") {
		requestPath = "/" + requestPath
	}
	targetURL := &url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort(targetHost, strconv.Itoa(endpoint.Port)),
		Path:     requestPath,
		RawQuery: strings.TrimSpace(rawQuery),
	}
	return targetURL.String(), nil
}

func copyHeaders(dst http.Header, src map[string][]string) {
	for name, values := range src {
		if isHopByHopHeader(name) {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func cloneHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		copiedValues := make([]string, 0, len(values))
		for _, value := range values {
			copiedValues = append(copiedValues, value)
		}
		result[name] = copiedValues
	}
	return result
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}
