package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNewHTTPServerUsesRuntimeConfig 验证启用 ui.web 后可基于 runtime 配置创建 HTTP API 服务。
func TestNewHTTPServerUsesRuntimeConfig(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.BasePath = "/agent"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"

	runtimeInstance, err := BootstrapWithOptions(context.Background(), config, BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	httpServer, err := newHTTPServer(runtimeInstance)
	if err != nil {
		testingObject.Fatalf("new http server failed: %v", err)
	}
	if httpServer == nil {
		testingObject.Fatalf("expected http server when ui.web.enabled=true")
	}

	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"change-me"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	httpServer.handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected login status: got=%d want=%d", loginRecorder.Code, http.StatusOK)
	}

	uiRedirectRequest := httptest.NewRequest(http.MethodGet, "/agent", nil)
	uiRedirectRecorder := httptest.NewRecorder()
	httpServer.handler.ServeHTTP(uiRedirectRecorder, uiRedirectRequest)
	if uiRedirectRecorder.Code != http.StatusPermanentRedirect {
		testingObject.Fatalf("unexpected ui redirect status: got=%d want=%d", uiRedirectRecorder.Code, http.StatusPermanentRedirect)
	}
	if location := uiRedirectRecorder.Header().Get("Location"); location != "/agent/" {
		testingObject.Fatalf("unexpected redirect location: got=%q want=%q", location, "/agent/")
	}

	uiRequest := httptest.NewRequest(http.MethodGet, "/agent/", nil)
	uiRecorder := httptest.NewRecorder()
	httpServer.handler.ServeHTTP(uiRecorder, uiRequest)
	if uiRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected ui status: got=%d want=%d", uiRecorder.Code, http.StatusOK)
	}
	if contentType := uiRecorder.Header().Get("Content-Type"); contentType == "" {
		testingObject.Fatalf("expected ui content type")
	}
	if versionHeader := uiRecorder.Header().Get("X-Agent-UI-Version"); versionHeader == "" {
		testingObject.Fatalf("expected embedded ui version header")
	}
}

// TestHTTPServerConfigSnapshotRedactsSensitiveSecrets 验证真实 HTTP 路径不会向浏览器回显敏感凭据。
func TestHTTPServerConfigSnapshotRedactsSensitiveSecrets(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.UI.Web.Enabled = true
	config.UI.Web.ListenAddr = "127.0.0.1:39082"
	config.UI.Web.BasePath = "/agent"
	config.UI.Web.Auth.Username = "admin"
	config.UI.Web.Auth.Password = "change-me"
	config.Session.AuthToken = "secret-token"

	runtimeInstance, err := BootstrapWithOptions(context.Background(), config, BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	httpServer, err := newHTTPServer(runtimeInstance)
	if err != nil {
		testingObject.Fatalf("new http server failed: %v", err)
	}

	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/api/login",
		bytes.NewBufferString(`{"username":"admin","password":"change-me"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	httpServer.handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected login status: got=%d want=%d", loginRecorder.Code, http.StatusOK)
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) == 0 {
		testingObject.Fatalf("expected login cookie")
	}

	configRequest := httptest.NewRequest(http.MethodGet, "/agent/api/app/config", nil)
	configRequest.AddCookie(cookies[0])
	configRecorder := httptest.NewRecorder()
	httpServer.handler.ServeHTTP(configRecorder, configRequest)
	if configRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected config status: got=%d want=%d", configRecorder.Code, http.StatusOK)
	}

	var responsePayload struct {
		Config struct {
			Session struct {
				AuthToken string `json:"auth_token"`
			} `json:"session"`
			UI struct {
				Web struct {
					Auth struct {
						Password string `json:"password"`
					} `json:"auth"`
				} `json:"web"`
			} `json:"ui"`
		} `json:"config"`
	}
	if err := json.Unmarshal(configRecorder.Body.Bytes(), &responsePayload); err != nil {
		testingObject.Fatalf("decode config response failed: %v", err)
	}
	if responsePayload.Config.Session.AuthToken != "" {
		testingObject.Fatalf("expected redacted auth_token, got=%q", responsePayload.Config.Session.AuthToken)
	}
	if responsePayload.Config.UI.Web.Auth.Password != "" {
		testingObject.Fatalf("expected redacted ui.web.auth.password, got=%q", responsePayload.Config.UI.Web.Auth.Password)
	}
}
