package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/hostapi"
)

const defaultSessionCookieName = "devbridge_agent_session"

type ServerOptions struct {
	BasePath          string
	SessionCookieName string
	Username          string
	Password          string
	Handler           hostapi.Handler
	UIHandler         http.Handler
	Now               func() time.Time
}

type Server struct {
	basePath          string
	baseAPIPath       string
	sessionCookieName string
	username          string
	password          string
	handler           hostapi.Handler
	uiHandler         http.Handler
	sessionStore      *sessionStore
	now               func() time.Time
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	ExpiresAtMS   uint64 `json:"expires_at_ms,omitempty"`
}

func NewServer(options ServerOptions) (*Server, error) {
	basePath := normalizeBasePath(options.BasePath)
	sessionCookieName := strings.TrimSpace(options.SessionCookieName)
	if sessionCookieName == "" {
		sessionCookieName = defaultSessionCookieName
	}
	if strings.TrimSpace(options.Username) == "" {
		return nil, fmt.Errorf("new http api server: empty username")
	}
	if strings.TrimSpace(options.Password) == "" {
		return nil, fmt.Errorf("new http api server: empty password")
	}
	if options.Handler == nil {
		return nil, fmt.Errorf("new http api server: hostapi handler is nil")
	}
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = func() time.Time {
			return time.Now().UTC()
		}
	}
	return &Server{
		basePath:          basePath,
		baseAPIPath:       strings.TrimRight(basePath, "/") + "/api",
		sessionCookieName: sessionCookieName,
		username:          strings.TrimSpace(options.Username),
		password:          strings.TrimSpace(options.Password),
		handler:           options.Handler,
		uiHandler:         options.UIHandler,
		sessionStore:      newSessionStore(0),
		now:               nowFunc,
	}, nil
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if server == nil {
		http.Error(writer, "server is nil", http.StatusInternalServerError)
		return
	}
	switch request.URL.Path {
	case server.baseAPIPath + "/login":
		server.handleLogin(writer, request)
	case server.baseAPIPath + "/logout":
		server.handleLogout(writer, request)
	case server.baseAPIPath + "/session":
		server.handleSession(writer, request)
	case server.baseAPIPath + "/events/stream":
		server.handleEventsStream(writer, request)
	case server.baseAPIPath + "/agent/snapshot":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodAgentSnapshot, nil)
	case server.baseAPIPath + "/app/config":
		server.handleAppConfig(writer, request)
	case server.baseAPIPath + "/session/snapshot":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodSessionSnapshot, nil)
	case server.baseAPIPath + "/session/reconnect":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodSessionReconnect, nil)
	case server.baseAPIPath + "/session/drain":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodSessionDrain, nil)
	case server.baseAPIPath + "/tunnels":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodTunnelList, nil)
	case server.baseAPIPath + "/traffic/stats":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodTrafficStatsSnapshot, nil)
	case server.baseAPIPath + "/diagnose/summary":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodDiagnoseSnapshot, nil)
	case server.baseAPIPath + "/diagnose/logs":
		server.handleProtectedHostAPI(writer, request, hostapi.MethodDiagnoseLogs, nil)
	case server.baseAPIPath + "/services":
		server.handleServices(writer, request)
	default:
		if request.URL.Path == server.baseAPIPath || strings.HasPrefix(request.URL.Path, server.baseAPIPath+"/") {
			http.NotFound(writer, request)
			return
		}
		if server.shouldServeUI(request.URL.Path) {
			server.serveUI(writer, request)
			return
		}
		http.NotFound(writer, request)
	}
}

func (server *Server) shouldServeUI(requestPath string) bool {
	if server == nil || server.uiHandler == nil {
		return false
	}
	return requestPath == server.basePath || strings.HasPrefix(requestPath, server.basePath+"/")
}

func (server *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	if server == nil || server.uiHandler == nil {
		http.NotFound(writer, request)
		return
	}
	if request.URL.Path == server.basePath {
		redirectURL := server.basePath + "/"
		if request.URL.RawQuery != "" {
			redirectURL += "?" + request.URL.RawQuery
		}
		http.Redirect(writer, request, redirectURL, http.StatusPermanentRedirect)
		return
	}
	clonedRequest := request.Clone(request.Context())
	trimmedPath := strings.TrimPrefix(request.URL.Path, server.basePath)
	if trimmedPath == "" {
		trimmedPath = "/"
	}
	clonedRequest.URL.Path = trimmedPath
	server.uiHandler.ServeHTTP(writer, clonedRequest)
}

func normalizeBasePath(basePath string) string {
	normalized := strings.TrimSpace(basePath)
	if normalized == "" {
		return "/agent"
	}
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return strings.TrimRight(normalized, "/")
}

func (server *Server) nowUTC() time.Time {
	return server.now().UTC()
}

func (server *Server) handleLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	loginPayload, err := decodeJSONBody[loginRequest](request)
	if err != nil {
		server.writeJSONError(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid login payload")
		return
	}
	if !validateCredentials(server.username, server.password, loginPayload.Username, loginPayload.Password) {
		server.writeJSONError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid username or password")
		return
	}
	now := server.nowUTC()
	savedSession, err := server.sessionStore.create(now, strings.TrimSpace(loginPayload.Username))
	if err != nil {
		server.writeJSONError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	http.SetCookie(
		writer,
		buildSessionCookie(server.sessionCookieName, savedSession.token, server.cookiePath(), savedSession.expiresAt),
	)
	server.writeJSON(writer, http.StatusOK, sessionResponse{
		Authenticated: true,
		Username:      savedSession.username,
		ExpiresAtMS:   uint64(savedSession.expiresAt.UnixMilli()),
	})
}

func (server *Server) handleLogout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if cookie, err := request.Cookie(server.sessionCookieName); err == nil {
		server.sessionStore.delete(cookie.Value)
	}
	http.SetCookie(writer, buildExpiredSessionCookie(server.sessionCookieName, server.cookiePath()))
	server.writeJSON(writer, http.StatusOK, map[string]any{"authenticated": false})
}

func (server *Server) handleSession(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	savedSession, ok := server.requireSession(writer, request)
	if !ok {
		return
	}
	server.writeJSON(writer, http.StatusOK, sessionResponse{
		Authenticated: true,
		Username:      savedSession.username,
		ExpiresAtMS:   uint64(savedSession.expiresAt.UnixMilli()),
	})
}

func (server *Server) handleServices(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.handleProtectedHostAPI(writer, request, hostapi.MethodServiceList, nil)
	case http.MethodPost:
		server.handleProtectedHostAPI(writer, request, hostapi.MethodServiceAdd, request.Body)
	case http.MethodDelete:
		payload, err := json.Marshal(map[string]any{
			"logical_service_id": strings.TrimSpace(request.URL.Query().Get("logical_service_id")),
			"instance_id":        strings.TrimSpace(request.URL.Query().Get("instance_id")),
		})
		if err != nil {
			server.writeJSONError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			return
		}
		server.handleProtectedHostAPI(writer, request, hostapi.MethodServiceDelete, strings.NewReader(string(payload)))
	default:
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (server *Server) handleAppConfig(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		server.handleProtectedHostAPI(writer, request, hostapi.MethodConfigSnapshot, nil)
	case http.MethodPut:
		server.handleConfigUpdate(writer, request)
	default:
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (server *Server) handleConfigUpdate(writer http.ResponseWriter, request *http.Request) {
	savedSession, ok := server.requireSession(writer, request)
	if !ok {
		return
	}
	payload, err := resolvePayload(request.Body)
	if err != nil {
		server.writeJSONError(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid request payload")
		return
	}
	payloadWithActor, err := injectUpdatedBy(payload, savedSession.username)
	if err != nil {
		server.writeJSONError(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid request payload")
		return
	}
	response, failure := server.handler.Handle(context.Background(), hostapi.Request{
		Method:  hostapi.MethodConfigUpdate,
		Payload: payloadWithActor,
	})
	if failure != nil {
		statusCode := http.StatusInternalServerError
		if failure.Code == "INVALID_REQUEST" {
			statusCode = http.StatusBadRequest
		}
		server.writeJSONError(writer, statusCode, failure.Code, failure.Message)
		return
	}
	server.writeJSON(writer, http.StatusOK, response.Payload)
}

func (server *Server) handleProtectedHostAPI(writer http.ResponseWriter, request *http.Request, method hostapi.Method, bodySource io.Reader) {
	if _, ok := server.requireSession(writer, request); !ok {
		return
	}
	payload, err := resolvePayload(bodySource)
	if err != nil {
		server.writeJSONError(writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid request payload")
		return
	}
	response, failure := server.handler.Handle(context.Background(), hostapi.Request{
		Method:  method,
		Payload: payload,
	})
	if failure != nil {
		statusCode := http.StatusInternalServerError
		if failure.Code == "INVALID_REQUEST" {
			statusCode = http.StatusBadRequest
		}
		if failure.Code == "METHOD_NOT_ALLOWED" {
			statusCode = http.StatusMethodNotAllowed
		}
		server.writeJSONError(writer, statusCode, failure.Code, failure.Message)
		return
	}
	server.writeJSON(writer, http.StatusOK, response.Payload)
}

func resolvePayload(bodySource io.Reader) (json.RawMessage, error) {
	if bodySource == nil {
		return json.RawMessage(`{}`), nil
	}
	raw, err := io.ReadAll(bodySource)
	if err != nil {
		return nil, err
	}
	return normalizePayload(raw), nil
}

func normalizePayload(raw []byte) json.RawMessage {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func injectUpdatedBy(payload json.RawMessage, username string) (json.RawMessage, error) {
	normalizedPayload := normalizePayload(payload)
	body := map[string]any{}
	if err := json.Unmarshal(normalizedPayload, &body); err != nil {
		return nil, err
	}
	body["updated_by"] = strings.TrimSpace(username)
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (server *Server) requireSession(writer http.ResponseWriter, request *http.Request) (session, bool) {
	cookie, err := request.Cookie(server.sessionCookieName)
	if err != nil {
		server.writeJSONError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "login required")
		return session{}, false
	}
	savedSession, exists := server.sessionStore.get(server.nowUTC(), cookie.Value)
	if !exists {
		server.writeJSONError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "login required")
		return session{}, false
	}
	return savedSession, true
}

func (server *Server) cookiePath() string {
	return server.basePath + "/"
}

func (server *Server) writeJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}

func (server *Server) writeJSONError(writer http.ResponseWriter, statusCode int, code string, message string) {
	server.writeJSON(writer, statusCode, map[string]any{
		"code":    strings.TrimSpace(code),
		"message": strings.TrimSpace(message),
	})
}

func decodeJSONBody[T any](request *http.Request) (T, error) {
	var payload T
	if request == nil || request.Body == nil {
		return payload, fmt.Errorf("request body is nil")
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		return payload, err
	}
	return payload, nil
}
