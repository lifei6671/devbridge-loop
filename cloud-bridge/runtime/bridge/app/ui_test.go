package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRegisterAdminUIRoutesRedirectAndStripPrefix 验证 /admin 路由重定向与前缀剥离行为。
func TestRegisterAdminUIRoutesRedirectAndStripPrefix(testingObject *testing.T) {
	testingObject.Parallel()
	mux := http.NewServeMux()
	RegisterAdminUIRoutes(mux, "/admin", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// 回写路径用于断言 StripPrefix 是否生效。
		writer.Header().Set("X-Forwarded-Path", request.URL.Path)
		writer.WriteHeader(http.StatusNoContent)
	}))

	adminRecorder := httptest.NewRecorder()
	adminRequest := httptest.NewRequest(http.MethodGet, "/admin", nil)
	mux.ServeHTTP(adminRecorder, adminRequest)
	if adminRecorder.Code != http.StatusTemporaryRedirect {
		testingObject.Fatalf("unexpected /admin status: %d", adminRecorder.Code)
	}
	if location := adminRecorder.Header().Get("Location"); location != "/admin/" {
		testingObject.Fatalf("unexpected /admin redirect location: %s", location)
	}

	assetRecorder := httptest.NewRecorder()
	assetRequest := httptest.NewRequest(http.MethodGet, "/admin/assets/app.js", nil)
	mux.ServeHTTP(assetRecorder, assetRequest)
	if assetRecorder.Code != http.StatusNoContent {
		testingObject.Fatalf("unexpected /admin/assets status: %d", assetRecorder.Code)
	}
	if forwardedPath := assetRecorder.Header().Get("X-Forwarded-Path"); forwardedPath != "/assets/app.js" {
		testingObject.Fatalf("unexpected forwarded path: %s", forwardedPath)
	}
}

// TestRegisterAdminUIRoutesRootRedirect 验证管理端口根路径会跳转到 /admin/。
func TestRegisterAdminUIRoutesRootRedirect(testingObject *testing.T) {
	testingObject.Parallel()
	mux := http.NewServeMux()
	RegisterAdminUIRoutes(mux, "/admin", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = request
		writer.WriteHeader(http.StatusNoContent)
	}))

	rootRecorder := httptest.NewRecorder()
	rootRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	mux.ServeHTTP(rootRecorder, rootRequest)
	if rootRecorder.Code != http.StatusTemporaryRedirect {
		testingObject.Fatalf("unexpected root status: %d", rootRecorder.Code)
	}
	if location := rootRecorder.Header().Get("Location"); location != "/admin/" {
		testingObject.Fatalf("unexpected root redirect location: %s", location)
	}
}

// TestNormalizeAdminUIBasePath 验证管理路由前缀归一化规则。
func TestNormalizeAdminUIBasePath(testingObject *testing.T) {
	testingObject.Parallel()
	testCases := []struct {
		input string
		want  string
	}{
		{input: "", want: "/admin"},
		{input: "admin", want: "/admin"},
		{input: "/admin/", want: "/admin"},
		{input: "/", want: "/admin"},
	}
	for _, testCase := range testCases {
		// 逐项断言输入是否被归一化到稳定路径。
		if got := normalizeAdminUIBasePath(testCase.input); got != testCase.want {
			testingObject.Fatalf("unexpected normalized path for %q: got=%s want=%s", testCase.input, got, testCase.want)
		}
	}
}
