package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestDefaultConfigAdminEnabledByDefault 验证默认配置下管理面总开关默认开启。
func TestDefaultConfigAdminEnabledByDefault(testingObject *testing.T) {
	testingObject.Parallel()

	defaultConfig := DefaultConfig()
	if !defaultConfig.Admin.Enabled {
		testingObject.Fatalf("expected admin.enabled default true")
	}
}

// TestConfigValidateAllowsEmptyAdminAddrWhenDisabled
// 验证 admin 关闭时允许监听地址为空，避免无意义校验阻断主链路。
func TestConfigValidateAllowsEmptyAdminAddrWhenDisabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = false
	config.Admin.ListenAddr = ""
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass when admin disabled: %v", err)
	}
}

// TestConfigValidateRejectsEmptyAdminAddrWhenEnabled
// 验证 admin 开启时必须显式配置监听地址。
func TestConfigValidateRejectsEmptyAdminAddrWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.ListenAddr = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin enabled and listen addr empty")
	}
}

// TestConfigValidateRejectsSharedAdminListenerByDefault
// 验证默认网络隔离策略下，admin 监听地址不能与控制面地址复用。
func TestConfigValidateRejectsSharedAdminListenerByDefault(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AllowSharedListener = false
	config.Admin.ListenAddr = config.ControlPlane.ListenAddr
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin listener shares control plane addr")
	}
}

// TestConfigValidateAllowsSharedAdminListenerWhenEnabled
// 验证显式打开 allow_shared_listener 后可允许复用监听地址（兼容特殊部署环境）。
func TestConfigValidateAllowsSharedAdminListenerWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AllowSharedListener = true
	config.Admin.ListenAddr = config.ControlPlane.ListenAddr
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass when shared listener is explicitly enabled: %v", err)
	}
}

// TestConfigValidateRejectsEquivalentSharedAdminListenerByDefault
// 验证等价监听地址（:port vs 0.0.0.0:port）也会被识别为冲突。
func TestConfigValidateRejectsEquivalentSharedAdminListenerByDefault(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AllowSharedListener = false
	config.Admin.ListenAddr = ":39081"
	config.ControlPlane.ListenAddr = "0.0.0.0:39081"
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail for equivalent shared listener addresses")
	}
}

// TestConfigValidateRejectsCookieAuthWithoutAllowedOrigins
// 验证 cookie 鉴权模式必须显式配置允许来源，避免写接口暴露于 CSRF 风险。
func TestConfigValidateRejectsCookieAuthWithoutAllowedOrigins(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthMode = "cookie"
	config.Admin.AllowedOrigins = nil
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when cookie auth mode has no allowed origins")
	}
}

// TestConfigValidateAllowsCookieAuthWithCSRFConfig
// 验证 cookie 鉴权模式在 CSRF 相关字段齐备时可通过配置校验。
func TestConfigValidateAllowsCookieAuthWithCSRFConfig(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthMode = "cookie"
	config.Admin.CookieTokenName = "bridge_admin_token"
	config.Admin.CSRFCookieName = "bridge_admin_csrf"
	config.Admin.CSRFHeaderName = "X-CSRF-Token"
	config.Admin.AllowedOrigins = []string{"http://127.0.0.1:39081"}
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass for cookie auth mode with csrf config: %v", err)
	}
}

// TestBootstrapSkipsAdminServerWhenDisabled 验证管理面关闭时不会初始化 admin server。
func TestBootstrapSkipsAdminServerWhenDisabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = false
	config.Admin.UIEnabled = true

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer != nil {
		testingObject.Fatalf("expected admin server nil when admin disabled")
	}
}

// TestBootstrapInitializesAdminServerWhenEnabled 验证管理面开启时会初始化 admin server。
func TestBootstrapInitializesAdminServerWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized when admin enabled")
	}
}

// TestConfigValidateRejectsEmptyAdminAuthTokensWhenEnabled
// 验证 admin 开启时必须配置 Bearer Token，避免无鉴权暴露管理接口。
func TestConfigValidateRejectsEmptyAdminAuthTokensWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthTokens = nil
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin enabled and auth tokens empty")
	}
}

// TestConfigValidateRejectsInvalidAdminAuthRole
// 验证 admin token 角色非法时会被配置校验拦截。
func TestConfigValidateRejectsInvalidAdminAuthRole(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthTokens = []AdminAuthTokenConfig{
		{
			Name:  "invalid",
			Token: "invalid-token",
			Role:  "super_admin",
		},
	}
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin auth role is unsupported")
	}
}

// TestBootstrapRegistersAdminAPIRoutesWhenEnabled
// 验证管理面开启后会注册 /api/admin/* 路由，并强制 Bearer 鉴权。
func TestBootstrapRegistersAdminAPIRoutesWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized when admin enabled")
	}

	unauthorizedRecorder := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	runtime.adminServer.Handler.ServeHTTP(unauthorizedRecorder, unauthorizedRequest)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		testingObject.Fatalf("unexpected unauthorized status: got=%d want=%d", unauthorizedRecorder.Code, http.StatusUnauthorized)
	}

	authorizedRecorder := httptest.NewRecorder()
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer devbridge-viewer-token")
	runtime.adminServer.Handler.ServeHTTP(authorizedRecorder, authorizedRequest)
	if authorizedRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected authorized status: got=%d want=%d body=%s", authorizedRecorder.Code, http.StatusOK, authorizedRecorder.Body.String())
	}
}

// TestBootstrapServesAdminUIAndAPIOnSingleServer
// 验证单实例 admin server 同时承载 UI 路由与 API 读写路由，覆盖 BMA-16 联调主路径。
func TestBootstrapServesAdminUIAndAPIOnSingleServer(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = true
	config.Admin.BasePath = "/console"

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	// 先验证 UI 路由可达，确保内嵌静态资源与前缀挂载生效。
	uiRecorder := httptest.NewRecorder()
	uiRequest := httptest.NewRequest(http.MethodGet, "/console/", nil)
	runtime.adminServer.Handler.ServeHTTP(uiRecorder, uiRequest)
	if uiRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected ui status: got=%d want=%d", uiRecorder.Code, http.StatusOK)
	}
	if !strings.Contains(strings.ToLower(uiRecorder.Body.String()), "<!doctype html>") {
		testingObject.Fatalf("expected ui response to be html document")
	}

	// 再验证同一 server 上 API 读接口可访问。
	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	overviewRequest.Header.Set("Authorization", "Bearer devbridge-viewer-token")
	runtime.adminServer.Handler.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected overview status: got=%d want=%d body=%s", overviewRecorder.Code, http.StatusOK, overviewRecorder.Body.String())
	}

	// 最后验证受控写接口可执行，覆盖“单二进制读写链路”关键验收点。
	reloadRecorder := httptest.NewRecorder()
	reloadRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	reloadRequest.Header.Set("Authorization", "Bearer devbridge-operator-token")
	runtime.adminServer.Handler.ServeHTTP(reloadRecorder, reloadRequest)
	if reloadRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected reload status: got=%d want=%d body=%s", reloadRecorder.Code, http.StatusOK, reloadRecorder.Body.String())
	}

	var reloadResponse struct {
		Result struct {
			ConfigVersion uint64 `json:"config_version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reloadRecorder.Body.Bytes(), &reloadResponse); err != nil {
		testingObject.Fatalf("decode reload response failed: %v", err)
	}
	if reloadResponse.Result.ConfigVersion == 0 {
		testingObject.Fatalf("unexpected reload config version: got=%d", reloadResponse.Result.ConfigVersion)
	}
}

// TestBootstrapServesAdminSSEOnSingleServer
// 验证单实例 admin server 可输出 SSE ready/snapshot 事件，覆盖实时联调主路径。
func TestBootstrapServesAdminSSEOnSingleServer(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = true
	config.Admin.BasePath = "/console"

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	// SSE 是长连接，需要用可取消上下文结束请求，避免测试阻塞。
	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/events/stream?topics=dashboard&interval_ms=1000&access_token=devbridge-viewer-token",
		nil,
	).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	serveDoneChannel := make(chan struct{})
	go func() {
		runtime.adminServer.Handler.ServeHTTP(recorder, request)
		close(serveDoneChannel)
	}()

	// 给 handler 留出 ready/snapshot 首帧输出窗口，再主动取消请求。
	time.Sleep(50 * time.Millisecond)
	cancelRequest()
	select {
	case <-serveDoneChannel:
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("events stream request did not exit after context cancel")
	}

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected sse status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	responseBody := recorder.Body.String()
	if !strings.Contains(responseBody, "event: bridge.ready") {
		testingObject.Fatalf("missing bridge.ready event: %s", responseBody)
	}
	if !strings.Contains(responseBody, "event: bridge.snapshot") {
		testingObject.Fatalf("missing bridge.snapshot event: %s", responseBody)
	}
	if !strings.Contains(responseBody, `"topic":"dashboard"`) {
		testingObject.Fatalf("missing dashboard topic payload: %s", responseBody)
	}
}

// TestAdminSessionDrainEndpointAppliesLifecycleEffects
// 验证 session drain 接口会驱动 session/service/tunnel 运行态同步收敛。
func TestAdminSessionDrainEndpointAppliesLifecycleEffects(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil || runtime.dataPlane == nil {
		testingObject.Fatalf("expected runtime admin server and data plane initialized")
	}

	now := time.Unix(1700000000, 0).UTC()
	runtime.dataPlane.sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:     "session-1",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	runtime.dataPlane.serviceRegistry.Upsert(now, pb.Service{
		ServiceID:    "service-1",
		ConnectorID:  "connector-1",
		Status:       pb.ServiceStatusActive,
		HealthStatus: pb.HealthStatusHealthy,
	})
	if _, err := runtime.dataPlane.tunnelRegistry.UpsertIdle(
		now,
		"connector-1",
		"session-1",
		&bootstrapTestTunnel{id: "tunnel-1"},
	); err != nil {
		testingObject.Fatalf("upsert tunnel failed: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/ops/session/session-1/drain",
		strings.NewReader(`{"reason":"manual_drain"}`),
	)
	request.Header.Set("Authorization", "Bearer devbridge-operator-token")
	request.Header.Set("Content-Type", "application/json")
	runtime.adminServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	sessionSnapshot, exists := runtime.dataPlane.sessionRegistry.GetBySession("session-1")
	if !exists {
		testingObject.Fatalf("expected session still exists")
	}
	if sessionSnapshot.State != registry.SessionDraining {
		testingObject.Fatalf("unexpected session state: got=%s want=%s", sessionSnapshot.State, registry.SessionDraining)
	}

	serviceSnapshot, exists := runtime.dataPlane.serviceRegistry.GetByServiceID("service-1")
	if !exists {
		testingObject.Fatalf("expected service exists")
	}
	if serviceSnapshot.Status != pb.ServiceStatusInactive {
		testingObject.Fatalf("unexpected service status: got=%s want=%s", serviceSnapshot.Status, pb.ServiceStatusInactive)
	}
	if _, exists := runtime.dataPlane.tunnelRegistry.Get("tunnel-1"); exists {
		testingObject.Fatalf("expected tunnel purged after drain")
	}
}

// TestAdminConfigUpdateEnforcesIfMatchVersion
// 验证配置写接口要求 if_match_version，并在版本冲突时返回 409。
func TestAdminConfigUpdateEnforcesIfMatchVersion(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`{"if_match_version":1,"patch":{"observability.log_level":"debug"}}`),
	)
	updateRequest.Header.Set("Authorization", "Bearer devbridge-admin-token")
	updateRequest.Header.Set("Content-Type", "application/json")
	runtime.adminServer.Handler.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected update status: got=%d want=%d body=%s", updateRecorder.Code, http.StatusOK, updateRecorder.Body.String())
	}

	var updateResponse struct {
		Result struct {
			ConfigVersion uint64 `json:"config_version"`
		} `json:"result"`
	}
	if err := json.Unmarshal(updateRecorder.Body.Bytes(), &updateResponse); err != nil {
		testingObject.Fatalf("decode update response failed: %v", err)
	}
	if updateResponse.Result.ConfigVersion != 2 {
		testingObject.Fatalf("unexpected config version: got=%d want=%d", updateResponse.Result.ConfigVersion, 2)
	}

	conflictRecorder := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`{"if_match_version":1,"patch":{"observability.log_level":"warn"}}`),
	)
	conflictRequest.Header.Set("Authorization", "Bearer devbridge-admin-token")
	conflictRequest.Header.Set("Content-Type", "application/json")
	runtime.adminServer.Handler.ServeHTTP(conflictRecorder, conflictRequest)
	if conflictRecorder.Code != http.StatusConflict {
		testingObject.Fatalf("unexpected conflict status: got=%d want=%d body=%s", conflictRecorder.Code, http.StatusConflict, conflictRecorder.Body.String())
	}
}

// TestAdminDiagnoseExportDownloadLifecycleOnSingleServer
// 验证单实例 admin server 下导出/下载链路可用，且下载令牌遵循一次性消费语义。
func TestAdminDiagnoseExportDownloadLifecycleOnSingleServer(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/diagnose/export", nil)
	exportRequest.Header.Set("Authorization", "Bearer devbridge-admin-token")
	runtime.adminServer.Handler.ServeHTTP(exportRecorder, exportRequest)
	if exportRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected export status: got=%d want=%d body=%s", exportRecorder.Code, http.StatusOK, exportRecorder.Body.String())
	}

	var exportResponse struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(exportRecorder.Body.Bytes(), &exportResponse); err != nil {
		testingObject.Fatalf("decode export response failed: %v", err)
	}
	if strings.TrimSpace(exportResponse.DownloadURL) == "" {
		testingObject.Fatalf("expected non-empty download_url")
	}

	downloadRecorder := httptest.NewRecorder()
	downloadRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	downloadRequest.Header.Set("Authorization", "Bearer devbridge-admin-token")
	runtime.adminServer.Handler.ServeHTTP(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected download status: got=%d want=%d body=%s", downloadRecorder.Code, http.StatusOK, downloadRecorder.Body.String())
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	replayRequest.Header.Set("Authorization", "Bearer devbridge-admin-token")
	runtime.adminServer.Handler.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected replay status: got=%d want=%d body=%s", replayRecorder.Code, http.StatusNotFound, replayRecorder.Body.String())
	}
}

// TestBootstrapCookieAuthWriteRequiresCSRFOnSingleServer
// 验证 cookie 鉴权模式在单实例 server 下生效，且写请求强制 CSRF 与 Origin 校验。
func TestBootstrapCookieAuthWriteRequiresCSRFOnSingleServer(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false
	config.Admin.AuthMode = "cookie"
	config.Admin.CookieTokenName = "bridge_admin_token"
	config.Admin.CSRFCookieName = "bridge_admin_csrf"
	config.Admin.CSRFHeaderName = "X-CSRF-Token"
	config.Admin.AllowedOrigins = []string{"http://127.0.0.1:39081"}

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	// 读接口仅依赖鉴权，不要求 CSRF。
	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	overviewRequest.AddCookie(&http.Cookie{
		Name:  config.Admin.CookieTokenName,
		Value: "devbridge-viewer-token",
	})
	runtime.adminServer.Handler.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected overview status in cookie auth mode: got=%d want=%d body=%s", overviewRecorder.Code, http.StatusOK, overviewRecorder.Body.String())
	}

	// 写接口缺失 CSRF Header 时应拒绝。
	forbiddenRecorder := httptest.NewRecorder()
	forbiddenRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	forbiddenRequest.AddCookie(&http.Cookie{
		Name:  config.Admin.CookieTokenName,
		Value: "devbridge-operator-token",
	})
	forbiddenRequest.AddCookie(&http.Cookie{
		Name:  config.Admin.CSRFCookieName,
		Value: "csrf-token",
	})
	forbiddenRequest.Header.Set("Origin", "http://127.0.0.1:39081")
	runtime.adminServer.Handler.ServeHTTP(forbiddenRecorder, forbiddenRequest)
	if forbiddenRecorder.Code != http.StatusForbidden {
		testingObject.Fatalf("unexpected forbidden status without csrf header: got=%d want=%d body=%s", forbiddenRecorder.Code, http.StatusForbidden, forbiddenRecorder.Body.String())
	}

	// 带齐 CSRF Header + Cookie + 允许来源后，写接口应通过。
	successRecorder := httptest.NewRecorder()
	successRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	successRequest.AddCookie(&http.Cookie{
		Name:  config.Admin.CookieTokenName,
		Value: "devbridge-operator-token",
	})
	successRequest.AddCookie(&http.Cookie{
		Name:  config.Admin.CSRFCookieName,
		Value: "csrf-token-2",
	})
	successRequest.Header.Set("Origin", "http://127.0.0.1:39081")
	successRequest.Header.Set(config.Admin.CSRFHeaderName, "csrf-token-2")
	runtime.adminServer.Handler.ServeHTTP(successRecorder, successRequest)
	if successRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected success status with csrf headers: got=%d want=%d body=%s", successRecorder.Code, http.StatusOK, successRecorder.Body.String())
	}
}

type bootstrapTestTunnel struct {
	id string
}

func (tunnel *bootstrapTestTunnel) ID() string {
	return tunnel.id
}

func (tunnel *bootstrapTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	_ = tunnel
	_ = ctx
	return pb.StreamPayload{}, io.EOF
}

func (tunnel *bootstrapTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	_ = tunnel
	_ = ctx
	_ = payload
	return nil
}

func (tunnel *bootstrapTestTunnel) Close() error {
	_ = tunnel
	return nil
}
