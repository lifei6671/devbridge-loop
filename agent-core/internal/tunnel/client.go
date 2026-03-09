package tunnel

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

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

// BridgeReplyError 表示 bridge 返回了 ERROR 回包或非法 ACK 语义。
type BridgeReplyError struct {
	Code    string
	Message string
	Reply   domain.TunnelReply
}

func (e *BridgeReplyError) Error() string {
	if strings.TrimSpace(e.Code) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// BridgeClient 负责向 cloud-bridge 发送 tunnel 同步事件。
type BridgeClient struct {
	eventEndpoint string
	httpClient    *http.Client
}

// NewBridgeClient 构建 bridge API 客户端。
func NewBridgeClient(bridgeAddress string, timeout time.Duration) *BridgeClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &BridgeClient{
		eventEndpoint: buildEventEndpoint(bridgeAddress),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// SendEvent 发送一条同步消息，并解析 bridge ACK/ERROR 回包。
func (c *BridgeClient) SendEvent(ctx context.Context, message domain.TunnelMessage) (domain.TunnelReply, error) {
	body, err := json.Marshal(message)
	if err != nil {
		return domain.TunnelReply{}, fmt.Errorf("encode tunnel message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.eventEndpoint, bytes.NewReader(body))
	if err != nil {
		return domain.TunnelReply{}, fmt.Errorf("create bridge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return domain.TunnelReply{}, fmt.Errorf("send bridge request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.TunnelReply{}, fmt.Errorf("read bridge response: %w", err)
	}

	var reply domain.TunnelReply
	if len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, &reply); err != nil {
			return domain.TunnelReply{}, fmt.Errorf("decode bridge response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return reply, &BridgeReplyError{
			Code:    reply.ErrorCode,
			Message: firstNonEmpty(reply.Message, fmt.Sprintf("bridge status code %d", resp.StatusCode)),
			Reply:   reply,
		}
	}

	if reply.Type == domain.MessageError || strings.EqualFold(reply.Status, "rejected") {
		return reply, &BridgeReplyError{
			Code:    firstNonEmpty(reply.ErrorCode, "BRIDGE_REJECTED"),
			Message: firstNonEmpty(reply.Message, "bridge rejected event"),
			Reply:   reply,
		}
	}

	return reply, nil
}

func buildEventEndpoint(bridgeAddress string) string {
	address := strings.TrimSpace(bridgeAddress)
	if address == "" {
		address = "http://127.0.0.1:18080"
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return "http://127.0.0.1:18080/api/v1/tunnel/events"
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/api/v1/tunnel/events") {
		parsed.Path = path
	} else {
		parsed.Path = path + "/api/v1/tunnel/events"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
