package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCacheControlForPathPolicy 验证不同类型资源会命中预期缓存策略。
func TestCacheControlForPathPolicy(testingObject *testing.T) {
	testingObject.Parallel()
	testCases := []struct {
		name        string
		filePath    string
		expectValue string
	}{
		{
			name:        "html_no_cache",
			filePath:    "index.html",
			expectValue: cacheControlHTML,
		},
		{
			name:        "hashed_asset_immutable",
			filePath:    "assets/main-ABCDEFGH123.js",
			expectValue: cacheControlImmutable,
		},
		{
			name:        "plain_asset_short_cache",
			filePath:    "assets/logo.svg",
			expectValue: cacheControlDefault,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			if gotValue := cacheControlForPath(testCase.filePath); gotValue != testCase.expectValue {
				testingObject.Fatalf("unexpected cache control: got=%s want=%s", gotValue, testCase.expectValue)
			}
		})
	}
}

// TestHandlerAddsVersionAndCacheHeaders 验证 UI 响应携带版本标识与缓存头。
func TestHandlerAddsVersionAndCacheHeaders(testingObject *testing.T) {
	testingObject.Parallel()
	uiHandler := Handler()
	uiVersion := EmbeddedVersion()
	if uiVersion == "" {
		testingObject.Fatalf("unexpected empty embedded version")
	}

	// 请求入口页时必须返回 no-cache，确保发布后能及时拿到新 index。
	indexRecorder := httptest.NewRecorder()
	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	uiHandler.ServeHTTP(indexRecorder, indexRequest)
	if indexRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected index status: %d", indexRecorder.Code)
	}
	if gotVersion := indexRecorder.Header().Get(HeaderUIVersion); gotVersion != uiVersion {
		testingObject.Fatalf("unexpected version header: got=%s want=%s", gotVersion, uiVersion)
	}
	if gotCacheControl := indexRecorder.Header().Get("Cache-Control"); gotCacheControl != cacheControlHTML {
		testingObject.Fatalf("unexpected index cache control: got=%s want=%s", gotCacheControl, cacheControlHTML)
	}

	hashedAssetPath := findHashedAssetPath(testingObject)
	assetRecorder := httptest.NewRecorder()
	assetRequest := httptest.NewRequest(http.MethodGet, "/"+hashedAssetPath, nil)
	uiHandler.ServeHTTP(assetRecorder, assetRequest)
	if assetRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected asset status: path=%s status=%d", hashedAssetPath, assetRecorder.Code)
	}
	if gotCacheControl := assetRecorder.Header().Get("Cache-Control"); gotCacheControl != cacheControlImmutable {
		testingObject.Fatalf("unexpected asset cache control: got=%s want=%s", gotCacheControl, cacheControlImmutable)
	}
}

// TestHandlerSPAFallback 验证未知前端路由会回落到 index.html。
func TestHandlerSPAFallback(testingObject *testing.T) {
	testingObject.Parallel()
	uiHandler := Handler()
	responseRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/routing/unknown/path", nil)
	uiHandler.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status for spa fallback: %d", responseRecorder.Code)
	}
	if gotCacheControl := responseRecorder.Header().Get("Cache-Control"); gotCacheControl != cacheControlHTML {
		testingObject.Fatalf("unexpected cache control for spa fallback: got=%s want=%s", gotCacheControl, cacheControlHTML)
	}
}

// findHashedAssetPath 从内嵌资源中挑选一个带 hash 的静态文件用于断言 immutable 缓存。
func findHashedAssetPath(testingObject *testing.T) string {
	testingObject.Helper()
	bundle := loadBundle()
	if !bundle.ready {
		testingObject.Fatalf("embedded ui bundle not ready")
	}
	hashedPath := ""
	_ = fs.WalkDir(bundle.staticFS, ".", func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if cacheControlForPath(currentPath) == cacheControlImmutable {
			// 命中首个 hash 资源即可，不需要遍历完整列表。
			hashedPath = currentPath
			return fs.SkipAll
		}
		return nil
	})
	if hashedPath == "" {
		testingObject.Fatalf("no hashed asset found in embedded bundle")
	}
	return hashedPath
}
