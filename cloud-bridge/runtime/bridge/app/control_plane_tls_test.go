package app

import (
	"errors"
	"io"
	"net"
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
