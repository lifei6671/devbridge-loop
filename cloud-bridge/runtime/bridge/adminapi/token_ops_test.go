package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConnectorTokensListReturnsMetadataOnly(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ListConnectorTokens: func() ([]ConnectorTokenRecord, error) {
				return []ConnectorTokenRecord{
					{
						TokenID:     "agent-local",
						ConnectorID: "agent-local",
						Status:      "active",
						IssuedAtMS:  uint64(time.Date(2026, 3, 29, 21, 0, 0, 0, time.UTC).UnixMilli()),
					},
				}, nil
			},
		},
		AuthProviders:  newAuthProvidersForTest(testAuthAccount{username: "viewer-user", password: "viewer-pass", role: RoleViewer}),
		AllowedOrigins: []string{testAllowedOrigin},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	session := loginAsTestUser(testingObject, mux, "viewer-user", "viewer-pass")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/connector-tokens", nil)
	applyTestSession(request, session)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "token_secret_hash") {
		testingObject.Fatalf("connector token list should not expose token_secret_hash: body=%s", recorder.Body.String())
	}
}

func TestAdminCanCreateConnectorToken(testingObject *testing.T) {
	testingObject.Parallel()

	var receivedRequest ConnectorTokenCreateRequest
	var receivedActor string

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			CreateConnectorToken: func(
				now time.Time,
				request ConnectorTokenCreateRequest,
				actor string,
			) (ConnectorTokenIssueResult, error) {
				receivedRequest = request
				receivedActor = actor
				return ConnectorTokenIssueResult{
					Record: ConnectorTokenRecord{
						TokenID:     "agent-a1b2",
						ConnectorID: request.ConnectorID,
						Status:      "active",
						IssuedAtMS:  uint64(now.UnixMilli()),
					},
					PlainToken: "dbt_agent-a1b2.demo-secret",
				}, nil
			},
		},
		AuthProviders:  newAuthProvidersForTest(testAuthAccount{username: "admin-user", password: "admin-pass", role: RoleAdmin}),
		AllowedOrigins: []string{testAllowedOrigin},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	session := loginAsTestUser(testingObject, mux, "admin-user", "admin-pass")

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/connector-tokens",
		strings.NewReader(`{"connector_id":"agent-demo","metadata":{"purpose":"runtime"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	applyTestSession(request, session)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if receivedRequest.ConnectorID != "agent-demo" {
		testingObject.Fatalf("unexpected create connector_id: got=%s want=agent-demo", receivedRequest.ConnectorID)
	}
	if receivedRequest.Metadata["purpose"] != "runtime" {
		testingObject.Fatalf("unexpected create metadata: got=%v", receivedRequest.Metadata)
	}
	if receivedActor != "admin-user" {
		testingObject.Fatalf("unexpected actor: got=%s want=admin-user", receivedActor)
	}

	var response struct {
		Result ConnectorTokenIssueResult `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode create response failed: %v body=%s", err, recorder.Body.String())
	}
	if response.Result.PlainToken != "dbt_agent-a1b2.demo-secret" {
		testingObject.Fatalf("unexpected plain token: got=%s", response.Result.PlainToken)
	}
}

func TestAdminCanRotateAndRevokeConnectorToken(testingObject *testing.T) {
	testingObject.Parallel()

	var rotatedTokenID string
	var revokedTokenID string

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			RotateConnectorToken: func(now time.Time, tokenID string, actor string) (ConnectorTokenIssueResult, error) {
				rotatedTokenID = tokenID
				return ConnectorTokenIssueResult{
					Record: ConnectorTokenRecord{
						TokenID:     "agent-new",
						ConnectorID: "agent-demo",
						Status:      "active",
						IssuedAtMS:  uint64(now.UnixMilli()),
					},
					PlainToken: "dbt_agent-new.rotate-secret",
				}, nil
			},
			RevokeConnectorToken: func(now time.Time, tokenID string, actor string) (ConnectorTokenRecord, error) {
				revokedTokenID = tokenID
				return ConnectorTokenRecord{
					TokenID:     tokenID,
					ConnectorID: "agent-demo",
					Status:      "revoked",
					IssuedAtMS:  uint64(now.Add(-time.Hour).UnixMilli()),
				}, nil
			},
		},
		AuthProviders:  newAuthProvidersForTest(testAuthAccount{username: "admin-user", password: "admin-pass", role: RoleAdmin}),
		AllowedOrigins: []string{testAllowedOrigin},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	session := loginAsTestUser(testingObject, mux, "admin-user", "admin-pass")

	rotateRecorder := httptest.NewRecorder()
	rotateRequest := httptest.NewRequest(http.MethodPost, "/api/admin/connector-tokens/agent-old/rotate", nil)
	applyTestSession(rotateRequest, session)
	mux.ServeHTTP(rotateRecorder, rotateRequest)
	if rotateRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected rotate status: got=%d want=%d body=%s", rotateRecorder.Code, http.StatusOK, rotateRecorder.Body.String())
	}
	if rotatedTokenID != "agent-old" {
		testingObject.Fatalf("unexpected rotated token id: got=%s want=agent-old", rotatedTokenID)
	}

	revokeRecorder := httptest.NewRecorder()
	revokeRequest := httptest.NewRequest(http.MethodPost, "/api/admin/connector-tokens/agent-new/revoke", nil)
	applyTestSession(revokeRequest, session)
	mux.ServeHTTP(revokeRecorder, revokeRequest)
	if revokeRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected revoke status: got=%d want=%d body=%s", revokeRecorder.Code, http.StatusOK, revokeRecorder.Body.String())
	}
	if revokedTokenID != "agent-new" {
		testingObject.Fatalf("unexpected revoked token id: got=%s want=agent-new", revokedTokenID)
	}
}
