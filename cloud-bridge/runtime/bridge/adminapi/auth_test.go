package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthLoginUsesConfiguredProviderOrderWhenProviderOmitted(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{},
		AuthProviders: []AuthProviderConfig{
			{
				Name:    "z-local",
				Type:    "password",
				Label:   "Z Local",
				Enabled: true,
				Password: PasswordProviderConfig{
					Accounts: []PasswordAccountConfig{
						{
							Username: "shared-user",
							Password: "shared-pass",
							Role:     RoleViewer,
						},
					},
				},
			},
			{
				Name:    "a-local",
				Type:    "password",
				Label:   "A Local",
				Enabled: true,
				Password: PasswordProviderConfig{
					Accounts: []PasswordAccountConfig{
						{
							Username: "shared-user",
							Password: "shared-pass",
							Role:     RoleAdmin,
						},
					},
				},
			},
		},
		AllowedOrigins: []string{testAllowedOrigin},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	payload, err := json.Marshal(map[string]string{
		"username": "shared-user",
		"password": "shared-pass",
	})
	if err != nil {
		testingObject.Fatalf("marshal login payload failed: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testAllowedOrigin)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response struct {
		Session struct {
			Provider string `json:"provider"`
		} `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode login response failed: %v body=%s", err, recorder.Body.String())
	}
	if response.Session.Provider != "z-local" {
		testingObject.Fatalf("unexpected provider: got=%s want=%s", response.Session.Provider, "z-local")
	}
}

func TestAuthLoginFallsBackToNextConfiguredProviderWhenProviderOmitted(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{},
		AuthProviders: []AuthProviderConfig{
			{
				Name:    "first-provider",
				Type:    "password",
				Label:   "First",
				Enabled: true,
				Password: PasswordProviderConfig{
					Accounts: []PasswordAccountConfig{
						{
							Username: "shared-user",
							Password: "wrong-pass",
							Role:     RoleViewer,
						},
					},
				},
			},
			{
				Name:    "second-provider",
				Type:    "password",
				Label:   "Second",
				Enabled: true,
				Password: PasswordProviderConfig{
					Accounts: []PasswordAccountConfig{
						{
							Username: "shared-user",
							Password: "correct-pass",
							Role:     RoleOperator,
						},
					},
				},
			},
		},
		AllowedOrigins: []string{testAllowedOrigin},
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	payload, err := json.Marshal(map[string]string{
		"username": "shared-user",
		"password": "correct-pass",
	})
	if err != nil {
		testingObject.Fatalf("marshal login payload failed: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testAllowedOrigin)
	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response struct {
		Session struct {
			Provider string `json:"provider"`
		} `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode login response failed: %v body=%s", err, recorder.Body.String())
	}
	if response.Session.Provider != "second-provider" {
		testingObject.Fatalf("unexpected provider: got=%s want=%s", response.Session.Provider, "second-provider")
	}
}
