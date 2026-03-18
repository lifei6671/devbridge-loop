package tls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
)

// TestNormalizeControlPlaneTLSMode 验证控制面 TLS 模式归一化结果。
func TestNormalizeControlPlaneTLSMode(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		rawMode  string
		wantMode controlPlaneTLSMode
		wantErr  bool
	}{
		{rawMode: "", wantMode: controlPlaneTLSModePlaintext},
		{rawMode: "plaintext", wantMode: controlPlaneTLSModePlaintext},
		{rawMode: "required", wantMode: controlPlaneTLSModeRequired},
		{rawMode: "optional", wantMode: controlPlaneTLSModeOptional},
		{rawMode: "invalid", wantErr: true},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.rawMode, func(testingObject *testing.T) {
			testingObject.Parallel()
			gotMode, err := normalizeControlPlaneTLSMode(testCase.rawMode)
			if testCase.wantErr {
				if err == nil {
					testingObject.Fatalf("expected normalize tls mode error")
				}
				return
			}
			if err != nil {
				testingObject.Fatalf("normalize tls mode failed: %v", err)
			}
			if gotMode != testCase.wantMode {
				testingObject.Fatalf("unexpected tls mode: got=%s want=%s", gotMode, testCase.wantMode)
			}
		})
	}
}

// TestNormalizeControlPlaneTLSCertSource 验证控制面证书来源模式归一化结果。
func TestNormalizeControlPlaneTLSCertSource(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		rawSource  string
		wantSource controlPlaneTLSCertSource
		wantErr    bool
	}{
		{rawSource: "", wantSource: controlPlaneTLSCertSourceExternal},
		{rawSource: "external", wantSource: controlPlaneTLSCertSourceExternal},
		{rawSource: "managed_ca", wantSource: controlPlaneTLSCertSourceManagedCA},
		{rawSource: "invalid", wantErr: true},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.rawSource, func(testingObject *testing.T) {
			testingObject.Parallel()
			gotSource, err := normalizeControlPlaneTLSCertSource(testCase.rawSource)
			if testCase.wantErr {
				if err == nil {
					testingObject.Fatalf("expected normalize tls cert source error")
				}
				return
			}
			if err != nil {
				testingObject.Fatalf("normalize tls cert source failed: %v", err)
			}
			if gotSource != testCase.wantSource {
				testingObject.Fatalf("unexpected tls cert source: got=%s want=%s", gotSource, testCase.wantSource)
			}
		})
	}
}

// TestLooksLikeTLSClientHello 验证 TLS ClientHello 首包可被识别。
func TestLooksLikeTLSClientHello(testingObject *testing.T) {
	testingObject.Parallel()

	if !looksLikeTLSClientHello([]byte{0x16, 0x03, 0x03}) {
		testingObject.Fatalf("expected tls client hello prefix matched")
	}
	if looksLikeTLSClientHello([]byte("PRI")) {
		testingObject.Fatalf("expected h2c preface not treated as tls client hello")
	}
}

// TestLoadControlPlaneServerTLSConfigDisablesSessionResumption 验证服务端 TLS 配置会显式关闭恢复路径。
func TestLoadControlPlaneServerTLSConfigDisablesSessionResumption(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	certFile, keyFile := writeControlPlaneTLSKeyPair(testingObject, tempDir)

	tlsConfig, err := loadControlPlaneServerTLSConfig(certFile, keyFile)
	if err != nil {
		testingObject.Fatalf("load control plane tls config failed: %v", err)
	}
	if tlsConfig == nil {
		testingObject.Fatalf("expected non-nil tls config")
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 || tlsConfig.MaxVersion != tls.VersionTLS13 {
		testingObject.Fatalf(
			"unexpected tls version range: min=%d max=%d want=%d",
			tlsConfig.MinVersion,
			tlsConfig.MaxVersion,
			tls.VersionTLS13,
		)
	}
	if !tlsConfig.SessionTicketsDisabled {
		testingObject.Fatalf("expected session tickets disabled for early-data hardening")
	}
	if len(tlsConfig.Certificates) != 1 {
		testingObject.Fatalf("expected one certificate loaded, got=%d", len(tlsConfig.Certificates))
	}
}

// TestManagedCAControlPlaneCertificateProviderLoadsTLSConfig 验证 managed_ca provider 可完成 Root CA 初始化与服务端签发。
func TestManagedCAControlPlaneCertificateProviderLoadsTLSConfig(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	caCertFile := filepath.Join(tempDir, "managed-root-ca.crt")
	caKeyFile := filepath.Join(tempDir, "managed-root-ca.key")
	provider, err := newControlPlaneTLSCertificateProvider(
		controlPlaneTLSCertificateProviderConfig{
			TLSCertSource:            string(controlPlaneTLSCertSourceManagedCA),
			TLSCACertFile:            caCertFile,
			TLSCAKeyFile:             caKeyFile,
			TLSServerCommonName:      "bridge.internal.example",
			TLSServerSANDNS:          []string{"bridge.internal.example"},
			TLSServerSANIPs:          []string{"127.0.0.1"},
			TLSServerCertTTL:         72 * time.Hour,
			TLSServerCertRenewBefore: 12 * time.Hour,
		},
		controlPlaneTLSCertificateProviderOptions{},
	)
	if err != nil {
		testingObject.Fatalf("new control plane tls provider failed: %v", err)
	}
	tlsConfig, err := provider.LoadServerTLSConfig(context.Background())
	if err != nil {
		testingObject.Fatalf("load server tls config by managed ca failed: %v", err)
	}
	if tlsConfig == nil {
		testingObject.Fatalf("expected non-nil tls config")
	}
	if len(tlsConfig.Certificates) != 1 {
		testingObject.Fatalf("expected one certificate loaded, got=%d", len(tlsConfig.Certificates))
	}
	if _, err := os.Stat(caCertFile); err != nil {
		testingObject.Fatalf("expected managed ca cert initialized: %v", err)
	}
	if _, err := os.Stat(caKeyFile); err != nil {
		testingObject.Fatalf("expected managed ca key initialized: %v", err)
	}
	leafCertificate := tlsConfig.Certificates[0].Leaf
	if leafCertificate == nil {
		testingObject.Fatalf("expected managed ca issued leaf certificate available")
	}
	if !strings.EqualFold(leafCertificate.Subject.CommonName, "bridge.internal.example") {
		testingObject.Fatalf(
			"unexpected leaf common name: got=%s want=%s",
			leafCertificate.Subject.CommonName,
			"bridge.internal.example",
		)
	}
	if len(leafCertificate.DNSNames) == 0 || leafCertificate.DNSNames[0] != "bridge.internal.example" {
		testingObject.Fatalf("unexpected leaf dns names: %v", leafCertificate.DNSNames)
	}
}

// TestManagedCAProviderCachesCertificateBeforeRenewWindow 验证 managed_ca provider 在未到续签窗口时会复用缓存证书。
func TestManagedCAProviderCachesCertificateBeforeRenewWindow(testingObject *testing.T) {
	testingObject.Parallel()

	fakeIssuer := &stubManagedCACertificateIssuer{
		certTTL: 2 * time.Hour,
	}
	provider := &managedCAControlPlaneCertificateProvider{
		issuer: fakeIssuer,
		request: managedCAServerCertificateRequest{
			ServerCommonName:      "bridge.internal.example",
			ServerSANDNS:          []string{"bridge.internal.example"},
			ServerCertTTL:         2 * time.Hour,
			ServerCertRenewBefore: 30 * time.Minute,
		},
	}
	firstConfig, err := provider.LoadServerTLSConfig(context.Background())
	if err != nil {
		testingObject.Fatalf("first load server tls config failed: %v", err)
	}
	secondConfig, err := provider.LoadServerTLSConfig(context.Background())
	if err != nil {
		testingObject.Fatalf("second load server tls config failed: %v", err)
	}
	if firstConfig == nil || secondConfig == nil {
		testingObject.Fatalf("expected non-nil tls config")
	}
	if fakeIssuer.IssueCount() != 1 {
		testingObject.Fatalf("unexpected managed ca issue count: got=%d want=1", fakeIssuer.IssueCount())
	}
}

// TestManagedCAProviderRenewsCertificateInsideRenewWindow 验证 managed_ca provider 进入续签窗口后会重新签发证书。
func TestManagedCAProviderRenewsCertificateInsideRenewWindow(testingObject *testing.T) {
	testingObject.Parallel()

	fakeIssuer := &stubManagedCACertificateIssuer{
		certTTL: 2 * time.Second,
	}
	provider := &managedCAControlPlaneCertificateProvider{
		issuer: fakeIssuer,
		request: managedCAServerCertificateRequest{
			ServerCommonName:      "bridge.internal.example",
			ServerSANDNS:          []string{"bridge.internal.example"},
			ServerCertTTL:         2 * time.Second,
			ServerCertRenewBefore: 1500 * time.Millisecond,
		},
	}
	if _, err := provider.LoadServerTLSConfig(context.Background()); err != nil {
		testingObject.Fatalf("first load server tls config failed: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := provider.LoadServerTLSConfig(context.Background()); err != nil {
		testingObject.Fatalf("second load server tls config failed: %v", err)
	}
	if fakeIssuer.IssueCount() < 2 {
		testingObject.Fatalf("expected managed ca provider renewed certificate, issue_count=%d", fakeIssuer.IssueCount())
	}
}

// TestManagedCAProviderRejectsExpiredCacheOnIssuerFailure
// 验证缓存证书已过期且续签失败时会返回错误，而不是继续复用过期证书。
func TestManagedCAProviderRejectsExpiredCacheOnIssuerFailure(testingObject *testing.T) {
	testingObject.Parallel()

	fakeIssuer := &stubManagedCACertificateIssuer{
		certTTL:   time.Hour,
		failAfter: 1,
		issueErr:  errors.New("managed ca issuer unavailable"),
	}
	provider := &managedCAControlPlaneCertificateProvider{
		issuer: fakeIssuer,
		request: managedCAServerCertificateRequest{
			ServerCommonName:      "bridge.internal.example",
			ServerSANDNS:          []string{"bridge.internal.example"},
			ServerCertTTL:         time.Hour,
			ServerCertRenewBefore: 10 * time.Minute,
		},
	}
	if _, err := provider.LoadServerTLSConfig(context.Background()); err != nil {
		testingObject.Fatalf("first load server tls config failed: %v", err)
	}
	provider.cachedServerCertNotAfter = time.Now().UTC().Add(-time.Second)

	if _, err := provider.LoadServerTLSConfig(context.Background()); err == nil {
		testingObject.Fatalf("expected expired cache with issuer failure to return error")
	}
}

// TestControlPlaneTLSConfigManagerReloadExternalCertificate 验证 external 模式手工刷新后可加载替换后的证书。
func TestControlPlaneTLSConfigManagerReloadExternalCertificate(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	certFile := filepath.Join(tempDir, "control-plane.crt")
	keyFile := filepath.Join(tempDir, "control-plane.key")
	writeControlPlaneTLSKeyPairWithCommonName(testingObject, certFile, keyFile, "bridge-v1.internal.example")

	manager, err := newControlPlaneTLSConfigManager(
		&externalControlPlaneCertificateProvider{
			certFile: certFile,
			keyFile:  keyFile,
		},
		controlPlaneTLSCertSourceExternal,
	)
	if err != nil {
		testingObject.Fatalf("new tls config manager failed: %v", err)
	}
	if err := manager.Refresh(context.Background()); err != nil {
		testingObject.Fatalf("refresh tls config manager first time failed: %v", err)
	}
	firstCN := manager.CurrentServerTLSConfig().Certificates[0].Leaf.Subject.CommonName
	if firstCN != "bridge-v1.internal.example" {
		testingObject.Fatalf("unexpected first certificate cn: got=%s", firstCN)
	}

	writeControlPlaneTLSKeyPairWithCommonName(testingObject, certFile, keyFile, "bridge-v2.internal.example")
	if err := manager.Refresh(context.Background()); err != nil {
		testingObject.Fatalf("refresh tls config manager second time failed: %v", err)
	}
	secondCN := manager.CurrentServerTLSConfig().Certificates[0].Leaf.Subject.CommonName
	if secondCN != "bridge-v2.internal.example" {
		testingObject.Fatalf("unexpected second certificate cn: got=%s want=%s", secondCN, "bridge-v2.internal.example")
	}
}

// TestControlPlaneTLSConfigManagerNextReloadIntervalManagedCAShortTTL
// 验证 managed_ca 模式在短 TTL 证书下会缩短刷新周期，避免固定 1 分钟轮询带来的过期窗口。
func TestControlPlaneTLSConfigManagerNextReloadIntervalManagedCAShortTTL(testingObject *testing.T) {
	testingObject.Parallel()

	manager := &controlPlaneTLSConfigManager{
		certSource:         controlPlaneTLSCertSourceManagedCA,
		serverCertNotAfter: time.Now().UTC().Add(30 * time.Second),
	}
	reloadInterval := manager.NextReloadInterval()
	if reloadInterval <= 0 {
		testingObject.Fatalf("unexpected reload interval: got=%s", reloadInterval)
	}
	if reloadInterval >= controlPlaneTLSManagedCAReloadInterval {
		testingObject.Fatalf(
			"expected short ttl reload interval less than base managed_ca interval: got=%s base=%s",
			reloadInterval,
			controlPlaneTLSManagedCAReloadInterval,
		)
	}
	if reloadInterval < controlPlaneTLSManagedCAMinReloadInterval {
		testingObject.Fatalf(
			"expected reload interval bounded by minimum: got=%s min=%s",
			reloadInterval,
			controlPlaneTLSManagedCAMinReloadInterval,
		)
	}
}

// TestControlPlaneTLSConfigManagerNextReloadIntervalManagedCAExpiredCert
// 验证 managed_ca 模式证书已过期时，刷新间隔会回落到 retry 周期。
func TestControlPlaneTLSConfigManagerNextReloadIntervalManagedCAExpiredCert(testingObject *testing.T) {
	testingObject.Parallel()

	manager := &controlPlaneTLSConfigManager{
		certSource:         controlPlaneTLSCertSourceManagedCA,
		serverCertNotAfter: time.Now().UTC().Add(-time.Second),
	}
	if reloadInterval := manager.NextReloadInterval(); reloadInterval != controlPlaneTLSReloadRetryInterval {
		testingObject.Fatalf(
			"unexpected reload interval for expired managed_ca cert: got=%s want=%s",
			reloadInterval,
			controlPlaneTLSReloadRetryInterval,
		)
	}
}

// TestLoadControlPlaneServerTLSConfigRejectsInsecureKeyPermission 验证 external 模式私钥文件权限过宽时会被拒绝加载。
func TestLoadControlPlaneServerTLSConfigRejectsInsecureKeyPermission(testingObject *testing.T) {
	testingObject.Parallel()

	if runtime.GOOS == "windows" {
		testingObject.Skip("skip permission mode assertion on windows")
	}
	tempDir := testingObject.TempDir()
	certFile, keyFile := writeControlPlaneTLSKeyPair(testingObject, tempDir)
	if err := os.Chmod(keyFile, 0o644); err != nil {
		testingObject.Fatalf("chmod key file failed: %v", err)
	}
	_, err := loadControlPlaneServerTLSConfig(certFile, keyFile)
	if err == nil {
		testingObject.Fatalf("expected insecure key permission to be rejected")
	}
	if !strings.Contains(err.Error(), "insecure permission") {
		testingObject.Fatalf("unexpected key permission error: %v", err)
	}
}

// TestAcceptControlPlaneConnWithTLSRejectsPlaintextWhenRequired 验证 required 模式会拒绝明文入站。
func TestAcceptControlPlaneConnWithTLSRejectsPlaintextWhenRequired(testingObject *testing.T) {
	testingObject.Parallel()

	serverConn, clientConn := net.Pipe()
	metrics := obs.NewMetrics()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	go func() {
		_, _ = clientConn.Write([]byte("PRI"))
	}()

	_, _, err := acceptControlPlaneConnWithTLS(serverConn, controlPlaneTLSModeRequired, nil, metrics)
	if err == nil {
		testingObject.Fatalf("expected required tls mode reject plaintext connection")
	}
	if !errors.Is(err, errControlPlaneTLSRejected) {
		testingObject.Fatalf("unexpected tls reject error: %v", err)
	}
	if !errors.Is(err, errControlPlaneTLSRejectPlaintextOnRequired) {
		testingObject.Fatalf("unexpected required/plaintext sentinel error: %v", err)
	}
	if metrics.BridgeTLSRejectPlaintextOnRequiredTotal() != 1 {
		testingObject.Fatalf(
			"unexpected plaintext reject metric: got=%d want=1",
			metrics.BridgeTLSRejectPlaintextOnRequiredTotal(),
		)
	}
}

// TestAcceptControlPlaneConnWithTLSRejectsTLSWhenPlaintext 验证 plaintext 模式会拒绝 TLS 首包入站。
func TestAcceptControlPlaneConnWithTLSRejectsTLSWhenPlaintext(testingObject *testing.T) {
	testingObject.Parallel()

	serverConn, clientConn := net.Pipe()
	metrics := obs.NewMetrics()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	go func() {
		_, _ = clientConn.Write([]byte{0x16, 0x03, 0x03})
	}()

	_, _, err := acceptControlPlaneConnWithTLS(serverConn, controlPlaneTLSModePlaintext, nil, metrics)
	if err == nil {
		testingObject.Fatalf("expected plaintext tls mode reject tls connection")
	}
	if !errors.Is(err, errControlPlaneTLSRejected) {
		testingObject.Fatalf("unexpected tls reject error: %v", err)
	}
	if !errors.Is(err, errControlPlaneTLSRejectTLSOnPlaintext) {
		testingObject.Fatalf("unexpected plaintext/tls sentinel error: %v", err)
	}
	if metrics.BridgeTLSRejectTLSOnPlaintextTotal() != 1 {
		testingObject.Fatalf(
			"unexpected tls reject metric: got=%d want=1",
			metrics.BridgeTLSRejectTLSOnPlaintextTotal(),
		)
	}
}

// TestDetectTLSClientHelloTreatsReadTimeoutAsPlaintext 验证无首包超时时按明文待判处理。
func TestDetectTLSClientHelloTreatsReadTimeoutAsPlaintext(testingObject *testing.T) {
	testingObject.Parallel()

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	detectedConn, isTLSClientHello, err := detectTLSClientHello(serverConn, 10*time.Millisecond)
	if err != nil {
		testingObject.Fatalf("detect tls client hello failed: %v", err)
	}
	if detectedConn == nil {
		testingObject.Fatalf("expected detected conn returned")
	}
	if isTLSClientHello {
		testingObject.Fatalf("expected read timeout path not treated as tls client hello")
	}
}

// TestDetectTLSClientHelloAllowsFragmentedTLSHeader 验证分片到达的 TLS 头不会被误判为明文。
func TestDetectTLSClientHelloAllowsFragmentedTLSHeader(testingObject *testing.T) {
	testingObject.Parallel()

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	go func() {
		_, _ = clientConn.Write([]byte{0x16})
		time.Sleep(10 * time.Millisecond)
		_, _ = clientConn.Write([]byte{0x03})
		time.Sleep(10 * time.Millisecond)
		_, _ = clientConn.Write([]byte{0x03})
	}()

	detectedConn, isTLSClientHello, err := detectTLSClientHello(serverConn, 100*time.Millisecond)
	if err != nil {
		testingObject.Fatalf("detect tls client hello failed: %v", err)
	}
	if detectedConn == nil {
		testingObject.Fatalf("expected detected conn returned")
	}
	if !isTLSClientHello {
		testingObject.Fatalf("expected fragmented tls header recognized as tls")
	}

	prefix := make([]byte, 3)
	if _, err := io.ReadFull(detectedConn, prefix); err != nil {
		testingObject.Fatalf("read detected prefix failed: %v", err)
	}
	if string(prefix) != string([]byte{0x16, 0x03, 0x03}) {
		testingObject.Fatalf("unexpected detected prefix: %v", prefix)
	}
}

// writeControlPlaneTLSKeyPair 写入一组测试证书和私钥，供服务端 TLS 加载路径复用。
func writeControlPlaneTLSKeyPair(testingObject *testing.T, directory string) (string, string) {
	testingObject.Helper()

	certFile := filepath.Join(directory, "control-plane.crt")
	keyFile := filepath.Join(directory, "control-plane.key")
	writeControlPlaneTLSKeyPairWithCommonName(testingObject, certFile, keyFile, "bridge.internal.example")
	return certFile, keyFile
}

// writeControlPlaneTLSKeyPairWithCommonName 生成并写入指定 CN 的测试证书与私钥。
func writeControlPlaneTLSKeyPairWithCommonName(
	testingObject *testing.T,
	certFile string,
	keyFile string,
	commonName string,
) {
	testingObject.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		testingObject.Fatalf("generate rsa key failed: %v", err)
	}
	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		testingObject.Fatalf("generate serial number failed: %v", err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             time.Now().UTC().Add(-time.Hour),
		NotAfter:              time.Now().UTC().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{commonName},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		testingObject.Fatalf("create certificate failed: %v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	}), 0o600); err != nil {
		testingObject.Fatalf("write certificate file failed: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}), 0o600); err != nil {
		testingObject.Fatalf("write private key file failed: %v", err)
	}
}

// stubManagedCACertificateIssuer 用于测试 managed_ca provider 缓存与续签行为。
type stubManagedCACertificateIssuer struct {
	certTTL   time.Duration
	mutex     sync.Mutex
	count     int
	failAfter int
	issueErr  error
}

// IssueServerCertificate 按测试参数生成一张短周期证书，并记录签发次数。
func (issuer *stubManagedCACertificateIssuer) IssueServerCertificate(
	_ context.Context,
	request managedCAServerCertificateRequest,
) (tls.Certificate, error) {
	issuer.mutex.Lock()
	defer issuer.mutex.Unlock()
	issuer.count++
	if issuer.failAfter > 0 && issuer.count > issuer.failAfter {
		if issuer.issueErr != nil {
			return tls.Certificate{}, issuer.issueErr
		}
		return tls.Certificate{}, errors.New("managed ca issuer failure")
	}
	certTTL := issuer.certTTL
	if certTTL <= 0 {
		certTTL = request.ServerCertTTL
	}
	if certTTL <= 0 {
		certTTL = time.Hour
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return tls.Certificate{}, err
	}
	commonName := request.ServerCommonName
	if strings.TrimSpace(commonName) == "" {
		commonName = "bridge.internal.example"
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             time.Now().UTC().Add(-time.Minute),
		NotAfter:              time.Now().UTC().Add(certTTL),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), request.ServerSANDNS...),
		IPAddresses:           cloneNonNilIPs(request.ServerSANIPs),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, leafTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: leafDER,
	})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	issuedCertificate, err := tls.X509KeyPair(leafPEM, privateKeyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	parsedLeafCertificate, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return tls.Certificate{}, err
	}
	issuedCertificate.Leaf = parsedLeafCertificate
	return issuedCertificate, nil
}

// IssueCount 返回测试桩累计签发次数。
func (issuer *stubManagedCACertificateIssuer) IssueCount() int {
	issuer.mutex.Lock()
	defer issuer.mutex.Unlock()
	return issuer.count
}
