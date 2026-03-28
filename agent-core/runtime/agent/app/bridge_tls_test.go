package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestResolveBridgeTLSServerNameFallsBackToBridgeHost 验证未显式配置 server_name 时会回退到 bridge host。
func TestResolveBridgeTLSServerNameFallsBackToBridgeHost(testingObject *testing.T) {
	testingObject.Parallel()

	serverName, err := resolveBridgeTLSServerName(BridgeTLSConfig{}, "bridge.internal.example:39080")
	if err != nil {
		testingObject.Fatalf("resolve bridge tls server name failed: %v", err)
	}
	if serverName != "bridge.internal.example" {
		testingObject.Fatalf("unexpected server name: got=%s want=%s", serverName, "bridge.internal.example")
	}
}

// TestBuildBridgeClientTLSConfigLoadsRootCA 验证 Agent 可基于 Root CA 文件构造 TLS 配置。
func TestBuildBridgeClientTLSConfigLoadsRootCA(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	rootCAFile := filepath.Join(tempDir, "root-ca.pem")
	if err := os.WriteFile(rootCAFile, buildTestCertificatePEM(testingObject), 0o600); err != nil {
		testingObject.Fatalf("write root ca file failed: %v", err)
	}

	tlsConfig, err := buildBridgeClientTLSConfig(BridgeTLSConfig{
		Enabled:    true,
		RootCAFile: rootCAFile,
	}, "bridge.internal.example:39080")
	if err != nil {
		testingObject.Fatalf("build bridge client tls config failed: %v", err)
	}
	if tlsConfig == nil {
		testingObject.Fatalf("expected non-nil tls config")
	}
	if tlsConfig.ServerName != "bridge.internal.example" {
		testingObject.Fatalf("unexpected tls server name: got=%s want=%s", tlsConfig.ServerName, "bridge.internal.example")
	}
	if tlsConfig.MinVersion == 0 {
		testingObject.Fatalf("expected tls min version configured")
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
	if tlsConfig.ClientSessionCache != nil {
		testingObject.Fatalf("expected client session cache disabled")
	}
	if tlsConfig.RootCAs == nil {
		testingObject.Fatalf("expected tls root ca pool initialized")
	}
}

func TestBuildBridgeClientTLSConfigRejectsInvalidRootCAFileWithPathHint(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	rootCAFile := filepath.Join(tempDir, "root-ca.key")
	if err := os.WriteFile(
		rootCAFile,
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-cert")}),
		0o600,
	); err != nil {
		testingObject.Fatalf("write invalid root ca file failed: %v", err)
	}

	_, err := buildBridgeClientTLSConfig(BridgeTLSConfig{
		Enabled:    true,
		RootCAFile: rootCAFile,
	}, "bridge.internal.example:39080")
	if err == nil {
		testingObject.Fatalf("expected invalid root ca file error")
	}
	if !strings.Contains(err.Error(), rootCAFile) {
		testingObject.Fatalf("expected error contains root ca file path, got=%v", err)
	}
	if !strings.Contains(err.Error(), "root-ca.crt") {
		testingObject.Fatalf("expected error contains managed_ca root cert hint, got=%v", err)
	}
}

// TestBuildBridgeQUICClientTLSConfigClearsHTTP2ALPN 验证 QUIC TLS 配置不会携带 gRPC 的 h2 ALPN。
func TestBuildBridgeQUICClientTLSConfigClearsHTTP2ALPN(testingObject *testing.T) {
	testingObject.Parallel()

	fixture := newBridgeQUICTLSFixtureForTest(testingObject)
	tlsConfig, err := buildBridgeQUICClientTLSConfig(BridgeTLSConfig{
		Enabled:    true,
		RootCAFile: fixture.rootCAFile,
		ServerName: fixture.serverName,
	}, "127.0.0.1:39080")
	if err != nil {
		testingObject.Fatalf("build bridge quic tls config failed: %v", err)
	}
	if tlsConfig == nil {
		testingObject.Fatalf("expected non-nil quic tls config")
	}
	if len(tlsConfig.NextProtos) != 0 {
		testingObject.Fatalf("expected quic tls config to leave ALPN empty, got=%v", tlsConfig.NextProtos)
	}
	if tlsConfig.ServerName != fixture.serverName {
		testingObject.Fatalf("unexpected quic tls server name: got=%s want=%s", tlsConfig.ServerName, fixture.serverName)
	}
	if tlsConfig.RootCAs == nil {
		testingObject.Fatalf("expected quic tls root ca pool initialized")
	}
}

type bridgeQUICTLSFixture struct {
	serverTLSConfig *tls.Config
	rootCAFile      string
	serverName      string
}

func newBridgeQUICTLSFixtureForTest(testingObject *testing.T) bridgeQUICTLSFixture {
	testingObject.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		testingObject.Fatalf("generate ed25519 key failed: %v", err)
	}
	certificateTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             time.Now().UTC().Add(-time.Hour),
		NotAfter:              time.Now().UTC().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, publicKey, privateKey)
	if err != nil {
		testingObject.Fatalf("create self-signed certificate failed: %v", err)
	}
	certificate, err := x509.ParseCertificate(derBytes)
	if err != nil {
		testingObject.Fatalf("parse certificate failed: %v", err)
	}
	rootCAFile := filepath.Join(testingObject.TempDir(), "root-ca.pem")
	if err := os.WriteFile(rootCAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}), 0o600); err != nil {
		testingObject.Fatalf("write root ca file failed: %v", err)
	}
	return bridgeQUICTLSFixture{
		serverTLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{
				Certificate: [][]byte{derBytes},
				PrivateKey:  privateKey,
				Leaf:        certificate,
			}},
			MinVersion: tls.VersionTLS13,
		},
		rootCAFile: rootCAFile,
		serverName: "127.0.0.1",
	}
}

// buildTestCertificatePEM 生成测试用 PEM 证书，供 Root CA 读取路径验证使用。
func buildTestCertificatePEM(testingObject *testing.T) []byte {
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
			CommonName: "bridge-root-ca",
		},
		NotBefore:             time.Now().UTC().Add(-time.Hour),
		NotAfter:              time.Now().UTC().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		testingObject.Fatalf("create certificate failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})
}
