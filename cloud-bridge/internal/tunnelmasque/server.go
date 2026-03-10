package tunnelmasque

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	masque "github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

const (
	masqueAuthModePSK  = "psk"
	masqueAuthModeECDH = "ecdh"

	masqueServerPubHeader = "X-Devloop-Masque-Server-Pub"
	masqueAuthModeHeader  = "X-Devloop-Masque-Auth-Mode"
	masqueProofContext    = "devloop-masque-auth"
)

type datagramAuth struct {
	Mode      string `json:"mode"`
	PSK       string `json:"psk,omitempty"`
	ClientPub string `json:"clientPub,omitempty"`
	Proof     string `json:"proof,omitempty"`
}

type datagramRequest struct {
	Auth  datagramAuth       `json:"auth"`
	Event domain.TunnelEvent `json:"event"`
}

type datagramResponse struct {
	Reply domain.TunnelEventReply `json:"reply"`
	Error string                  `json:"error,omitempty"`
}

// Server 提供基于 MASQUE (CONNECT-UDP) 的 tunnel 事件传输能力。
type Server struct {
	cfg   config.Config
	store *store.MemoryStore

	proxy    *masque.Proxy
	h3Server *http3.Server
	udpConn  *net.UDPConn

	ecdhPrivate   *ecdh.PrivateKey
	ecdhPublicB64 string
	closeOnce     sync.Once
}

func NewServer(cfg config.Config, state *store.MemoryStore) (*Server, error) {
	// 每次进程启动动态生成 TLS 证书，避免依赖本地固定证书文件。
	tlsCert, err := generateRuntimeCertificate()
	if err != nil {
		return nil, fmt.Errorf("generate runtime tls certificate failed: %w", err)
	}

	server := &Server{
		cfg:   cfg,
		store: state,
		proxy: &masque.Proxy{},
	}
	// ECDH 模式下预生成服务端密钥对，公钥通过 CONNECT-UDP 响应头下发给客户端。
	if cfg.MasqueAuthMode == masqueAuthModeECDH {
		curve := ecdh.X25519()
		privateKey, err := curve.GenerateKey(cryptorand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ecdh private key failed: %w", err)
		}
		server.ecdhPrivate = privateKey
		server.ecdhPublicB64 = base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/masque/udp/", server.handleProxyConnectUDP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server.h3Server = &http3.Server{
		Addr:            cfg.MasqueAddr,
		Handler:         mux,
		EnableDatagrams: true,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			NextProtos:   []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{
			EnableDatagrams: true,
		},
	}
	return server, nil
}

func (s *Server) Run(ctx context.Context) error {
	// MASQUE CONNECT-UDP 会把 datagram 转发到本地 UDP 端口，这里负责接收并处理业务事件。
	udpAddr, err := net.ResolveUDPAddr("udp", s.cfg.MasqueTunnelUDPAddr)
	if err != nil {
		return fmt.Errorf("resolve masque tunnel udp addr failed: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen masque tunnel udp addr failed: %w", err)
	}
	s.udpConn = udpConn

	errCh := make(chan error, 2)
	go func() {
		if err := s.serveEventDatagrams(ctx); err != nil {
			errCh <- err
		}
	}()
	go func() {
		// H3 服务专门承载 CONNECT-UDP 请求。
		if err := s.h3Server.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return s.Close()
	case err := <-errCh:
		_ = s.Close()
		if isClosedNetworkError(err) || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.h3Server != nil {
			if err := s.h3Server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				closeErr = err
			}
		}
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
		if s.proxy != nil {
			_ = s.proxy.Close()
		}
	})
	return closeErr
}

func (s *Server) handleProxyConnectUDP(w http.ResponseWriter, r *http.Request) {
	// 依据请求 host 构造模板，确保 ParseRequest 能正确解析目标地址。
	template := uritemplate.MustNew(fmt.Sprintf("https://%s/masque/udp/{target_host}/{target_port}", r.Host))
	request, err := masque.ParseRequest(r, template)
	if err != nil {
		var parseErr *masque.RequestParseError
		if errors.As(err, &parseErr) {
			w.WriteHeader(parseErr.HTTPStatus)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// 仅允许代理到 bridge 内部 tunnel UDP 处理端口，避免被滥用成开放 UDP 代理。
	if strings.TrimSpace(request.Target) != strings.TrimSpace(s.cfg.MasqueTunnelUDPAddr) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// ECDH 模式将服务端公钥放入响应头，供客户端派生共享密钥。
	// 同时始终返回服务端认证模式，便于客户端在模式不一致时给出明确报错。
	w.Header().Set(masqueAuthModeHeader, strings.TrimSpace(s.cfg.MasqueAuthMode))
	if s.cfg.MasqueAuthMode == masqueAuthModeECDH {
		w.Header().Set(masqueServerPubHeader, s.ecdhPublicB64)
	}

	slog.Info("masque tunnel link connected",
		"remoteAddr", strings.TrimSpace(r.RemoteAddr),
		"target", strings.TrimSpace(request.Target),
		"authMode", strings.TrimSpace(s.cfg.MasqueAuthMode),
	)
	if err := s.proxy.Proxy(w, request); err != nil {
		slog.Warn("masque tunnel link disconnected with error",
			"remoteAddr", strings.TrimSpace(r.RemoteAddr),
			"target", strings.TrimSpace(request.Target),
			"error", err,
		)
		s.store.AddError(domain.SyncErrorInvalidPayload, "masque proxy failed", map[string]string{
			"error": err.Error(),
		})
		return
	}
	slog.Info("masque tunnel link disconnected",
		"remoteAddr", strings.TrimSpace(r.RemoteAddr),
		"target", strings.TrimSpace(request.Target),
	)
}

func (s *Server) serveEventDatagrams(ctx context.Context) error {
	buffer := make([]byte, 64*1024)

	for {
		// 通过短读超时轮询上下文，避免阻塞导致无法及时退出。
		_ = s.udpConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := s.udpConn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return nil
				default:
					continue
				}
			}
			if isClosedNetworkError(err) {
				return nil
			}
			return fmt.Errorf("read masque datagram failed: %w", err)
		}

		response := s.handleDatagram(buffer[:n])
		encoded, err := json.Marshal(response)
		if err != nil {
			continue
		}
		// 响应直接回写给来源地址，形成 request/response 风格的 datagram 交互。
		if _, err := s.udpConn.WriteToUDP(encoded, addr); err != nil && !isClosedNetworkError(err) {
			return fmt.Errorf("write masque datagram failed: %w", err)
		}
	}
}

func (s *Server) handleDatagram(data []byte) datagramResponse {
	var request datagramRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return datagramResponse{
			Error: fmt.Sprintf("decode datagram request failed: %v", err),
		}
	}

	if err := s.verifyAuth(request.Auth); err != nil {
		return datagramResponse{
			Error: fmt.Sprintf("auth failed: %v", err),
		}
	}

	// 复用现有 tunnel 事件处理链，避免复制业务逻辑。
	reply, err := s.store.ProcessTunnelEvent(request.Event)
	if err == nil {
		return datagramResponse{Reply: reply}
	}

	var reject *store.EventRejectError
	if errors.As(err, &reject) {
		// 语义性拒绝（如 stale epoch）回包给 agent，保持与 HTTP 模式一致的错误语义。
		return datagramResponse{
			Reply: domain.TunnelEventReply{
				Type:            domain.TunnelMessageError,
				Status:          domain.EventStatusRejected,
				EventID:         request.Event.EventID,
				SessionEpoch:    selectSessionEpoch(request.Event.SessionEpoch, reject.SessionEpoch),
				ResourceVersion: reject.ResourceVersion,
				Deduplicated:    false,
				ErrorCode:       reject.Code,
				Message:         reject.Message,
			},
		}
	}

	return datagramResponse{
		Error: fmt.Sprintf("process tunnel event failed: %v", err),
	}
}

func (s *Server) verifyAuth(auth datagramAuth) error {
	// 允许历史客户端省略 mode 字段时回落到 psk，提升兼容性。
	mode := strings.ToLower(strings.TrimSpace(auth.Mode))
	if mode == "" {
		mode = masqueAuthModePSK
	}
	if mode != s.cfg.MasqueAuthMode {
		return fmt.Errorf("unexpected auth mode: %s", mode)
	}

	switch mode {
	case masqueAuthModeECDH:
		return s.verifyECDHAuth(auth)
	default:
		// PSK 使用常量时间比较，减少时序侧信道。
		incoming := []byte(strings.TrimSpace(auth.PSK))
		expected := []byte(strings.TrimSpace(s.cfg.MasquePSK))
		if len(expected) == 0 {
			expected = []byte("devloop-masque-default-psk")
		}
		if subtle.ConstantTimeCompare(incoming, expected) != 1 {
			return errors.New("invalid psk")
		}
		return nil
	}
}

func (s *Server) verifyECDHAuth(auth datagramAuth) error {
	if s.ecdhPrivate == nil {
		return errors.New("server ecdh key is not ready")
	}

	clientPubEncoded := strings.TrimSpace(auth.ClientPub)
	proof := strings.TrimSpace(auth.Proof)
	if clientPubEncoded == "" || proof == "" {
		return errors.New("clientPub/proof is required")
	}

	clientPubRaw, err := base64.StdEncoding.DecodeString(clientPubEncoded)
	if err != nil {
		return fmt.Errorf("decode client public key failed: %w", err)
	}
	curve := ecdh.X25519()
	clientPub, err := curve.NewPublicKey(clientPubRaw)
	if err != nil {
		return fmt.Errorf("parse client public key failed: %w", err)
	}

	shared, err := s.ecdhPrivate.ECDH(clientPub)
	if err != nil {
		return fmt.Errorf("derive shared key failed: %w", err)
	}
	// 通过对共享密钥计算 HMAC proof 来证明客户端确实持有对应私钥。
	expectedProof := buildECDHProof(shared)
	if subtle.ConstantTimeCompare([]byte(proof), []byte(expectedProof)) != 1 {
		return errors.New("invalid ecdh proof")
	}
	return nil
}

func buildECDHProof(shared []byte) string {
	mac := hmac.New(sha256.New, shared)
	_, _ = mac.Write([]byte(masqueProofContext))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func selectSessionEpoch(incoming, fallback int64) int64 {
	if fallback > 0 {
		return fallback
	}
	return incoming
}

func generateRuntimeCertificate() (tls.Certificate, error) {
	// 运行时证书仅用于本地进程间加密，不依赖外部 CA。
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate ecdsa private key failed: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := cryptorand.Int(cryptorand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial number failed: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		NotBefore:    time.Now().Add(-1 * time.Minute).UTC(),
		NotAfter:     time.Now().Add(24 * time.Hour).UTC(),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
		},
	}

	der, err := x509.CreateCertificate(cryptorand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create x509 certificate failed: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal ecdsa private key failed: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load x509 key pair failed: %w", err)
	}
	return certificate, nil
}

func isClosedNetworkError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}
