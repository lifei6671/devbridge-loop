package forwarder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

// ForwardError 表示本地回流转发失败，并携带统一错误码。
type ForwardError struct {
	Code    domain.ErrorCode
	Message string
	Cause   error
}

func (e *ForwardError) Error() string {
	if e.Cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Message, e.Cause.Error())
}

func (e *ForwardError) Unwrap() error {
	return e.Cause
}

// HTTPForwarder 负责将回流请求转发到本地服务端点。
type HTTPForwarder struct {
	httpClient *http.Client
}

// NewHTTPForwarder 创建本地 HTTP 转发器。
func NewHTTPForwarder(timeout time.Duration) *HTTPForwarder {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPForwarder{
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Forward 执行一次回流转发并返回上游响应。
func (f *HTTPForwarder) Forward(ctx context.Context, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	targetURL, err := buildTargetURL(request.TargetHost, request.TargetPort, request.Path, request.RawQuery)
	if err != nil {
		return domain.BackflowHTTPResponse{}, &ForwardError{
			Code:    domain.ErrorLocalEndpointDown,
			Message: "invalid local endpoint target",
			Cause:   err,
		}
	}

	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}

	outgoingReq, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(request.Body))
	if err != nil {
		return domain.BackflowHTTPResponse{}, &ForwardError{
			Code:    domain.ErrorLocalEndpointDown,
			Message: "build outgoing request failed",
			Cause:   err,
		}
	}
	if host := strings.TrimSpace(request.Host); host != "" {
		outgoingReq.Host = host
	}

	// 回流转发透传业务请求头，但需要过滤 hop-by-hop 头避免代理语义冲突。
	for name, values := range request.Headers {
		if isHopByHopHeader(name) {
			continue
		}
		for _, value := range values {
			outgoingReq.Header.Add(name, value)
		}
	}

	resp, err := f.httpClient.Do(outgoingReq)
	if err != nil {
		if isTimeoutError(err) {
			return domain.BackflowHTTPResponse{}, &ForwardError{
				Code:    domain.ErrorUpstreamTimeout,
				Message: "forward to local endpoint timeout",
				Cause:   err,
			}
		}
		return domain.BackflowHTTPResponse{}, &ForwardError{
			Code:    domain.ErrorLocalEndpointDown,
			Message: "forward to local endpoint failed",
			Cause:   err,
		}
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.BackflowHTTPResponse{}, &ForwardError{
			Code:    domain.ErrorLocalEndpointDown,
			Message: "read local endpoint response failed",
			Cause:   err,
		}
	}

	return domain.BackflowHTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    cloneHeaders(resp.Header),
		Body:       responseBody,
	}, nil
}

func buildTargetURL(host string, port int, path string, rawQuery string) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("target host is empty")
	}
	if port <= 0 {
		return "", fmt.Errorf("target port must be positive")
	}
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	u := &url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)),
		Path:     path,
		RawQuery: rawQuery,
	}
	return u.String(), nil
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errorsAs(err, &netErr) && netErr.Timeout()
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func cloneHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		copied := make([]string, 0, len(values))
		for _, value := range values {
			copied = append(copied, value)
		}
		result[name] = copied
	}
	return result
}

// errorsAs 独立封装，便于单测中覆盖网络错误判断逻辑。
var errorsAs = func(err error, target any) bool {
	return errors.As(err, target)
}
