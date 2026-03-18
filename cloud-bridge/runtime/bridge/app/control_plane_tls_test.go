package app

import (
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
		time.Sleep(20 * time.Millisecond)
		_, _ = clientConn.Write([]byte{0x03})
		time.Sleep(20 * time.Millisecond)
		_, _ = clientConn.Write([]byte{0x03})
	}()

	detectedConn, isTLSClientHello, err := detectTLSClientHello(serverConn, 30*time.Millisecond)
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
			CommonName: "bridge-control-plane",
		},
		NotBefore:             time.Now().UTC().Add(-time.Hour),
		NotAfter:              time.Now().UTC().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"bridge.internal.example"},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		testingObject.Fatalf("create certificate failed: %v", err)
	}
	certFile := filepath.Join(directory, "control-plane.crt")
	keyFile := filepath.Join(directory, "control-plane.key")
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
	return certFile, keyFile
}
