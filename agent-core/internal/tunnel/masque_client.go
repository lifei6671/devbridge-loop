package tunnel

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	masque "github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

const (
	masqueAuthModePSK  = "psk"
	masqueAuthModeECDH = "ecdh"

	masqueServerPubHeader = "X-Devloop-Masque-Server-Pub"
)

type MasqueClientOptions struct {
	ProxyURL       string
	TargetAddr     string
	AuthMode       string
	PSK            string
	RequestTimeout time.Duration
}

type masqueTunnelAuth struct {
	Mode      string `json:"mode"`
	PSK       string `json:"psk,omitempty"`
	ClientPub string `json:"clientPub,omitempty"`
	Proof     string `json:"proof,omitempty"`
}

type masqueTunnelRequest struct {
	Auth  masqueTunnelAuth     `json:"auth"`
	Event domain.TunnelMessage `json:"event"`
}

type masqueTunnelResponse struct {
	Reply domain.TunnelReply `json:"reply"`
	Error string             `json:"error,omitempty"`
}

type MasqueBridgeClient struct {
	timeout       time.Duration
	targetAddr    string
	authMode      string
	psk           string
	proxyTemplate *uritemplate.Template
	client        *masque.Client

	mu            sync.Mutex
	conn          net.PacketConn
	ecdhPrivate   *ecdh.PrivateKey
	ecdhClientPub string
	ecdhProof     string
}

func NewMasqueBridgeClient(options MasqueClientOptions) (*MasqueBridgeClient, error) {
	proxyURL := strings.TrimSpace(options.ProxyURL)
	if proxyURL == "" {
		return nil, fmt.Errorf("masque proxy url is required")
	}
	template, err := uritemplate.New(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse masque proxy template failed: %w", err)
	}

	targetAddr := strings.TrimSpace(options.TargetAddr)
	if targetAddr == "" {
		targetAddr = "127.0.0.1:39081"
	}

	authMode := strings.ToLower(strings.TrimSpace(options.AuthMode))
	if authMode != masqueAuthModeECDH {
		authMode = masqueAuthModePSK
	}

	client := &MasqueBridgeClient{
		timeout:       normalizeTimeout(options.RequestTimeout),
		targetAddr:    targetAddr,
		authMode:      authMode,
		psk:           strings.TrimSpace(options.PSK),
		proxyTemplate: template,
		client: &masque.Client{
			TLSClientConfig: &tls.Config{
				NextProtos:         []string{http3.NextProtoH3},
				InsecureSkipVerify: true, // 动态证书每次重启都会变化，MASQUE 模式通过 PSK/ECDH 做应用层鉴权。
			},
			QUICConfig: &quic.Config{
				EnableDatagrams: true,
			},
		},
	}
	if client.psk == "" {
		client.psk = "devloop-masque-default-psk"
	}
	return client, nil
}

func (c *MasqueBridgeClient) SendEvent(ctx context.Context, message domain.TunnelMessage) (domain.TunnelReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 懒加载连接，首次发送时才真正发起 CONNECT-UDP。
	if err := c.ensureConn(ctx); err != nil {
		return domain.TunnelReply{}, err
	}

	// 每个 datagram 都携带鉴权信息，便于服务端快速拒绝无效流量。
	auth, err := c.buildAuthPayload()
	if err != nil {
		return domain.TunnelReply{}, err
	}

	payload, err := json.Marshal(masqueTunnelRequest{
		Auth:  auth,
		Event: message,
	})
	if err != nil {
		return domain.TunnelReply{}, fmt.Errorf("encode masque tunnel request failed: %w", err)
	}

	// 发送超时取“客户端默认超时”和“调用上下文超时”里的较小值，避免阻塞调用链。
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = c.conn.SetDeadline(deadline)

	// 连接态异常时立即丢弃当前连接，下次请求重新建链路。
	if _, err := c.conn.WriteTo(payload, nil); err != nil {
		c.closeConnLocked()
		return domain.TunnelReply{}, fmt.Errorf("write masque datagram failed: %w", err)
	}

	buffer := make([]byte, 64*1024)
	n, _, err := c.conn.ReadFrom(buffer)
	if err != nil {
		c.closeConnLocked()
		return domain.TunnelReply{}, fmt.Errorf("read masque datagram failed: %w", err)
	}

	var response masqueTunnelResponse
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return domain.TunnelReply{}, fmt.Errorf("decode masque datagram response failed: %w", err)
	}
	if strings.TrimSpace(response.Error) != "" {
		return domain.TunnelReply{}, fmt.Errorf("masque bridge rejected event: %s", response.Error)
	}
	return response.Reply, nil
}

func (c *MasqueBridgeClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closeConnLocked()
	return c.client.Close()
}

func (c *MasqueBridgeClient) ensureConn(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}

	// DialAddr 会建立 QUIC/H3 链路并发起 CONNECT-UDP，成功后返回可读写 datagram 的 PacketConn。
	conn, response, err := c.client.DialAddr(ctx, c.proxyTemplate, c.targetAddr)
	if err != nil {
		return fmt.Errorf("dial masque proxy failed: %w", err)
	}

	// ECDH 模式需要从 CONNECT-UDP 响应头读取服务端公钥，再派生本端 proof。
	if c.authMode == masqueAuthModeECDH {
		if err := c.prepareECDH(response); err != nil {
			_ = conn.Close()
			return err
		}
	}

	c.conn = conn
	return nil
}

func (c *MasqueBridgeClient) buildAuthPayload() (masqueTunnelAuth, error) {
	switch c.authMode {
	case masqueAuthModeECDH:
		if strings.TrimSpace(c.ecdhClientPub) == "" || strings.TrimSpace(c.ecdhProof) == "" {
			return masqueTunnelAuth{}, fmt.Errorf("ecdh auth is not ready")
		}
		return masqueTunnelAuth{
			Mode:      masqueAuthModeECDH,
			ClientPub: c.ecdhClientPub,
			Proof:     c.ecdhProof,
		}, nil
	default:
		return masqueTunnelAuth{
			Mode: masqueAuthModePSK,
			PSK:  c.psk,
		}, nil
	}
}

func (c *MasqueBridgeClient) prepareECDH(response *http.Response) error {
	// 服务端公钥通过响应头返回；若缺失说明服务端未按 ECDH 模式工作。
	serverPubEncoded := strings.TrimSpace(response.Header.Get(masqueServerPubHeader))
	if serverPubEncoded == "" {
		return fmt.Errorf("masque ecdh server public key is missing")
	}

	serverPubRaw, err := base64.StdEncoding.DecodeString(serverPubEncoded)
	if err != nil {
		return fmt.Errorf("decode masque ecdh server public key failed: %w", err)
	}

	curve := ecdh.X25519()
	serverPub, err := curve.NewPublicKey(serverPubRaw)
	if err != nil {
		return fmt.Errorf("parse masque ecdh server public key failed: %w", err)
	}
	clientPrivate, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate masque ecdh client key failed: %w", err)
	}
	shared, err := clientPrivate.ECDH(serverPub)
	if err != nil {
		return fmt.Errorf("derive masque ecdh shared key failed: %w", err)
	}

	// 仅缓存最小必要信息：客户端公钥和 proof，后续每条消息复用。
	c.ecdhPrivate = clientPrivate
	c.ecdhClientPub = base64.StdEncoding.EncodeToString(clientPrivate.PublicKey().Bytes())
	c.ecdhProof = buildECDHProof(shared)
	return nil
}

func (c *MasqueBridgeClient) closeConnLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func buildECDHProof(shared []byte) string {
	// 用固定上下文避免跨协议重放，proof 只用于证明双方持有同一共享密钥。
	mac := hmac.New(sha256.New, shared)
	_, _ = mac.Write([]byte("devloop-masque-auth"))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
