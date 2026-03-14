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
