package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/hostapi"
)

type testHostHandler struct {
	requests  []hostapi.Request
	responses map[hostapi.Method]any
}

func (handler *testHostHandler) Handle(_ context.Context, request hostapi.Request) (hostapi.Response, *hostapi.Failure) {
	handler.requests = append(handler.requests, request)
	var payload any = map[string]any{"source": "test"}
	if handler.responses != nil {
		if responsePayload, ok := handler.responses[request.Method]; ok {
			payload = responsePayload
		}
	}
	return hostapi.Response{
		Method:  request.Method,
		Payload: payload,
	}, nil
}

func TestProtectedEndpointRequiresLogin(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           &testHostHandler{},
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/agent/api/services", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		testingObject.Fatalf("unexpected status code: got=%d want=%d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestEventsStreamRequiresLogin(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           &testHostHandler{},
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/agent/api/events/stream", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		testingObject.Fatalf("unexpected status code: got=%d want=%d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestLoginAndSession(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           &testHostHandler{},
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	loginBody := bytes.NewBufferString(`{"username":"admin","password":"change-me"}`)
	loginRequest := httptest.NewRequest(http.MethodPost, "/agent/api/login", loginBody)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	server.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected login status code: got=%d want=%d", loginRecorder.Code, http.StatusOK)
	}
	loginResponse := loginRecorder.Result()
	if len(loginResponse.Cookies()) == 0 {
		testingObject.Fatalf("expected session cookie after login")
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/agent/api/session", nil)
	sessionRequest.AddCookie(loginResponse.Cookies()[0])
	sessionRecorder := httptest.NewRecorder()
	server.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected session status code: got=%d want=%d", sessionRecorder.Code, http.StatusOK)
	}
	var sessionResponse struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username"`
	}
	if err := json.Unmarshal(sessionRecorder.Body.Bytes(), &sessionResponse); err != nil {
		testingObject.Fatalf("decode session response failed: %v", err)
	}
	if !sessionResponse.Authenticated || sessionResponse.Username != "admin" {
		testingObject.Fatalf("unexpected session response: %+v", sessionResponse)
	}
}

func TestConfigSnapshotUsesHostAPI(testingObject *testing.T) {
	testingObject.Parallel()

	handler := &testHostHandler{
		responses: map[hostapi.Method]any{
			hostapi.MethodConfigSnapshot: map[string]any{
				"agent_id": "agent-web",
				"source":   "agent.runtime",
			},
		},
	}
	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           handler,
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	cookie := mustLogin(testingObject, server)
	request := httptest.NewRequest(http.MethodGet, "/agent/api/app/config", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected config status code: got=%d want=%d", recorder.Code, http.StatusOK)
	}
	if len(handler.requests) != 1 || handler.requests[0].Method != hostapi.MethodConfigSnapshot {
		testingObject.Fatalf("unexpected hostapi requests: %+v", handler.requests)
	}
}

func TestConfigUpdateUsesHostAPI(testingObject *testing.T) {
	testingObject.Parallel()

	handler := &testHostHandler{}
	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           handler,
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	cookie := mustLogin(testingObject, server)
	request := httptest.NewRequest(
		http.MethodPut,
		"/agent/api/app/config",
		bytes.NewBufferString(`{"config":{"agent_id":"agent-web"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected config update status code: got=%d want=%d", recorder.Code, http.StatusOK)
	}
	if len(handler.requests) != 1 || handler.requests[0].Method != hostapi.MethodConfigUpdate {
		testingObject.Fatalf("unexpected hostapi requests: %+v", handler.requests)
	}
	if !bytes.Contains(handler.requests[0].Payload, []byte(`"updated_by":"admin"`)) {
		testingObject.Fatalf("expected updated_by in payload: %s", string(handler.requests[0].Payload))
	}
}

func TestConfigUpdateAllowsMissingOrBlankSessionAuthToken(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name string
		body string
	}{
		{
			name: "missing auth token",
			body: `{"config":{"agent_id":"agent-web","session":{"auth_method":"token"}}}`,
		},
		{
			name: "blank auth token",
			body: `{"config":{"agent_id":"agent-web","session":{"auth_method":"token","auth_token":""}}}`,
		},
	}

	for _, testCase := range testCases {
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			handler := &testHostHandler{}
			server, err := NewServer(ServerOptions{
				BasePath:          "/agent",
				SessionCookieName: "devbridge_agent_session",
				Username:          "admin",
				Password:          "change-me",
				Handler:           handler,
			})
			if err != nil {
				testingObject.Fatalf("new server failed: %v", err)
			}

			cookie := mustLogin(testingObject, server)
			request := httptest.NewRequest(
				http.MethodPut,
				"/agent/api/app/config",
				bytes.NewBufferString(testCase.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(cookie)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				testingObject.Fatalf("unexpected config update status code: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if len(handler.requests) != 1 || handler.requests[0].Method != hostapi.MethodConfigUpdate {
				testingObject.Fatalf("unexpected hostapi requests: %+v", handler.requests)
			}
		})
	}
}

func TestUnknownAPIPathReturnsNotFoundInsteadOfUI(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           &testHostHandler{},
		UIHandler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("<html>ui</html>"))
		}),
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/agent/api/unknown", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestServiceMutationsUseHostAPI(testingObject *testing.T) {
	testingObject.Parallel()

	handler := &testHostHandler{}
	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           handler,
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	cookie := mustLogin(testingObject, server)

	addRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/api/services",
		bytes.NewBufferString(`{"instance_id":"inst-order-service","service_name":"order-service"}`),
	)
	addRequest.Header.Set("Content-Type", "application/json")
	addRequest.AddCookie(cookie)
	addRecorder := httptest.NewRecorder()
	server.ServeHTTP(addRecorder, addRequest)
	if addRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected service add status code: got=%d want=%d", addRecorder.Code, http.StatusOK)
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/agent/api/services?instance_id=inst-order-service",
		nil,
	)
	deleteRequest.AddCookie(cookie)
	deleteRecorder := httptest.NewRecorder()
	server.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected service delete status code: got=%d want=%d", deleteRecorder.Code, http.StatusOK)
	}

	if len(handler.requests) != 2 {
		testingObject.Fatalf("unexpected request count: got=%d want=2", len(handler.requests))
	}
	if handler.requests[0].Method != hostapi.MethodServiceAdd {
		testingObject.Fatalf("unexpected first method: %s", handler.requests[0].Method)
	}
	if handler.requests[1].Method != hostapi.MethodServiceDelete {
		testingObject.Fatalf("unexpected second method: %s", handler.requests[1].Method)
	}
}

func TestSessionAndTunnelEndpointsUseHostAPI(testingObject *testing.T) {
	testingObject.Parallel()

	handler := &testHostHandler{}
	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           handler,
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	cookie := mustLogin(testingObject, server)

	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/agent/api/agent/snapshot", nil),
		httptest.NewRequest(http.MethodGet, "/agent/api/session/snapshot", nil),
		httptest.NewRequest(http.MethodPost, "/agent/api/session/reconnect", nil),
		httptest.NewRequest(http.MethodPost, "/agent/api/session/drain", nil),
		httptest.NewRequest(http.MethodGet, "/agent/api/tunnels", nil),
		httptest.NewRequest(http.MethodGet, "/agent/api/diagnose/summary", nil),
	}
	for _, request := range requests {
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			testingObject.Fatalf("unexpected status for %s %s: got=%d want=%d", request.Method, request.URL.Path, recorder.Code, http.StatusOK)
		}
	}

	expectedMethods := []hostapi.Method{
		hostapi.MethodAgentSnapshot,
		hostapi.MethodSessionSnapshot,
		hostapi.MethodSessionReconnect,
		hostapi.MethodSessionDrain,
		hostapi.MethodTunnelList,
		hostapi.MethodDiagnoseSnapshot,
	}
	if len(handler.requests) != len(expectedMethods) {
		testingObject.Fatalf("unexpected request count: got=%d want=%d", len(handler.requests), len(expectedMethods))
	}
	for index, expectedMethod := range expectedMethods {
		if handler.requests[index].Method != expectedMethod {
			testingObject.Fatalf("unexpected method at index=%d: got=%s want=%s", index, handler.requests[index].Method, expectedMethod)
		}
	}
}

func TestEventsStreamEmitsReadyAndSnapshot(testingObject *testing.T) {
	testingObject.Parallel()

	handler := &testHostHandler{
		responses: map[hostapi.Method]any{
			hostapi.MethodAgentSnapshot:        map[string]any{"agent_id": "agent-web"},
			hostapi.MethodSessionSnapshot:      map[string]any{"state": "connected"},
			hostapi.MethodServiceList:          map[string]any{"services": []map[string]any{{"instance_id": "svc-1"}}},
			hostapi.MethodTunnelList:           map[string]any{"tunnels": []map[string]any{{"tunnel_id": "tunnel-1"}}},
			hostapi.MethodTrafficStatsSnapshot: map[string]any{"upload_total_bytes": float64(12)},
			hostapi.MethodDiagnoseSnapshot:     map[string]any{"state": "healthy"},
			hostapi.MethodDiagnoseLogs:         map[string]any{"items": []map[string]any{{"code": "READY"}}},
			hostapi.MethodConfigSnapshot:       map[string]any{"agent_id": "agent-web", "source_path": "/tmp/agent.yaml"},
		},
	}
	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           handler,
		Now: func() time.Time {
			return time.UnixMilli(1711708200000).UTC()
		},
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	cookie := mustLogin(testingObject, server)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	request := httptest.NewRequest(http.MethodGet, "/agent/api/events/stream", nil).WithContext(requestContext)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code: got=%d want=%d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		testingObject.Fatalf("unexpected content type: %q", contentType)
	}

	body := recorder.Body.String()
	assertStreamContains(testingObject, body, "event: agent.ready")
	assertStreamContains(testingObject, body, "event: agent.snapshot")
	assertStreamContains(testingObject, body, `"type":"ready"`)
	assertStreamContains(testingObject, body, `"type":"snapshot"`)
	assertStreamContains(testingObject, body, `"agent":{"agent_id":"agent-web"}`)
	assertStreamContains(testingObject, body, `"session":{"state":"connected"}`)
	assertStreamContains(testingObject, body, `"services":{"services":[{"instance_id":"svc-1"}]}`)
	assertStreamContains(testingObject, body, `"config":{"agent_id":"agent-web","source_path":"/tmp/agent.yaml"}`)

	expectedMethods := []hostapi.Method{
		hostapi.MethodAgentSnapshot,
		hostapi.MethodSessionSnapshot,
		hostapi.MethodServiceList,
		hostapi.MethodTunnelList,
		hostapi.MethodTrafficStatsSnapshot,
		hostapi.MethodDiagnoseSnapshot,
		hostapi.MethodDiagnoseLogs,
		hostapi.MethodConfigSnapshot,
	}
	if len(handler.requests) != len(expectedMethods) {
		testingObject.Fatalf("unexpected request count: got=%d want=%d", len(handler.requests), len(expectedMethods))
	}
	for index, expectedMethod := range expectedMethods {
		if handler.requests[index].Method != expectedMethod {
			testingObject.Fatalf("unexpected method at index=%d: got=%s want=%s", index, handler.requests[index].Method, expectedMethod)
		}
	}
}

func TestUIRequestsRouteToStaticHandler(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BasePath:          "/agent",
		SessionCookieName: "devbridge_agent_session",
		Username:          "admin",
		Password:          "change-me",
		Handler:           &testHostHandler{},
		UIHandler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(writer, request.URL.Path)
		}),
	})
	if err != nil {
		testingObject.Fatalf("new server failed: %v", err)
	}

	testCases := []struct {
		name     string
		path     string
		expected string
		status   int
	}{
		{name: "base path", path: "/agent", expected: "/agent/", status: http.StatusPermanentRedirect},
		{name: "nested path", path: "/agent/services", expected: "/services", status: http.StatusAccepted},
		{name: "asset path", path: "/agent/assets/app.js", expected: "/assets/app.js", status: http.StatusAccepted},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, request)
			if recorder.Code != testCase.status {
				testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, testCase.status)
			}
			if testCase.status == http.StatusPermanentRedirect {
				if location := recorder.Header().Get("Location"); location != testCase.expected {
					testingObject.Fatalf("unexpected redirect: got=%q want=%q", location, testCase.expected)
				}
				return
			}
			if body := recorder.Body.String(); body != testCase.expected {
				testingObject.Fatalf("unexpected body: got=%q want=%q", body, testCase.expected)
			}
		})
	}
}

func mustLogin(testingObject *testing.T, server *Server) *http.Cookie {
	testingObject.Helper()

	request := httptest.NewRequest(
		http.MethodPost,
		"/agent/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"change-me"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("login failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := recorder.Result()
	cookies := response.Cookies()
	if len(cookies) == 0 {
		testingObject.Fatalf("expected cookie after login")
	}
	return cookies[0]
}

func assertStreamContains(testingObject *testing.T, body string, expected string) {
	testingObject.Helper()

	if !strings.Contains(body, expected) {
		testingObject.Fatalf("expected stream to contain %q, body=%s", expected, body)
	}
}
