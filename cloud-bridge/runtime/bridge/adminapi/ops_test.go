package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOperatorCanDrainSession 验证 operator 角色可调用 session drain 写接口。
func TestOperatorCanDrainSession(testingObject *testing.T) {
	testingObject.Parallel()

	var callbackCalled bool
	var receivedSessionID string
	var receivedReason string
	var receivedActor string

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			DrainSession: func(now time.Time, sessionID string, reason string, actor string) (DrainResult, error) {
				callbackCalled = true
				receivedSessionID = sessionID
				receivedReason = reason
				receivedActor = actor
				return DrainResult{
					SessionID:   sessionID,
					ConnectorID: "connector-1",
					Result:      "drained",
				}, nil
			},
		},
		BearerTokens: []BearerToken{
			{Name: "operator-user", Token: "operator-token", Role: RoleOperator},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/ops/session/session-1/drain",
		strings.NewReader(`{"reason":"manual_drain"}`),
	)
	request.Header.Set("Authorization", "Bearer operator-token")
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !callbackCalled {
		testingObject.Fatalf("expected drain callback called")
	}
	if receivedSessionID != "session-1" {
		testingObject.Fatalf("unexpected session id: got=%s want=session-1", receivedSessionID)
	}
	if receivedReason != "manual_drain" {
		testingObject.Fatalf("unexpected reason: got=%s want=manual_drain", receivedReason)
	}
	if receivedActor != "operator-user" {
		testingObject.Fatalf("unexpected actor: got=%s want=operator-user", receivedActor)
	}
}

// TestViewerCannotDrainSession 验证 viewer 不能访问 session drain 写接口。
func TestViewerCannotDrainSession(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			DrainSession: func(now time.Time, sessionID string, reason string, actor string) (DrainResult, error) {
				testingObject.Fatalf("viewer request should not reach drain callback")
				return DrainResult{}, nil
			},
		},
		BearerTokens: []BearerToken{
			{Name: "viewer-user", Token: "viewer-token", Role: RoleViewer},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/ops/session/session-1/drain",
		strings.NewReader(`{"reason":"manual_drain"}`),
	)
	request.Header.Set("Authorization", "Bearer viewer-token")
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusForbidden)
	}
}

// TestConfigUpdateReturnsConflict 验证配置版本冲突会返回 409。
func TestConfigUpdateReturnsConflict(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			UpdateConfig: func(now time.Time, request ConfigUpdateRequest, actor string) (ConfigUpdateResult, error) {
				return ConfigUpdateResult{}, fmt.Errorf("%w: stale version", ErrAdminVersionConflict)
			},
		},
		BearerTokens: []BearerToken{
			{Name: "admin-user", Token: "admin-token", Role: RoleAdmin},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`{"if_match_version":1,"patch":{"observability.log_level":"debug"}}`),
	)
	request.Header.Set("Authorization", "Bearer admin-token")
	request.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

// TestConfigUpdateAcceptsYAMLBody 验证配置更新接口支持 YAML 请求体，并把嵌套 patch 展平成稳定 key。
func TestConfigUpdateAcceptsYAMLBody(testingObject *testing.T) {
	testingObject.Parallel()

	var callbackCalled bool
	var receivedRequest ConfigUpdateRequest
	var receivedActor string

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			UpdateConfig: func(now time.Time, request ConfigUpdateRequest, actor string) (ConfigUpdateResult, error) {
				callbackCalled = true
				receivedRequest = request
				receivedActor = actor
				return ConfigUpdateResult{
					ConfigVersion: 2,
					Snapshot: map[string]any{
						"config_version": 2,
					},
					ApplyMode:       "staged_requires_restart",
					RequiresRestart: true,
				}, nil
			},
		},
		BearerTokens: []BearerToken{
			{Name: "admin-user", Token: "admin-token", Role: RoleAdmin},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`if_match_version: 1
patch:
  observability:
    log_level: debug
  admin:
    enabled: true
`),
	)
	request.Header.Set("Authorization", "Bearer admin-token")
	request.Header.Set("Content-Type", "application/yaml")
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if callbackCalled {
	} else {
		testingObject.Fatalf("expected update callback called")
	}
	if receivedRequest.IfMatchVersion != 1 {
		testingObject.Fatalf("unexpected if_match_version: got=%d want=1", receivedRequest.IfMatchVersion)
	}
	if receivedRequest.Patch["observability.log_level"] == "debug" {
	} else {
		testingObject.Fatalf("unexpected flattened observability.log_level: got=%v", receivedRequest.Patch["observability.log_level"])
	}
	if receivedRequest.Patch["admin.enabled"] == true {
	} else {
		testingObject.Fatalf("unexpected flattened admin.enabled: got=%v", receivedRequest.Patch["admin.enabled"])
	}
	if receivedActor != "admin-user" {
		testingObject.Fatalf("unexpected actor: got=%s want=admin-user", receivedActor)
	}
}

// TestDiagnoseExportDownloadMasksSensitiveFields 验证导出链路会脱敏敏感字段。
func TestDiagnoseExportDownloadMasksSensitiveFields(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			BuildConfigSnapshot: func() map[string]any {
				return map[string]any{
					"admin": map[string]any{
						"auth_tokens": []map[string]any{
							{
								"name":  "admin",
								"token": "plain-secret-token",
							},
						},
					},
					"upstream_dsn": "postgres://demo:password123@127.0.0.1:5432/devbridge",
				}
			},
		},
		BearerTokens: []BearerToken{
			{Name: "admin-user", Token: "admin-token", Role: RoleAdmin},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/diagnose/export", nil)
	exportRequest.Header.Set("Authorization", "Bearer admin-token")
	mux.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", exportRecorder.Code, http.StatusOK, exportRecorder.Body.String())
	}

	var exportResponse struct {
		DownloadURL  string   `json:"download_url"`
		MaskedFields []string `json:"masked_fields"`
	}
	if err := json.Unmarshal(exportRecorder.Body.Bytes(), &exportResponse); err != nil {
		testingObject.Fatalf("decode export response failed: %v", err)
	}
	if strings.TrimSpace(exportResponse.DownloadURL) == "" {
		testingObject.Fatalf("expected non-empty download_url")
	}

	downloadRecorder := httptest.NewRecorder()
	downloadRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	downloadRequest.Header.Set("Authorization", "Bearer admin-token")
	mux.ServeHTTP(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected download status: got=%d want=%d body=%s", downloadRecorder.Code, http.StatusOK, downloadRecorder.Body.String())
	}
	if strings.Contains(downloadRecorder.Body.String(), "plain-secret-token") {
		testingObject.Fatalf("expected token to be masked in exported payload")
	}
	if strings.Contains(downloadRecorder.Body.String(), "password123@") {
		testingObject.Fatalf("expected dsn credential to be masked in exported payload")
	}
}

// TestCookieAuthWriteRequiresValidCSRF 验证 cookie 鉴权模式下写接口会强制 CSRF 与 Origin 校验。
func TestCookieAuthWriteRequiresValidCSRF(testingObject *testing.T) {
	testingObject.Parallel()

	var reloadCalled bool
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ReloadConfig: func(now time.Time, actor string) (ReloadConfigResult, error) {
				reloadCalled = true
				return ReloadConfigResult{
					ConfigVersion: 2,
					ReloadedAtMS:  uint64(now.UnixMilli()),
				}, nil
			},
		},
		BearerTokens: []BearerToken{
			{Name: "operator-user", Token: "cookie-operator-token", Role: RoleOperator},
		},
		AuthMode:        "cookie",
		CookieTokenName: "bridge_admin_token",
		CSRFCookieName:  "bridge_admin_csrf",
		CSRFHeaderName:  "X-CSRF-Token",
		AllowedOrigins:  []string{"http://127.0.0.1:39081"},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	invalidRecorder := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	invalidRequest.AddCookie(&http.Cookie{Name: "bridge_admin_token", Value: "cookie-operator-token"})
	invalidRequest.AddCookie(&http.Cookie{Name: "bridge_admin_csrf", Value: "csrf-token-1"})
	invalidRequest.Header.Set("Origin", "http://127.0.0.1:39081")
	// 缺失 CSRF Header，应被拒绝。
	mux.ServeHTTP(invalidRecorder, invalidRequest)
	if invalidRecorder.Code != http.StatusForbidden {
		testingObject.Fatalf("unexpected status for missing csrf header: got=%d want=%d", invalidRecorder.Code, http.StatusForbidden)
	}
	if reloadCalled {
		testingObject.Fatalf("reload callback should not be called when csrf validation fails")
	}

	validRecorder := httptest.NewRecorder()
	validRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	validRequest.AddCookie(&http.Cookie{Name: "bridge_admin_token", Value: "cookie-operator-token"})
	validRequest.AddCookie(&http.Cookie{Name: "bridge_admin_csrf", Value: "csrf-token-2"})
	validRequest.Header.Set("Origin", "http://127.0.0.1:39081")
	validRequest.Header.Set("X-CSRF-Token", "csrf-token-2")
	mux.ServeHTTP(validRecorder, validRequest)
	if validRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status for valid csrf request: got=%d want=%d body=%s", validRecorder.Code, http.StatusOK, validRecorder.Body.String())
	}
	if !reloadCalled {
		testingObject.Fatalf("expected reload callback to be called")
	}
}

// TestDiagnoseExportDownloadEnforcesIssuerAndOneTimeUse 验证导出下载受“发起人绑定 + 一次性令牌”约束。
func TestDiagnoseExportDownloadEnforcesIssuerAndOneTimeUse(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		BearerTokens: []BearerToken{
			{Name: "admin-a", Token: "admin-a-token", Role: RoleAdmin},
			{Name: "admin-b", Token: "admin-b-token", Role: RoleAdmin},
		},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/diagnose/export", nil)
	exportRequest.Header.Set("Authorization", "Bearer admin-a-token")
	mux.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected export status: got=%d want=%d body=%s", exportRecorder.Code, http.StatusOK, exportRecorder.Body.String())
	}
	var exportResponse struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(exportRecorder.Body.Bytes(), &exportResponse); err != nil {
		testingObject.Fatalf("decode export response failed: %v", err)
	}

	forbiddenRecorder := httptest.NewRecorder()
	forbiddenRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	forbiddenRequest.Header.Set("Authorization", "Bearer admin-b-token")
	mux.ServeHTTP(forbiddenRecorder, forbiddenRequest)
	if forbiddenRecorder.Code != http.StatusForbidden {
		testingObject.Fatalf("unexpected download status for wrong actor: got=%d want=%d body=%s", forbiddenRecorder.Code, http.StatusForbidden, forbiddenRecorder.Body.String())
	}

	successRecorder := httptest.NewRecorder()
	successRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	successRequest.Header.Set("Authorization", "Bearer admin-a-token")
	mux.ServeHTTP(successRecorder, successRequest)
	if successRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected download status for issuer: got=%d want=%d body=%s", successRecorder.Code, http.StatusOK, successRecorder.Body.String())
	}
	if strings.TrimSpace(successRecorder.Header().Get("Cache-Control")) != "no-store" {
		testingObject.Fatalf("expected no-store cache control header")
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	replayRequest.Header.Set("Authorization", "Bearer admin-a-token")
	mux.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected replay status: got=%d want=%d body=%s", replayRecorder.Code, http.StatusNotFound, replayRecorder.Body.String())
	}
}
