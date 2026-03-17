package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
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
	if tlsConfig.RootCAs == nil {
		testingObject.Fatalf("expected tls root ca pool initialized")
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
