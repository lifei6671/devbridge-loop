package tls

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// defaultManagedCARootCommonName 定义自建 CA 未显式配置时使用的根证书 CN。
	defaultManagedCARootCommonName = "devbridge-managed-root-ca"
	// defaultManagedCAServerCommonName 定义服务端证书在无可用 SAN 时使用的兜底 CN。
	defaultManagedCAServerCommonName = "devbridge-bridge-control-plane"
	// managedCARootCertificateTTL 定义本地自建 Root CA 的长期有效期。
	managedCARootCertificateTTL = 10 * 365 * 24 * time.Hour
	// managedCAServerClockSkewLeeway 给证书生效时间预留时钟偏移缓冲，减少边界抖动。
	managedCAServerClockSkewLeeway = 5 * time.Minute
)

// controlPlaneTLSCertSource 定义控制面证书来源模式。
type controlPlaneTLSCertSource string

const (
	controlPlaneTLSCertSourceExternal  controlPlaneTLSCertSource = "external"
	controlPlaneTLSCertSourceManagedCA controlPlaneTLSCertSource = "managed_ca"
)

// normalizeControlPlaneTLSCertSource 归一化并校验证书来源模式。
func normalizeControlPlaneTLSCertSource(rawSource string) (controlPlaneTLSCertSource, error) {
	switch strings.ToLower(strings.TrimSpace(rawSource)) {
	case "", string(controlPlaneTLSCertSourceExternal):
		return controlPlaneTLSCertSourceExternal, nil
	case string(controlPlaneTLSCertSourceManagedCA):
		return controlPlaneTLSCertSourceManagedCA, nil
	default:
		return "", fmt.Errorf("unsupported control_plane.tls_cert_source=%s", rawSource)
	}
}

// controlPlaneTLSCertificateProvider 抽象控制面证书加载来源，便于 external/managed_ca/第三方实现平滑替换。
type controlPlaneTLSCertificateProvider interface {
	// LoadServerTLSConfig 返回控制面服务端 TLS 配置。
	LoadServerTLSConfig(ctx context.Context) (*tls.Config, error)
}

// controlPlaneTLSCertificateProviderOptions 提供证书 provider 的可选依赖。
type controlPlaneTLSCertificateProviderOptions struct {
	// managedCAIssuer 允许注入第三方 CA 签发器；为空时使用本地自建 CA。
	managedCAIssuer managedCACertificateIssuer
}

// controlPlaneTLSCertificateProviderConfig 定义证书 provider 所需配置字段。
type controlPlaneTLSCertificateProviderConfig struct {
	TLSCertSource            string
	TLSCertFile              string
	TLSKeyFile               string
	TLSCACertFile            string
	TLSCAKeyFile             string
	TLSServerCommonName      string
	TLSServerSANDNS          []string
	TLSServerSANIPs          []string
	TLSServerCertTTL         time.Duration
	TLSServerCertRenewBefore time.Duration
}

// newControlPlaneTLSCertificateProvider 根据配置创建证书加载 provider。
func newControlPlaneTLSCertificateProvider(
	config controlPlaneTLSCertificateProviderConfig,
	options controlPlaneTLSCertificateProviderOptions,
) (controlPlaneTLSCertificateProvider, error) {
	normalizedCertSource, err := normalizeControlPlaneTLSCertSource(config.TLSCertSource)
	if err != nil {
		return nil, err
	}
	switch normalizedCertSource {
	case controlPlaneTLSCertSourceExternal:
		return &externalControlPlaneCertificateProvider{
			certFile: config.TLSCertFile,
			keyFile:  config.TLSKeyFile,
		}, nil
	case controlPlaneTLSCertSourceManagedCA:
		managedCAIssuer := options.managedCAIssuer
		if managedCAIssuer == nil {
			managedCAIssuer = newLocalManagedCACertificateIssuer()
		}
		sanIPs, parseErr := parseControlPlaneSANIPs(config.TLSServerSANIPs)
		if parseErr != nil {
			return nil, parseErr
		}
		return &managedCAControlPlaneCertificateProvider{
			issuer: managedCAIssuer,
			request: managedCAServerCertificateRequest{
				CACertFile:            config.TLSCACertFile,
				CAKeyFile:             config.TLSCAKeyFile,
				ServerCommonName:      config.TLSServerCommonName,
				ServerSANDNS:          normalizeNonEmptyStringSlice(config.TLSServerSANDNS),
				ServerSANIPs:          sanIPs,
				ServerCertTTL:         config.TLSServerCertTTL,
				ServerCertRenewBefore: config.TLSServerCertRenewBefore,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported control_plane.tls_cert_source=%s", normalizedCertSource)
	}
}

// externalControlPlaneCertificateProvider 使用外部静态 cert/key 文件提供服务端证书。
type externalControlPlaneCertificateProvider struct {
	certFile string
	keyFile  string
}

// LoadServerTLSConfig 从本地 cert/key 文件加载服务端 TLS 配置。
func (provider *externalControlPlaneCertificateProvider) LoadServerTLSConfig(_ context.Context) (*tls.Config, error) {
	return loadControlPlaneServerTLSConfig(provider.certFile, provider.keyFile)
}

// managedCACertificateIssuer 抽象 managed_ca 模式的证书签发能力，便于后续替换为第三方 CA 服务。
type managedCACertificateIssuer interface {
	// IssueServerCertificate 签发 Bridge 控制面服务端证书并返回可直接用于 TLS 的证书链。
	IssueServerCertificate(ctx context.Context, request managedCAServerCertificateRequest) (tls.Certificate, error)
}

// managedCAServerCertificateRequest 定义 managed_ca 模式签发服务端证书所需参数。
type managedCAServerCertificateRequest struct {
	CACertFile            string
	CAKeyFile             string
	ServerCommonName      string
	ServerSANDNS          []string
	ServerSANIPs          []net.IP
	ServerCertTTL         time.Duration
	ServerCertRenewBefore time.Duration
}

// managedCAControlPlaneCertificateProvider 基于 CA 签发器动态获取 Bridge 服务端证书。
type managedCAControlPlaneCertificateProvider struct {
	issuer  managedCACertificateIssuer
	request managedCAServerCertificateRequest
	mutex   sync.Mutex
	// cachedServerTLSConfig 缓存最近一次可用的服务端 TLS 配置，避免每次握手都触发重签发。
	cachedServerTLSConfig *tls.Config
	// cachedServerCertNotAfter 保存当前缓存证书到期时间，用于判断是否进入 renew_before 窗口。
	cachedServerCertNotAfter time.Time
}

// LoadServerTLSConfig 调用 CA 签发器获取服务端证书并构造 TLS 配置。
func (provider *managedCAControlPlaneCertificateProvider) LoadServerTLSConfig(ctx context.Context) (*tls.Config, error) {
	if provider == nil || provider.issuer == nil {
		return nil, errors.New("load control plane tls config: managed ca issuer is nil")
	}
	now := time.Now().UTC()
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	if provider.cachedServerTLSConfig != nil && !shouldRenewManagedServerCertificate(
		now,
		provider.cachedServerCertNotAfter,
		provider.request.ServerCertRenewBefore,
	) {
		// 命中缓存时直接复用，避免不必要的证书签发和磁盘写入。
		return provider.cachedServerTLSConfig.Clone(), nil
	}
	certificate, err := provider.issuer.IssueServerCertificate(ctx, provider.request)
	if err != nil {
		// 已有未过期缓存时优先保证服务连续性；过期缓存不得继续返回。
		if provider.cachedServerTLSConfig != nil && provider.cachedServerCertNotAfter.After(now) {
			return provider.cachedServerTLSConfig.Clone(), nil
		}
		if provider.cachedServerTLSConfig != nil {
			return nil, fmt.Errorf(
				"load control plane tls certificate by managed ca: cached certificate expired at %s and refresh failed: %w",
				provider.cachedServerCertNotAfter.Format(time.RFC3339),
				err,
			)
		}
		return nil, fmt.Errorf("load control plane tls certificate by managed ca: %w", err)
	}
	serverTLSConfig := buildControlPlaneServerTLSConfigFromCertificate(certificate)
	serverCertNotAfter, resolveErr := resolveTLSCertificateNotAfter(certificate)
	if resolveErr != nil {
		return nil, fmt.Errorf("load control plane tls config: resolve certificate not_after failed: %w", resolveErr)
	}
	provider.cachedServerTLSConfig = serverTLSConfig
	provider.cachedServerCertNotAfter = serverCertNotAfter
	return serverTLSConfig.Clone(), nil
}

// localManagedCACertificateIssuer 提供“本地文件 Root CA + 本地签发服务端证书”的默认实现。
type localManagedCACertificateIssuer struct{}

// newLocalManagedCACertificateIssuer 创建本地自建 CA 签发器。
func newLocalManagedCACertificateIssuer() managedCACertificateIssuer {
	return &localManagedCACertificateIssuer{}
}

// IssueServerCertificate 在本地加载/初始化 Root CA，并签发 Bridge 服务端证书。
func (issuer *localManagedCACertificateIssuer) IssueServerCertificate(
	ctx context.Context,
	request managedCAServerCertificateRequest,
) (tls.Certificate, error) {
	if issuer == nil {
		return tls.Certificate{}, errors.New("issue server certificate: local issuer is nil")
	}
	select {
	case <-ctx.Done():
		return tls.Certificate{}, ctx.Err()
	default:
	}
	rootMaterial, err := loadOrInitializeManagedCARootCertificate(request.CACertFile, request.CAKeyFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	normalizedSANDNS := normalizeNonEmptyStringSlice(request.ServerSANDNS)
	normalizedSANIPs := cloneNonNilIPs(request.ServerSANIPs)
	serverCommonName := strings.TrimSpace(request.ServerCommonName)
	if serverCommonName == "" {
		// 没有显式 CN 时优先使用第一个 SAN，保持证书主体可读。
		switch {
		case len(normalizedSANDNS) > 0:
			serverCommonName = normalizedSANDNS[0]
		case len(normalizedSANIPs) > 0:
			serverCommonName = normalizedSANIPs[0].String()
		default:
			serverCommonName = defaultManagedCAServerCommonName
		}
	}
	serverPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: generate private key failed: %w", err)
	}
	serialNumber, err := randomCertificateSerialNumber()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: generate serial number failed: %w", err)
	}
	now := time.Now().UTC()
	serverCertTTL := request.ServerCertTTL
	if serverCertTTL <= 0 {
		serverCertTTL = 168 * time.Hour
	}
	// renew_before 当前用于配置约束与后续热续签扩展，这里先消费字段避免行为分叉。
	if request.ServerCertRenewBefore < 0 {
		return tls.Certificate{}, fmt.Errorf(
			"issue server certificate: renew_before=%s must be greater than or equal to 0",
			request.ServerCertRenewBefore,
		)
	}
	if request.ServerCertRenewBefore >= serverCertTTL {
		return tls.Certificate{}, fmt.Errorf(
			"issue server certificate: renew_before=%s must be less than cert_ttl=%s",
			request.ServerCertRenewBefore,
			serverCertTTL,
		)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: serverCommonName,
		},
		// 预留轻微回拨窗口，降低集群时钟偏差导致的“刚签发即不可用”概率。
		NotBefore:             now.Add(-managedCAServerClockSkewLeeway),
		NotAfter:              now.Add(serverCertTTL),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              normalizedSANDNS,
		IPAddresses:           normalizedSANIPs,
	}
	serverCertDER, err := x509.CreateCertificate(
		rand.Reader,
		serverTemplate,
		rootMaterial.certificate,
		serverPrivateKey.Public(),
		rootMaterial.privateKey,
	)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: sign certificate failed: %w", err)
	}
	serverCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertDER,
	})
	serverPrivateKeyPKCS8, err := x509.MarshalPKCS8PrivateKey(serverPrivateKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: marshal private key failed: %w", err)
	}
	serverPrivateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: serverPrivateKeyPKCS8,
	})
	// 证书链按“leaf -> root”顺序拼接，便于客户端在缺失中间链时仍可完整校验。
	serverCertChainPEM := append(append([]byte(nil), serverCertPEM...), rootMaterial.certificatePEM...)
	issuedCertificate, err := tls.X509KeyPair(serverCertChainPEM, serverPrivateKeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: load key pair failed: %w", err)
	}
	issuedCertificateLeaf, err := x509.ParseCertificate(serverCertDER)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue server certificate: parse leaf certificate failed: %w", err)
	}
	issuedCertificate.Leaf = issuedCertificateLeaf
	return issuedCertificate, nil
}

// managedCARootMaterial 保存 Root CA 证书和私钥材料，供签发服务端证书复用。
type managedCARootMaterial struct {
	certificate    *x509.Certificate
	privateKey     crypto.Signer
	certificatePEM []byte
}

// loadOrInitializeManagedCARootCertificate 从磁盘加载 Root CA，缺失时自动初始化新 Root CA。
func loadOrInitializeManagedCARootCertificate(
	caCertFile string,
	caKeyFile string,
) (*managedCARootMaterial, error) {
	normalizedCACertFile := strings.TrimSpace(caCertFile)
	normalizedCAKeyFile := strings.TrimSpace(caKeyFile)
	if normalizedCACertFile == "" {
		return nil, errors.New("load managed ca root certificate: empty ca cert file")
	}
	if normalizedCAKeyFile == "" {
		return nil, errors.New("load managed ca root certificate: empty ca key file")
	}
	caCertPEM, certReadErr := os.ReadFile(normalizedCACertFile)
	caKeyPEM, keyReadErr := os.ReadFile(normalizedCAKeyFile)
	certExists := certReadErr == nil
	keyExists := keyReadErr == nil
	if certExists && keyExists {
		if permissionErr := validatePrivateKeyFilePermission(normalizedCAKeyFile); permissionErr != nil {
			return nil, fmt.Errorf("load managed ca root certificate: %w", permissionErr)
		}
		return parseManagedCARootMaterial(caCertPEM, caKeyPEM)
	}
	if certExists != keyExists {
		return nil, fmt.Errorf(
			"load managed ca root certificate: cert/key file mismatch cert_exists=%t key_exists=%t",
			certExists,
			keyExists,
		)
	}
	if errors.Is(certReadErr, os.ErrNotExist) && errors.Is(keyReadErr, os.ErrNotExist) {
		return initializeManagedCARootCertificate(normalizedCACertFile, normalizedCAKeyFile)
	}
	if certReadErr != nil {
		return nil, fmt.Errorf("load managed ca root certificate: read cert file failed: %w", certReadErr)
	}
	if keyReadErr != nil {
		return nil, fmt.Errorf("load managed ca root certificate: read key file failed: %w", keyReadErr)
	}
	return nil, errors.New("load managed ca root certificate: unexpected root ca state")
}

// parseManagedCARootMaterial 解析 Root CA PEM 并校验证书和私钥匹配关系。
func parseManagedCARootMaterial(certificatePEM []byte, privateKeyPEM []byte) (*managedCARootMaterial, error) {
	certificate, err := parseSingleCertificatePEM(certificatePEM)
	if err != nil {
		return nil, fmt.Errorf("parse managed ca root material: parse certificate failed: %w", err)
	}
	if !certificate.IsCA {
		return nil, errors.New("parse managed ca root material: certificate is not a ca")
	}
	privateKey, err := parsePEMPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse managed ca root material: parse private key failed: %w", err)
	}
	publicKeyInCertificate, marshalCertPublicKeyErr := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if marshalCertPublicKeyErr != nil {
		return nil, fmt.Errorf("parse managed ca root material: marshal cert public key failed: %w", marshalCertPublicKeyErr)
	}
	publicKeyInPrivateKey, marshalPrivatePublicKeyErr := x509.MarshalPKIXPublicKey(privateKey.Public())
	if marshalPrivatePublicKeyErr != nil {
		return nil, fmt.Errorf(
			"parse managed ca root material: marshal private key public key failed: %w",
			marshalPrivatePublicKeyErr,
		)
	}
	if !bytes.Equal(publicKeyInCertificate, publicKeyInPrivateKey) {
		return nil, errors.New("parse managed ca root material: certificate and private key mismatch")
	}
	return &managedCARootMaterial{
		certificate:    certificate,
		privateKey:     privateKey,
		certificatePEM: append([]byte(nil), certificatePEM...),
	}, nil
}

// initializeManagedCARootCertificate 生成新的 Root CA 并写入指定文件路径。
func initializeManagedCARootCertificate(caCertFile string, caKeyFile string) (*managedCARootMaterial, error) {
	rootPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: generate private key failed: %w", err)
	}
	serialNumber, err := randomCertificateSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: generate serial number failed: %w", err)
	}
	now := time.Now().UTC()
	rootTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: defaultManagedCARootCommonName,
		},
		NotBefore:             now.Add(-managedCAServerClockSkewLeeway),
		NotAfter:              now.Add(managedCARootCertificateTTL),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootCertDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPrivateKey.Public(), rootPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: create certificate failed: %w", err)
	}
	rootCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: rootCertDER,
	})
	rootPrivateKeyPKCS8, err := x509.MarshalPKCS8PrivateKey(rootPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: marshal private key failed: %w", err)
	}
	rootPrivateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: rootPrivateKeyPKCS8,
	})
	if err := ensureParentDirectory(caCertFile); err != nil {
		return nil, err
	}
	if err := ensureParentDirectory(caKeyFile); err != nil {
		return nil, err
	}
	if err := os.WriteFile(caCertFile, rootCertPEM, 0o644); err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: write cert file failed: %w", err)
	}
	if err := os.WriteFile(caKeyFile, rootPrivateKeyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: write key file failed: %w", err)
	}
	parsedRootCertificate, err := x509.ParseCertificate(rootCertDER)
	if err != nil {
		return nil, fmt.Errorf("initialize managed ca root certificate: parse root cert der failed: %w", err)
	}
	return &managedCARootMaterial{
		certificate:    parsedRootCertificate,
		privateKey:     rootPrivateKey,
		certificatePEM: rootCertPEM,
	}, nil
}

// parseSingleCertificatePEM 从 PEM 数据中解析第一张证书。
func parseSingleCertificatePEM(certificatePEM []byte) (*x509.Certificate, error) {
	pemBlock, _ := pem.Decode(certificatePEM)
	if pemBlock == nil || len(pemBlock.Bytes) == 0 {
		return nil, errors.New("parse certificate pem: empty pem block")
	}
	certificate, err := x509.ParseCertificate(pemBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return certificate, nil
}

// parsePEMPrivateKey 从 PEM 中解析私钥并转成 crypto.Signer。
func parsePEMPrivateKey(privateKeyPEM []byte) (crypto.Signer, error) {
	pemBlock, _ := pem.Decode(privateKeyPEM)
	if pemBlock == nil || len(pemBlock.Bytes) == 0 {
		return nil, errors.New("parse pem private key: empty pem block")
	}
	if parsedPrivateKey, err := x509.ParsePKCS8PrivateKey(pemBlock.Bytes); err == nil {
		privateKeySigner, ok := parsedPrivateKey.(crypto.Signer)
		if !ok {
			return nil, errors.New("parse pem private key: pkcs8 private key does not implement signer")
		}
		return privateKeySigner, nil
	}
	if parsedPrivateKey, err := x509.ParseECPrivateKey(pemBlock.Bytes); err == nil {
		return parsedPrivateKey, nil
	}
	if parsedPrivateKey, err := x509.ParsePKCS1PrivateKey(pemBlock.Bytes); err == nil {
		return parsedPrivateKey, nil
	}
	return nil, errors.New("parse pem private key: unsupported private key format")
}

// parseControlPlaneSANIPs 把配置中的 SAN IP 文本解析为 net.IP 列表。
func parseControlPlaneSANIPs(rawSANIPs []string) ([]net.IP, error) {
	normalizedSANIPTexts := normalizeNonEmptyStringSlice(rawSANIPs)
	if len(normalizedSANIPTexts) == 0 {
		return nil, nil
	}
	sanIPs := make([]net.IP, 0, len(normalizedSANIPTexts))
	for _, sanIPText := range normalizedSANIPTexts {
		parsedSANIP := net.ParseIP(sanIPText)
		if parsedSANIP == nil {
			return nil, fmt.Errorf("parse control plane san ips: invalid ip=%s", sanIPText)
		}
		sanIPs = append(sanIPs, parsedSANIP)
	}
	return sanIPs, nil
}

// cloneNonNilIPs 深拷贝 IP 列表，避免调用方后续修改影响签发模板。
func cloneNonNilIPs(rawIPs []net.IP) []net.IP {
	if len(rawIPs) == 0 {
		return nil
	}
	cloned := make([]net.IP, 0, len(rawIPs))
	for _, rawIP := range rawIPs {
		if rawIP == nil {
			continue
		}
		cloned = append(cloned, append(net.IP(nil), rawIP...))
	}
	return cloned
}

// shouldRenewManagedServerCertificate 判断 managed_ca 证书是否进入 renew_before 续签窗口。
func shouldRenewManagedServerCertificate(
	now time.Time,
	certificateNotAfter time.Time,
	renewBefore time.Duration,
) bool {
	if certificateNotAfter.IsZero() {
		return true
	}
	if renewBefore <= 0 {
		return !certificateNotAfter.After(now)
	}
	return !certificateNotAfter.Add(-renewBefore).After(now)
}

// resolveTLSCertificateNotAfter 从 tls.Certificate 中提取 leaf 证书到期时间。
func resolveTLSCertificateNotAfter(certificate tls.Certificate) (time.Time, error) {
	if certificate.Leaf != nil {
		return certificate.Leaf.NotAfter.UTC(), nil
	}
	if len(certificate.Certificate) == 0 {
		return time.Time{}, errors.New("resolve tls certificate not_after: empty certificate chain")
	}
	parsedLeafCertificate, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return parsedLeafCertificate.NotAfter.UTC(), nil
}

// randomCertificateSerialNumber 生成证书序列号，满足 RFC 推荐的足够随机空间。
func randomCertificateSerialNumber() (*big.Int, error) {
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, err
	}
	return serialNumber, nil
}

// validatePrivateKeyFilePermission 校验私钥文件权限，避免 group/other 读写执行暴露密钥。
func validatePrivateKeyFilePermission(filePath string) error {
	normalizedFilePath := strings.TrimSpace(filePath)
	if normalizedFilePath == "" {
		return errors.New("validate private key file permission: empty file path")
	}
	// Windows ACL 语义与 Unix 位掩码不同，这里仅在类 Unix 平台执行权限位约束。
	if runtime.GOOS == "windows" {
		return nil
	}
	fileStat, err := os.Stat(normalizedFilePath)
	if err != nil {
		return fmt.Errorf("validate private key file permission: stat file failed: %w", err)
	}
	fileMode := fileStat.Mode().Perm()
	if fileMode&0o077 != 0 {
		return fmt.Errorf(
			"validate private key file permission: insecure permission %o on file=%s (require <= 600)",
			fileMode,
			normalizedFilePath,
		)
	}
	return nil
}

// ensureParentDirectory 确保目标文件父目录存在。
func ensureParentDirectory(filePath string) error {
	normalizedFilePath := strings.TrimSpace(filePath)
	if normalizedFilePath == "" {
		return errors.New("ensure parent directory: empty file path")
	}
	parentDirectory := filepath.Dir(normalizedFilePath)
	if parentDirectory == "." || parentDirectory == "" {
		return nil
	}
	if err := os.MkdirAll(parentDirectory, 0o755); err != nil {
		return fmt.Errorf("ensure parent directory: mkdir failed: %w", err)
	}
	return nil
}
