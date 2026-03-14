package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
)

type codedError struct {
	code    string
	message string
}

func (err *codedError) Error() string {
	if err == nil {
		return ""
	}
	if err.code == "" {
		return err.message
	}
	if err.message == "" {
		return err.code
	}
	return fmt.Sprintf("%s: %s", err.code, err.message)
}

func errorsWithCode(code string, message string) error {
	return &codedError{code: code, message: message}
}

func codedErrorCode(err error) string {
	var localErr *codedError
	if errors.As(err, &localErr) {
		if localErr.code != "" {
			return localErr.code
		}
	}
	return "INTERNAL_ERROR"
}

type localRPCServer struct {
	runtime       *Runtime
	ipcTransport  string
	ipcEndpoint   string
	sessionSecret string

	listener localRPCListener
	closeMu  sync.Mutex
	closed   bool
}

type localRPCRequestBody struct {
	Type      string          `json:"type"`
	Method    string          `json:"method"`
	TimeoutMS uint64          `json:"timeout_ms"`
	Payload   json.RawMessage `json:"payload"`
}

type localRPCResponseBody struct {
	OK      bool               `json:"ok"`
	Payload any                `json:"payload,omitempty"`
	Error   *localRPCErrorBody `json:"error,omitempty"`
}

type localRPCErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type localRPCAuthBeginPayload struct {
	ClientName      string `json:"client_name"`
	ProtocolVersion string `json:"protocol_version"`
	ClientNonce     string `json:"client_nonce"`
}

type localRPCAuthCompletePayload struct {
	ProtocolVersion string `json:"protocol_version"`
	ClientNonce     string `json:"client_nonce"`
	AgentNonce      string `json:"agent_nonce"`
	ClientProof     string `json:"client_proof"`
}

type localRPCAuthPending struct {
	protocolVersion string
	clientNonce     string
	agentNonce      string
}

type localRPCConnectionAuthState struct {
	authenticated bool
	pending       *localRPCAuthPending
}

type localRPCFailure struct {
	code    string
	message string
}

func newLocalRPCServer(agentRuntime *Runtime) (*localRPCServer, error) {
	if agentRuntime == nil {
		return nil, errors.New("runtime is nil")
	}
	ipcTransport := strings.TrimSpace(os.Getenv("DEV_AGENT_IPC_TRANSPORT"))
	if ipcTransport == "" {
		if runtime.GOOS == "windows" {
			ipcTransport = "named_pipe"
		} else {
			ipcTransport = "uds"
		}
	}
	ipcEndpoint := strings.TrimSpace(os.Getenv("DEV_AGENT_IPC_ENDPOINT"))
	if ipcEndpoint == "" {
		return nil, errors.New("DEV_AGENT_IPC_ENDPOINT is required")
	}
	sessionSecret := strings.TrimSpace(os.Getenv("DEV_AGENT_SESSION_SECRET"))
	if err := validateHexToken(sessionSecret, 64, "session_secret"); err != nil {
		return nil, fmt.Errorf("invalid DEV_AGENT_SESSION_SECRET: %w", err)
	}
	listener, err := newLocalRPCListener(ipcTransport, ipcEndpoint)
	if err != nil {
		return nil, err
	}
	return &localRPCServer{
		runtime:       agentRuntime,
		ipcTransport:  ipcTransport,
		ipcEndpoint:   ipcEndpoint,
		sessionSecret: sessionSecret,
		listener:      listener,
	}, nil
}

func (server *localRPCServer) Close() error {
	if server == nil {
		return nil
	}
	server.closeMu.Lock()
	defer server.closeMu.Unlock()
	if server.closed {
		return nil
	}
	server.closed = true
	if server.listener != nil {
		return server.listener.Close()
	}
	return nil
}

func (server *localRPCServer) Serve(ctx context.Context) error {
	if server == nil {
		return errors.New("localrpc server is nil")
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connection, err := server.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				continue
			}
			return err
		}
		serveErr := server.serveConnection(ctx, connection)
		_ = connection.Close()
		if serveErr != nil && ctx.Err() == nil {
			// 单连接协议错误不影响 listener 生命周期，允许宿主重新连入。
			continue
		}
	}
}

func parseLocalRPCRequestBody(frame localRPCFrame) (localRPCRequestBody, error) {
	if frame.frameType != localRPCFrameTypeRequest {
		return localRPCRequestBody{}, errorsWithCode("PROTOCOL_ERROR", "frame type is not request")
	}
	var requestBody localRPCRequestBody
	if err := json.Unmarshal(frame.body, &requestBody); err != nil {
		return localRPCRequestBody{}, errorsWithCode("PROTOCOL_ERROR", fmt.Sprintf("decode request body failed: %v", err))
	}
	requestBody.Method = strings.TrimSpace(requestBody.Method)
	if requestBody.Method == "" {
		return localRPCRequestBody{}, errorsWithCode("INVALID_REQUEST", "method is required")
	}
	if len(requestBody.Payload) == 0 {
		requestBody.Payload = json.RawMessage(`{}`)
	}
	return requestBody, nil
}

func (server *localRPCServer) writeResponse(connection localRPCConn, requestID [16]byte, responseBody localRPCResponseBody) error {
	responseBytes, err := json.Marshal(responseBody)
	if err != nil {
		return err
	}
	return writeLocalRPCFrame(connection, localRPCFrame{
		frameType: localRPCFrameTypeResponse,
		requestID: requestID,
		body:      responseBytes,
	})
}

func (server *localRPCServer) writeSuccess(connection localRPCConn, requestID [16]byte, payload any) error {
	return server.writeResponse(connection, requestID, localRPCResponseBody{
		OK:      true,
		Payload: payload,
	})
}

func (server *localRPCServer) writeFailure(connection localRPCConn, requestID [16]byte, code string, message string) error {
	normalizedCode := strings.TrimSpace(code)
	if normalizedCode == "" {
		normalizedCode = "INTERNAL_ERROR"
	}
	return server.writeResponse(connection, requestID, localRPCResponseBody{
		OK: false,
		Error: &localRPCErrorBody{
			Code:    normalizedCode,
			Message: strings.TrimSpace(message),
		},
	})
}

func (server *localRPCServer) writePong(connection localRPCConn) error {
	return writeLocalRPCFrame(connection, localRPCFrame{
		frameType: localRPCFrameTypePong,
		requestID: [16]byte{},
		body:      nil,
	})
}

func (server *localRPCServer) serveConnection(ctx context.Context, connection localRPCConn) error {
	connectionAuthState := &localRPCConnectionAuthState{}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := readLocalRPCFrame(connection)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		switch frame.frameType {
		case localRPCFrameTypePing:
			if err := server.writePong(connection); err != nil {
				return err
			}
		case localRPCFrameTypeRequest:
			requestBody, parseErr := parseLocalRPCRequestBody(frame)
			if parseErr != nil {
				if err := server.writeFailure(connection, frame.requestID, codedErrorCode(parseErr), parseErr.Error()); err != nil {
					return err
				}
				continue
			}
			payload, failure := server.dispatchRequest(requestBody, connectionAuthState)
			if failure != nil {
				if err := server.writeFailure(connection, frame.requestID, failure.code, failure.message); err != nil {
					return err
				}
				continue
			}
			if err := server.writeSuccess(connection, frame.requestID, payload); err != nil {
				return err
			}
		default:
			// 客户端只允许发送 request/ping，其他帧类型视为协议违规并断开。
			return errorsWithCode("PROTOCOL_ERROR", "client sent an unsupported frame type")
		}
	}
}

func decodePayload[T any](payload json.RawMessage) (T, error) {
	var value T
	if len(payload) == 0 {
		return value, nil
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (server *localRPCServer) handleAuthBegin(
	requestBody localRPCRequestBody,
	connectionAuthState *localRPCConnectionAuthState,
) (any, *localRPCFailure) {
	beginPayload, err := decodePayload[localRPCAuthBeginPayload](requestBody.Payload)
	if err != nil {
		return nil, &localRPCFailure{code: "INVALID_REQUEST", message: "invalid app.auth.begin payload"}
	}
	protocolVersion := strings.TrimSpace(beginPayload.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = localRPCAuthProtocolVersion
	}
	if strings.TrimSpace(beginPayload.ClientNonce) == "" {
		return nil, &localRPCFailure{code: "INVALID_REQUEST", message: "client_nonce is required"}
	}
	if err := validateHexToken(beginPayload.ClientNonce, 16, "client_nonce"); err != nil {
		return nil, &localRPCFailure{code: "INVALID_REQUEST", message: err.Error()}
	}
	agentNonce, err := generateRandomHex(16)
	if err != nil {
		return nil, &localRPCFailure{code: "INTERNAL_ERROR", message: fmt.Sprintf("generate agent_nonce failed: %v", err)}
	}
	agentProof, err := computeAgentProof(
		server.sessionSecret,
		beginPayload.ClientNonce,
		agentNonce,
		protocolVersion,
	)
	if err != nil {
		return nil, &localRPCFailure{code: "INTERNAL_ERROR", message: fmt.Sprintf("compute agent proof failed: %v", err)}
	}
	connectionAuthState.pending = &localRPCAuthPending{
		protocolVersion: protocolVersion,
		clientNonce:     beginPayload.ClientNonce,
		agentNonce:      agentNonce,
	}
	return map[string]any{
		"protocol_version": protocolVersion,
		"agent_nonce":      agentNonce,
		"agent_proof":      agentProof,
	}, nil
}

func (server *localRPCServer) handleAuthComplete(
	requestBody localRPCRequestBody,
	connectionAuthState *localRPCConnectionAuthState,
) (any, *localRPCFailure) {
	if connectionAuthState.pending == nil {
		return nil, &localRPCFailure{code: "UNAUTHORIZED", message: "app.auth.begin is required first"}
	}
	completePayload, err := decodePayload[localRPCAuthCompletePayload](requestBody.Payload)
	if err != nil {
		return nil, &localRPCFailure{code: "INVALID_REQUEST", message: "invalid app.auth.complete payload"}
	}
	if strings.TrimSpace(completePayload.ClientNonce) != connectionAuthState.pending.clientNonce {
		return nil, &localRPCFailure{code: "UNAUTHORIZED", message: "client_nonce mismatch"}
	}
	if strings.TrimSpace(completePayload.AgentNonce) != connectionAuthState.pending.agentNonce {
		return nil, &localRPCFailure{code: "UNAUTHORIZED", message: "agent_nonce mismatch"}
	}
	protocolVersion := strings.TrimSpace(completePayload.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = connectionAuthState.pending.protocolVersion
	}
	expectedClientProof, err := computeHostProof(
		server.sessionSecret,
		connectionAuthState.pending.clientNonce,
		connectionAuthState.pending.agentNonce,
		protocolVersion,
	)
	if err != nil {
		return nil, &localRPCFailure{code: "INTERNAL_ERROR", message: fmt.Sprintf("compute host proof failed: %v", err)}
	}
	if !constantTimeEqual(strings.TrimSpace(completePayload.ClientProof), expectedClientProof) {
		return nil, &localRPCFailure{code: "UNAUTHORIZED", message: "client_proof mismatch"}
	}
	connectionAuthState.authenticated = true
	connectionAuthState.pending = nil
	return map[string]any{
		"authenticated": true,
	}, nil
}

func (server *localRPCServer) dispatchRequest(
	requestBody localRPCRequestBody,
	connectionAuthState *localRPCConnectionAuthState,
) (any, *localRPCFailure) {
	switch requestBody.Method {
	case "app.auth.begin":
		return server.handleAuthBegin(requestBody, connectionAuthState)
	case "app.auth.complete":
		return server.handleAuthComplete(requestBody, connectionAuthState)
	}

	if !connectionAuthState.authenticated {
		return nil, &localRPCFailure{code: "UNAUTHORIZED", message: "app.auth.begin/app.auth.complete is required"}
	}

	switch requestBody.Method {
	case "app.bootstrap":
		return server.runtime.agentSnapshotPayload(), nil
	case "app.shutdown":
		go func(runtimeInstance *Runtime) {
			_ = runtimeInstance.Shutdown(context.Background())
		}(server.runtime)
		return map[string]any{
			"accepted": true,
		}, nil
	case "agent.snapshot":
		return server.runtime.agentSnapshotPayload(), nil
	case "session.snapshot":
		return server.runtime.sessionSnapshotPayload(), nil
	case "session.reconnect":
		server.runtime.requestBridgeReconnect(true)
		return server.runtime.sessionSnapshotPayload(), nil
	case "session.drain":
		server.runtime.requestBridgeDrain()
		return server.runtime.sessionSnapshotPayload(), nil
	case "service.list":
		return server.runtime.serviceListPayload(), nil
	case "tunnel.list":
		return server.runtime.tunnelListPayload(), nil
	case "traffic.stats.snapshot":
		// traffic 指标优先返回 runtime 数据面真实链路统计，避免占位值误导 UI。
		return server.runtime.trafficStatsSnapshotPayload(), nil
	case "config.snapshot":
		return server.runtime.configSnapshotPayload(server.ipcTransport, server.ipcEndpoint), nil
	case "diagnose.snapshot":
		return server.runtime.diagnoseSnapshotPayload(), nil
	case "diagnose.logs":
		return map[string]any{"items": []map[string]any{}}, nil
	default:
		return nil, &localRPCFailure{code: "METHOD_NOT_ALLOWED", message: fmt.Sprintf("method %s is not allowed", requestBody.Method)}
	}
}
