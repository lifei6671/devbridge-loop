package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testAllowedOrigin = "http://127.0.0.1:39080"

type testAuthAccount struct {
	username string
	password string
	role     Role
}

type testAuthSession struct {
	cookies   []*http.Cookie
	csrfToken string
}

func newAuthProvidersForTest(accounts ...testAuthAccount) []AuthProviderConfig {
	providerAccounts := make([]PasswordAccountConfig, 0, len(accounts))
	for _, account := range accounts {
		providerAccounts = append(providerAccounts, PasswordAccountConfig{
			Username: account.username,
			Password: account.password,
			Role:     account.role,
		})
	}
	return []AuthProviderConfig{
		{
			Name:    "local-password",
			Type:    "password",
			Label:   "本地账号",
			Enabled: true,
			Password: PasswordProviderConfig{
				Accounts: providerAccounts,
			},
		},
	}
}

func loginAsTestUser(testingObject *testing.T, handler http.Handler, username string, password string) testAuthSession {
	testingObject.Helper()
	payload, err := json.Marshal(map[string]string{
		"provider": "local-password",
		"username": username,
		"password": password,
	})
	if err != nil {
		testingObject.Fatalf("marshal login payload failed: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testAllowedOrigin)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("login failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Session struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode login response failed: %v body=%s", err, recorder.Body.String())
	}
	return testAuthSession{
		cookies:   recorder.Result().Cookies(),
		csrfToken: response.Session.CSRFToken,
	}
}

func applyTestSession(request *http.Request, session testAuthSession) {
	for _, cookie := range session.cookies {
		request.AddCookie(cookie)
	}
	if request != nil && request.Method != http.MethodGet && session.csrfToken != "" {
		request.Header.Set("Origin", testAllowedOrigin)
		request.Header.Set("X-CSRF-Token", session.csrfToken)
	}
}
