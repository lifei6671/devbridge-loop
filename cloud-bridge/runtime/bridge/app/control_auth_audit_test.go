package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
)

// TestNormalizeConnectorAuthSourceIP 验证 source_ip 会从远端地址中稳定提取。
func TestNormalizeConnectorAuthSourceIP(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name     string
		peerAddr string
		wantIP   string
	}{
		{name: "ipv4", peerAddr: "10.20.30.40:39080", wantIP: "10.20.30.40"},
		{name: "ipv6", peerAddr: "[2001:db8::1]:39080", wantIP: "2001:db8::1"},
		{name: "label", peerAddr: "bufconn", wantIP: "bufconn"},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			if gotIP := normalizeConnectorAuthSourceIP(testCase.peerAddr); gotIP != testCase.wantIP {
				testingObject.Fatalf("unexpected source ip: got=%s want=%s", gotIP, testCase.wantIP)
			}
		})
	}
}

// TestExtractConnectorTokenIDForAuditRequiresParsableToken 验证只有可解析 token 才会产出 token_id。
func TestExtractConnectorTokenIDForAuditRequiresParsableToken(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name     string
		rawToken string
		wantID   string
	}{
		{name: "valid", rawToken: "dbt_agent-local.secret-a", wantID: "agent-local"},
		{name: "missing-secret", rawToken: "dbt_agent-local", wantID: ""},
		{name: "empty-secret", rawToken: "dbt_agent-local.", wantID: ""},
		{name: "missing-token-id", rawToken: "dbt_.secret-a", wantID: ""},
		{name: "invalid-prefix", rawToken: "agent-local.secret-a", wantID: ""},
	}
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			if gotID := extractConnectorTokenIDForAudit(testCase.rawToken); gotID != testCase.wantID {
				testingObject.Fatalf("unexpected token id: got=%s want=%s", gotID, testCase.wantID)
			}
		})
	}
}

// TestServeControlChannelAuthAuditLogMasksTokenID 验证认证拒绝日志会脱敏 token_id 并保留 source_ip。
func TestServeControlChannelAuthAuditLogMasksTokenID(testingObject *testing.T) {
	binding, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new tcp binding failed: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	serverControl, err := binding.OpenControlChannel(serverConn)
	if err != nil {
		testingObject.Fatalf("open server control channel failed: %v", err)
	}
	clientControl, err := binding.OpenControlChannel(clientConn)
	if err != nil {
		testingObject.Fatalf("open client control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()

	var logBuffer bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{})))
	defer slog.SetDefault(originalLogger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = serveControlChannelWithDispatcherAndPeerAddr(
			ctx,
			serverControl,
			newControlMessageDispatcher(controlMessageDispatcherOptions{}),
			"10.20.30.40:39080",
		)
	}()

	helloPayload := pb.ConnectorHello{
		ConnectorID: "connector-auth-invalid-method",
		NodeName:    "node-a",
	}
	encodedHelloPayload, err := json.Marshal(helloPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector hello payload failed: %v", err)
	}
	helloFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		ConnectorID:  helloPayload.ConnectorID,
		Payload:      encodedHelloPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector hello frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, helloFrame); err != nil {
		testingObject.Fatalf("write connector hello frame failed: %v", err)
	}
	if _, err := clientControl.ReadControlFrame(context.Background()); err != nil {
		testingObject.Fatalf("read connector welcome frame failed: %v", err)
	}

	authPayload := pb.ConnectorAuth{
		AuthMethod: "hmac",
		Token:      "dbt_connector-auth-invalid-method.secret-a",
	}
	encodedAuthPayload, err := json.Marshal(authPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector auth payload failed: %v", err)
	}
	authFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  helloPayload.ConnectorID,
		Payload:      encodedAuthPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector auth frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, authFrame); err != nil {
		testingObject.Fatalf("write connector auth frame failed: %v", err)
	}
	if _, err := clientControl.ReadControlFrame(context.Background()); err != nil {
		testingObject.Fatalf("read connector auth ack failed: %v", err)
	}

	logEntry := decodeLastConnectorAuthAuditEntry(testingObject, logBuffer.Bytes())
	if gotConnectorID, _ := logEntry["connector_id"].(string); gotConnectorID != "connector-auth-invalid-method" {
		testingObject.Fatalf("unexpected connector id in audit log: got=%v", logEntry["connector_id"])
	}
	if gotTokenID, _ := logEntry["token_id"].(string); gotTokenID != "****thod" {
		testingObject.Fatalf("unexpected masked token id: got=%v want=%s", logEntry["token_id"], "****thod")
	}
	if gotSourceIP, _ := logEntry["source_ip"].(string); gotSourceIP != "10.20.30.40" {
		testingObject.Fatalf("unexpected source ip in audit log: got=%v want=%s", logEntry["source_ip"], "10.20.30.40")
	}
	if gotErrorCode, _ := logEntry["error_code"].(string); gotErrorCode != connectorAuthErrorInvalidMethod {
		testingObject.Fatalf("unexpected error code in audit log: got=%v want=%s", logEntry["error_code"], connectorAuthErrorInvalidMethod)
	}
	if strings.Contains(logBuffer.String(), "connector-auth-invalid-method.secret-a") {
		testingObject.Fatalf("expected raw token secret not present in audit logs")
	}
}

// TestServeControlChannelAuthAuditLogOmitsTokenIDBeforeWelcome 验证 pre-welcome 拒绝不会解析 payload 中的 token。
func TestServeControlChannelAuthAuditLogOmitsTokenIDBeforeWelcome(testingObject *testing.T) {
	binding, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		testingObject.Fatalf("new tcp binding failed: %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	}()

	serverControl, err := binding.OpenControlChannel(serverConn)
	if err != nil {
		testingObject.Fatalf("open server control channel failed: %v", err)
	}
	clientControl, err := binding.OpenControlChannel(clientConn)
	if err != nil {
		testingObject.Fatalf("open client control channel failed: %v", err)
	}
	defer func() {
		_ = clientControl.Close(context.Background())
	}()

	var logBuffer bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{})))
	defer slog.SetDefault(originalLogger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = serveControlChannelWithDispatcherAndPeerAddr(
			ctx,
			serverControl,
			newControlMessageDispatcher(controlMessageDispatcherOptions{}),
			"10.20.30.40:39080",
		)
	}()

	authPayload := pb.ConnectorAuth{
		AuthMethod: "token",
		Token:      "dbt_connector-auth-prewelcome.secret-a",
	}
	encodedAuthPayload, err := json.Marshal(authPayload)
	if err != nil {
		testingObject.Fatalf("marshal connector auth payload failed: %v", err)
	}
	authFrame, err := transport.EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  "connector-auth-prewelcome",
		Payload:      encodedAuthPayload,
	})
	if err != nil {
		testingObject.Fatalf("encode connector auth frame failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(ctx, authFrame); err != nil {
		testingObject.Fatalf("write connector auth frame failed: %v", err)
	}
	if _, err := clientControl.ReadControlFrame(context.Background()); err != nil {
		testingObject.Fatalf("read connector auth ack failed: %v", err)
	}

	logEntry := decodeLastConnectorAuthAuditEntry(testingObject, logBuffer.Bytes())
	if gotConnectorID, _ := logEntry["connector_id"].(string); gotConnectorID != "connector-auth-prewelcome" {
		testingObject.Fatalf("unexpected connector id in audit log: got=%v", logEntry["connector_id"])
	}
	if gotTokenID, _ := logEntry["token_id"].(string); gotTokenID != "" {
		testingObject.Fatalf("unexpected token id in audit log: got=%v want empty", logEntry["token_id"])
	}
	if gotSourceIP, _ := logEntry["source_ip"].(string); gotSourceIP != "10.20.30.40" {
		testingObject.Fatalf("unexpected source ip in audit log: got=%v want=%s", logEntry["source_ip"], "10.20.30.40")
	}
	if gotErrorCode, _ := logEntry["error_code"].(string); gotErrorCode != connectorAuthErrorInternal {
		testingObject.Fatalf("unexpected error code in audit log: got=%v want=%s", logEntry["error_code"], connectorAuthErrorInternal)
	}
	if strings.Contains(logBuffer.String(), "connector-auth-prewelcome.secret-a") {
		testingObject.Fatalf("expected raw token secret not present in audit logs")
	}
}

// decodeLastConnectorAuthAuditEntry 从日志缓冲中提取最后一条 connector auth audit JSON 记录。
func decodeLastConnectorAuthAuditEntry(testingObject *testing.T, rawLogs []byte) map[string]any {
	testingObject.Helper()

	lines := bytes.Split(bytes.TrimSpace(rawLogs), []byte{'\n'})
	for index := len(lines) - 1; index >= 0; index-- {
		line := bytes.TrimSpace(lines[index])
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			testingObject.Fatalf("unmarshal audit log entry failed: %v", err)
		}
		if message, _ := entry["msg"].(string); message == "connector auth audit" {
			return entry
		}
	}
	testingObject.Fatalf("expected connector auth audit log entry, got=%s", string(rawLogs))
	return nil
}
