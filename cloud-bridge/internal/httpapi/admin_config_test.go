package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

type mockConfigEditor struct {
	path      string
	content   string
	loadErr   error
	saveErr   error
	savedBody string
	triggered bool
}

func (m *mockConfigEditor) LoadConfigFile() (string, string, error) {
	return m.path, m.content, m.loadErr
}

func (m *mockConfigEditor) SaveConfigFile(content string) error {
	m.savedBody = content
	return m.saveErr
}

func (m *mockConfigEditor) TriggerHotRestart() {
	m.triggered = true
}

func TestAdminConfigGetAndSave(t *testing.T) {
	editor := &mockConfigEditor{
		path:    "/tmp/bridge.yaml",
		content: "httpAddr: 0.0.0.0:38080\n",
	}
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
	)

	getReq := httptest.NewRequest(http.MethodGet, "http://bridge.internal/api/v1/admin/config/file", nil)
	getResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d", getResp.Code)
	}
	if !strings.Contains(getResp.Body.String(), "/tmp/bridge.yaml") {
		t.Fatalf("unexpected get payload: %s", getResp.Body.String())
	}

	postReq := httptest.NewRequest(http.MethodPost, "http://bridge.internal/api/v1/admin/config/file", strings.NewReader(`{"content":"httpAddr: 0.0.0.0:39080"}`))
	postResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(postResp, postReq)
	if postResp.Code != http.StatusOK {
		t.Fatalf("unexpected post status: %d body=%s", postResp.Code, postResp.Body.String())
	}
	if editor.savedBody == "" {
		t.Fatalf("expected config content saved")
	}
	if !editor.triggered {
		t.Fatalf("expected hot restart triggered")
	}
}

func TestAdminConfigModelGetAndSave(t *testing.T) {
	editor := &mockConfigEditor{
		path: "/tmp/bridge.yaml",
		content: `
httpAddr: 0.0.0.0:38080
discovery:
  backends: [local]
routes:
  - env: base
    serviceName: user
    protocol: http
    host: 127.0.0.1
    port: 8081
`,
	}
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
	)

	getReq := httptest.NewRequest(http.MethodGet, "http://bridge.internal/api/v1/admin/config/model", nil)
	getResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("unexpected get model status: %d body=%s", getResp.Code, getResp.Body.String())
	}
	if !strings.Contains(getResp.Body.String(), `"httpAddr":"0.0.0.0:38080"`) {
		t.Fatalf("unexpected get model payload: %s", getResp.Body.String())
	}

	postReq := httptest.NewRequest(http.MethodPost, "http://bridge.internal/api/v1/admin/config/model", strings.NewReader(`{
  "model": {
    "httpAddr": "0.0.0.0:39080",
    "tunnelSyncProtocols": ["masque"],
    "routeExtractorOrder": ["host","header","sni"],
    "masque": {"addr":"0.0.0.0:39080","tunnelUdpAddr":"127.0.0.1:39081","authMode":"psk","psk":"x"},
    "adminAuth": {"enabled": true, "username": "bridge-admin", "password": "bridge-pass", "realm": "bridge-console"},
    "bridgePublic": {"host":"bridge.example.internal","port":443},
    "ingress": {"fallbackBackflowUrl":"http://127.0.0.1:39090","timeoutSec":10},
    "discovery": {
      "backends":["local"],
      "timeoutMs":2000,
      "localFile":"./bridge-config.yaml",
      "nacos":{"addr":"","namespace":"","group":"DEFAULT_GROUP","servicePattern":"${service}","username":"","password":""},
      "etcd":{"endpoints":[],"keyPrefix":"/devloop/services"},
      "consul":{"addr":"","datacenter":"","servicePattern":"${service}"}
    },
    "routes": [{"env":"base","serviceName":"user","protocol":"http","host":"127.0.0.1","port":8081}]
  }
}`))
	postResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(postResp, postReq)
	if postResp.Code != http.StatusOK {
		t.Fatalf("unexpected post model status: %d body=%s", postResp.Code, postResp.Body.String())
	}
	if !strings.Contains(editor.savedBody, "httpAddr: 0.0.0.0:39080") {
		t.Fatalf("unexpected saved model body: %s", editor.savedBody)
	}
	if !strings.Contains(editor.savedBody, "adminAuth:") || !strings.Contains(editor.savedBody, "username: bridge-admin") {
		t.Fatalf("unexpected admin auth saved body: %s", editor.savedBody)
	}
	if !editor.triggered {
		t.Fatalf("expected hot restart triggered")
	}
}

func TestAdminPageUnavailableWhenEditorMissing(t *testing.T) {
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
	)
	req := httptest.NewRequest(http.MethodGet, "http://bridge.internal/admin", nil)
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: %d", resp.Code)
	}
}

func TestAdminRouteRequireAuthWhenEnabled(t *testing.T) {
	editor := &mockConfigEditor{
		path:    "/tmp/bridge.yaml",
		content: "httpAddr: 0.0.0.0:38080\n",
	}
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
		WithAdminAuth(config.AdminAuthConfig{
			Enabled:  true,
			Username: "admin",
			Password: "admin-pass",
			Realm:    "bridge-console",
		}),
	)

	apiReq := httptest.NewRequest(http.MethodGet, "http://bridge.internal/api/v1/admin/config/model", nil)
	apiResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(apiResp, apiReq)
	if apiResp.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected api status: %d body=%s", apiResp.Code, apiResp.Body.String())
	}
	if !strings.Contains(apiResp.Header().Get("WWW-Authenticate"), "Basic realm=") {
		t.Fatalf("missing WWW-Authenticate header: %s", apiResp.Header().Get("WWW-Authenticate"))
	}

	pageReq := httptest.NewRequest(http.MethodGet, "http://bridge.internal/admin", nil)
	pageResp := httptest.NewRecorder()
	handler.Router().ServeHTTP(pageResp, pageReq)
	if pageResp.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected page status: %d body=%s", pageResp.Code, pageResp.Body.String())
	}
}

func TestAdminRouteAllowWhenAuthPassed(t *testing.T) {
	editor := &mockConfigEditor{
		path:    "/tmp/bridge.yaml",
		content: "httpAddr: 0.0.0.0:38080\n",
	}
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
		WithAdminAuth(config.AdminAuthConfig{
			Enabled:  true,
			Username: "admin",
			Password: "admin-pass",
			Realm:    "bridge-console",
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "http://bridge.internal/api/v1/admin/config/model", nil)
	req.SetBasicAuth("admin", "admin-pass")
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminDiscoveryConnectivityTestNacos(t *testing.T) {
	editor := &mockConfigEditor{
		path:    "/tmp/bridge.yaml",
		content: "httpAddr: 0.0.0.0:38080\n",
	}
	var calledPath string
	nacosServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hosts":[]}`))
	}))
	defer nacosServer.Close()

	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
	)

	req := httptest.NewRequest(http.MethodPost, "http://bridge.internal/api/v1/admin/discovery/test", strings.NewReader(`{
  "backend":"nacos",
  "timeoutMs":1500,
  "discovery":{
    "backends":["nacos"],
    "timeoutMs":1500,
    "nacos":{"addr":"`+nacosServer.URL+`","namespace":"","group":"DEFAULT_GROUP","servicePattern":"${service}","username":"","password":""},
    "etcd":{"endpoints":[],"keyPrefix":"/devloop/services"},
    "consul":{"addr":"","datacenter":"","servicePattern":"${service}"}
  }
}`))
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", resp.Code, resp.Body.String())
	}
	if calledPath != "/nacos/v1/ns/instance/list" {
		t.Fatalf("unexpected nacos request path: %s", calledPath)
	}
	if !strings.Contains(resp.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected response body: %s", resp.Body.String())
	}
}

func TestAdminDiscoveryConnectivityTestLocalNotSupported(t *testing.T) {
	editor := &mockConfigEditor{
		path:    "/tmp/bridge.yaml",
		content: "httpAddr: 0.0.0.0:38080\n",
	}
	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		store.NewMemoryStore(),
		&mockBackflowCaller{},
		"",
		WithConfigEditor(editor),
	)

	req := httptest.NewRequest(http.MethodPost, "http://bridge.internal/api/v1/admin/discovery/test", strings.NewReader(`{
  "backend":"local",
  "discovery":{"backends":["local"]}
}`))
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "不需要远端连通性测试") {
		t.Fatalf("unexpected response body: %s", resp.Body.String())
	}
}
