package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	apptls "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/tls"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const bootstrapTestAllowedOrigin = "http://127.0.0.1:39081"

type bootstrapAuthSession struct {
	cookies   []*http.Cookie
	csrfToken string
}

func loginBootstrapUser(testingObject *testing.T, handler http.Handler, username string, password string) bootstrapAuthSession {
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
	request.Header.Set("Origin", bootstrapTestAllowedOrigin)
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
	return bootstrapAuthSession{
		cookies:   recorder.Result().Cookies(),
		csrfToken: response.Session.CSRFToken,
	}
}

func applyBootstrapSession(request *http.Request, session bootstrapAuthSession) {
	for _, cookie := range session.cookies {
		request.AddCookie(cookie)
	}
	if request != nil && request.Method != http.MethodGet && session.csrfToken != "" {
		request.Header.Set("Origin", bootstrapTestAllowedOrigin)
		request.Header.Set("X-CSRF-Token", session.csrfToken)
	}
}

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

// TestConfigValidateRejectsMissingAllowedOrigins
// 验证浏览器登录会话模式必须显式配置允许来源，避免登录与写接口暴露于 CSRF 风险。
func TestConfigValidateRejectsMissingAllowedOrigins(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AllowedOrigins = nil
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when no allowed origins are configured")
	}
}

// TestConfigValidateAllowsBrowserSessionAuthConfig
// 验证浏览器登录会话模式在会话与 CSRF 相关字段齐备时可通过配置校验。
func TestConfigValidateAllowsBrowserSessionAuthConfig(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.SessionCookieName = "bridge_admin_session"
	config.Admin.CSRFCookieName = "bridge_admin_csrf"
	config.Admin.CSRFHeaderName = "X-CSRF-Token"
	config.Admin.AllowedOrigins = []string{bootstrapTestAllowedOrigin}
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass for browser session auth config: %v", err)
	}
}

// TestConfigValidateRejectsMissingTLSFilesWhenTLSEnabled
// 验证控制面启用 TLS 模式后必须显式提供证书和私钥路径。
func TestConfigValidateRejectsMissingTLSFilesWhenTLSEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ControlPlane.TLSMode = string(apptls.ModeRequired)
	config.ControlPlane.TLSCertFile = ""
	config.ControlPlane.TLSKeyFile = ""
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when tls_mode requires cert/key")
	}
}

// TestConfigValidateAllowsTLSModeOptionalWithFiles
// 验证 optional 模式在证书路径齐备时可通过结构化校验。
func TestConfigValidateAllowsTLSModeOptionalWithFiles(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ControlPlane.TLSMode = string(apptls.ModeOptional)
	config.ControlPlane.TLSCertFile = "/tmp/bridge-cert.pem"
	config.ControlPlane.TLSKeyFile = "/tmp/bridge-key.pem"
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass when tls_mode optional has cert/key: %v", err)
	}
}

// TestConfigValidateRejectsManagedCAWithoutSAN 验证 managed_ca 模式缺失 SAN 时会被配置校验拒绝。
func TestConfigValidateRejectsManagedCAWithoutSAN(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ControlPlane.TLSMode = string(apptls.ModeRequired)
	config.ControlPlane.TLSCertSource = string(apptls.CertSourceManagedCA)
	config.ControlPlane.TLSCACertFile = "/tmp/managed-root-ca.crt"
	config.ControlPlane.TLSCAKeyFile = "/tmp/managed-root-ca.key"
	config.ControlPlane.TLSServerSANDNS = nil
	config.ControlPlane.TLSServerSANIPs = nil
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when managed_ca has no san")
	}
}

// TestConfigValidateAllowsManagedCAWithSAN 验证 managed_ca 模式在 CA 文件和 SAN 齐备时可通过校验。
func TestConfigValidateAllowsManagedCAWithSAN(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ControlPlane.TLSMode = string(apptls.ModeRequired)
	config.ControlPlane.TLSCertSource = string(apptls.CertSourceManagedCA)
	config.ControlPlane.TLSCACertFile = "/tmp/managed-root-ca.crt"
	config.ControlPlane.TLSCAKeyFile = "/tmp/managed-root-ca.key"
	config.ControlPlane.TLSServerSANDNS = []string{"bridge.internal.example"}
	config.ControlPlane.TLSServerCertTTL = 72 * time.Hour
	config.ControlPlane.TLSServerCertRenewBefore = 12 * time.Hour
	if err := config.Validate(); err != nil {
		testingObject.Fatalf("validate config should pass for managed_ca: %v", err)
	}
}

// TestBootstrapIgnoresTLSFilesWhenTLSModePlaintext 验证 plaintext 模式下不会因为残留证书路径阻断启动。
func TestBootstrapIgnoresTLSFilesWhenTLSModePlaintext(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ControlPlane.TLSMode = string(apptls.ModePlaintext)
	config.ControlPlane.TLSCertFile = "/tmp/stale-bridge-cert.pem"
	config.ControlPlane.TLSKeyFile = "/tmp/stale-bridge-key.pem"

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime should ignore tls files in plaintext mode: %v", err)
	}
	if runtime.controlServer == nil {
		testingObject.Fatalf("expected control server initialized")
	}
	if runtime.controlServer.currentServerTLSConfig() != nil {
		testingObject.Fatalf("expected plaintext mode not to load server tls config")
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

// TestConfigValidateRejectsEmptyAdminAuthProvidersWhenEnabled
// 验证 admin 开启时必须配置至少一个登录 provider，避免无鉴权暴露管理接口。
func TestConfigValidateRejectsEmptyAdminAuthProvidersWhenEnabled(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthProviders = nil
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin enabled and auth providers empty")
	}
}

// TestConfigValidateRejectsInvalidAdminAuthRole
// 验证 admin 账号角色非法时会被配置校验拦截。
func TestConfigValidateRejectsInvalidAdminAuthRole(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.AuthProviders = []AdminAuthProviderConfig{
		{
			Name:    "local-password",
			Type:    "password",
			Enabled: true,
			Password: AdminPasswordProviderConfig{
				Accounts: []AdminPasswordAccountConfig{
					{
						Username: "invalid",
						Password: "invalid-pass",
						Role:     "super_admin",
					},
				},
			},
		},
	}
	if err := config.Validate(); err == nil {
		testingObject.Fatalf("validate config should fail when admin auth role is unsupported")
	}
}

// TestBootstrapRegistersAdminAPIRoutesWhenEnabled
// 验证管理面开启后会注册 /api/admin/* 路由，并强制浏览器会话鉴权。
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

	viewerSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "viewer", "devbridge-viewer-pass")
	authorizedRecorder := httptest.NewRecorder()
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	applyBootstrapSession(authorizedRequest, viewerSession)
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
	viewerSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "viewer", "devbridge-viewer-pass")
	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	applyBootstrapSession(overviewRequest, viewerSession)
	runtime.adminServer.Handler.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected overview status: got=%d want=%d body=%s", overviewRecorder.Code, http.StatusOK, overviewRecorder.Body.String())
	}

	// 最后验证受控写接口可执行，覆盖“单二进制读写链路”关键验收点。
	operatorSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "operator", "devbridge-operator-pass")
	reloadRecorder := httptest.NewRecorder()
	reloadRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	applyBootstrapSession(reloadRequest, operatorSession)
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

	viewerSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "viewer", "devbridge-viewer-pass")
	// SSE 是长连接，需要用可取消上下文结束请求，避免测试阻塞。
	requestContext, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/events/stream?topics=dashboard&interval_ms=1000",
		nil,
	).WithContext(requestContext)
	applyBootstrapSession(request, viewerSession)
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
	runtime.dataPlane.serviceRegistry.Upsert(now, pb.LogicalService{
		LogicalServiceID: "service-1",
		ServiceName:      "order-service",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Status:               pb.ServiceStatusActive,
		ActiveInstanceCount:  1,
		HealthyInstanceCount: 1,
	}, pb.ServiceInstance{
		InstanceID:       "instance-1",
		LogicalServiceID: "service-1",
		ConnectorID:      "connector-1",
		SessionID:        "session-1",
		SessionEpoch:     1,
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
	})
	if _, err := runtime.dataPlane.tunnelRegistry.UpsertIdle(
		now,
		"connector-1",
		"session-1",
		&bootstrapTestTunnel{id: "tunnel-1"},
	); err != nil {
		testingObject.Fatalf("upsert tunnel failed: %v", err)
	}

	operatorSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "operator", "devbridge-operator-pass")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/ops/session/session-1/drain",
		strings.NewReader(`{"reason":"manual_drain"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	applyBootstrapSession(request, operatorSession)
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

	logicalServiceSnapshot, exists := runtime.dataPlane.serviceRegistry.GetLogicalServiceByID("service-1")
	if !exists {
		testingObject.Fatalf("expected service exists")
	}
	if logicalServiceSnapshot.Status != pb.ServiceStatusInactive {
		testingObject.Fatalf("unexpected service status: got=%s want=%s", logicalServiceSnapshot.Status, pb.ServiceStatusInactive)
	}
	instanceSnapshot, exists := runtime.dataPlane.serviceRegistry.GetInstanceByID("instance-1")
	if !exists {
		testingObject.Fatalf("expected instance exists")
	}
	if instanceSnapshot.Instance.InstanceStatus != pb.ServiceStatusInactive {
		testingObject.Fatalf(
			"unexpected instance status: got=%s want=%s",
			instanceSnapshot.Instance.InstanceStatus,
			pb.ServiceStatusInactive,
		)
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

	adminSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "admin", "devbridge-admin-pass")
	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`{"if_match_version":1,"patch":{"observability.log_level":"debug"}}`),
	)
	updateRequest.Header.Set("Content-Type", "application/json")
	applyBootstrapSession(updateRequest, adminSession)
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
	conflictRequest.Header.Set("Content-Type", "application/json")
	applyBootstrapSession(conflictRequest, adminSession)
	runtime.adminServer.Handler.ServeHTTP(conflictRecorder, conflictRequest)
	if conflictRecorder.Code != http.StatusConflict {
		testingObject.Fatalf("unexpected conflict status: got=%d want=%d body=%s", conflictRecorder.Code, http.StatusConflict, conflictRecorder.Body.String())
	}
}

// TestAdminConfigUpdatePersistsToRuntimeConfigFile 验证配置更新会落盘到运行配置文件。
func TestAdminConfigUpdatePersistsToRuntimeConfigFile(testingObject *testing.T) {
	testingObject.Parallel()

	tempDir := testingObject.TempDir()
	configFilePath := filepath.Join(tempDir, "bridge.yaml")

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false
	config.RuntimeConfigFilePath = configFilePath

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	adminSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "admin", "devbridge-admin-pass")
	updateRecorder := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/admin/config",
		strings.NewReader(`{"if_match_version":1,"patch":{"ingress.http_addr":":18080","ingress.base_domain":"svc.dev.internal","control_plane.heartbeat_timeout_ms":45000}}`),
	)
	updateRequest.Header.Set("Content-Type", "application/json")
	applyBootstrapSession(updateRequest, adminSession)
	runtime.adminServer.Handler.ServeHTTP(updateRecorder, updateRequest)
	if updateRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected update status: got=%d want=%d body=%s", updateRecorder.Code, http.StatusOK, updateRecorder.Body.String())
	}

	savedConfig, err := LoadConfigFromYAMLFile(configFilePath)
	if err != nil {
		testingObject.Fatalf("load saved config failed: %v", err)
	}
	if savedConfig.Ingress.HTTPAddr != ":18080" {
		testingObject.Fatalf("unexpected persisted ingress.http_addr: got=%s want=%s", savedConfig.Ingress.HTTPAddr, ":18080")
	}
	if savedConfig.Ingress.BaseDomain != "svc.dev.internal" {
		testingObject.Fatalf("unexpected persisted ingress.base_domain: got=%s want=%s", savedConfig.Ingress.BaseDomain, "svc.dev.internal")
	}
	if savedConfig.ControlPlane.HeartbeatTimeout != 45*time.Second {
		testingObject.Fatalf(
			"unexpected persisted control_plane.heartbeat_timeout: got=%s want=%s",
			savedConfig.ControlPlane.HeartbeatTimeout,
			45*time.Second,
		)
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

	adminSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "admin", "devbridge-admin-pass")
	exportRecorder := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/diagnose/export", nil)
	applyBootstrapSession(exportRequest, adminSession)
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
	applyBootstrapSession(downloadRequest, adminSession)
	runtime.adminServer.Handler.ServeHTTP(downloadRecorder, downloadRequest)
	if downloadRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected download status: got=%d want=%d body=%s", downloadRecorder.Code, http.StatusOK, downloadRecorder.Body.String())
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodGet, exportResponse.DownloadURL, nil)
	applyBootstrapSession(replayRequest, adminSession)
	runtime.adminServer.Handler.ServeHTTP(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected replay status: got=%d want=%d body=%s", replayRecorder.Code, http.StatusNotFound, replayRecorder.Body.String())
	}
}

// TestBootstrapSessionWriteRequiresCSRFOnSingleServer
// 验证浏览器会话模式在单实例 server 下生效，且写请求强制 CSRF 与 Origin 校验。
func TestBootstrapSessionWriteRequiresCSRFOnSingleServer(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Admin.Enabled = true
	config.Admin.UIEnabled = false
	config.Admin.SessionCookieName = "bridge_admin_session"
	config.Admin.CSRFCookieName = "bridge_admin_csrf"
	config.Admin.CSRFHeaderName = "X-CSRF-Token"
	config.Admin.AllowedOrigins = []string{bootstrapTestAllowedOrigin}

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.adminServer == nil {
		testingObject.Fatalf("expected admin server initialized")
	}

	viewerSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "viewer", "devbridge-viewer-pass")
	operatorSession := loginBootstrapUser(testingObject, runtime.adminServer.Handler, "operator", "devbridge-operator-pass")
	// 读接口仅依赖鉴权，不要求 CSRF。
	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	applyBootstrapSession(overviewRequest, viewerSession)
	runtime.adminServer.Handler.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected overview status in session auth mode: got=%d want=%d body=%s", overviewRecorder.Code, http.StatusOK, overviewRecorder.Body.String())
	}

	// 写接口缺失 CSRF Header 时应拒绝。
	forbiddenRecorder := httptest.NewRecorder()
	forbiddenRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	for _, cookie := range operatorSession.cookies {
		forbiddenRequest.AddCookie(cookie)
	}
	forbiddenRequest.Header.Set("Origin", bootstrapTestAllowedOrigin)
	runtime.adminServer.Handler.ServeHTTP(forbiddenRecorder, forbiddenRequest)
	if forbiddenRecorder.Code != http.StatusForbidden {
		testingObject.Fatalf("unexpected forbidden status without csrf header: got=%d want=%d body=%s", forbiddenRecorder.Code, http.StatusForbidden, forbiddenRecorder.Body.String())
	}

	// 带齐 CSRF Header + Cookie + 允许来源后，写接口应通过。
	successRecorder := httptest.NewRecorder()
	successRequest := httptest.NewRequest(http.MethodPost, "/api/admin/ops/config/reload", nil)
	applyBootstrapSession(successRequest, operatorSession)
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
