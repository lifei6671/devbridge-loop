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

// TestDefaultConfigAdminDisabledByDefault 验证默认配置下管理面总开关为关闭。
func TestDefaultConfigAdminDisabledByDefault(testingObject *testing.T) {
	testingObject.Parallel()

	defaultConfig := DefaultConfig()
	if defaultConfig.Admin.Enabled {
		testingObject.Fatalf("expected admin.enabled default false")
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
