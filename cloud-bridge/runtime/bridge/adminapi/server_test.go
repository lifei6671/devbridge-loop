package adminapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestOverviewRequiresBearerToken 验证未携带 Bearer Token 时会被拒绝。
func TestOverviewRequiresBearerToken(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ListRoutes:          func() []pb.Route { return nil },
			ListServices:        func() []pb.Service { return nil },
			ListSessions:        func() []registry.SessionRuntime { return nil },
			ListTunnels:         func() []registry.TunnelRuntime { return nil },
			TunnelSnapshot:      func() registry.TunnelSnapshot { return registry.TunnelSnapshot{} },
			BuildConfigSnapshot: func() map[string]any { return map[string]any{} },
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusUnauthorized)
	}
}

// TestViewerTokenCanReadOverview 验证 viewer 角色可以访问只读总览接口。
func TestViewerTokenCanReadOverview(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now:          func() time.Time { return now },
			ListRoutes:   func() []pb.Route { return []pb.Route{{RouteID: "route-1"}} },
			ListServices: func() []pb.Service { return []pb.Service{{ServiceID: "svc-1", ConnectorID: "conn-1"}} },
			ListSessions: func() []registry.SessionRuntime {
				return []registry.SessionRuntime{{SessionID: "sess-1", ConnectorID: "conn-1", State: registry.SessionActive}}
			},
			ListTunnels:         func() []registry.TunnelRuntime { return []registry.TunnelRuntime{} },
			TunnelSnapshot:      func() registry.TunnelSnapshot { return registry.TunnelSnapshot{IdleCount: 1, TotalCount: 1} },
			BuildConfigSnapshot: func() map[string]any { return map[string]any{"config_version": uint64(1)} },
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	request.Header.Set("Authorization", "Bearer viewer-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "\"connector_total\":1") {
		testingObject.Fatalf("unexpected overview payload: %s", recorder.Body.String())
	}
}

// TestLogsSearchRequiresTimeWindow 验证 logs.search 强制要求 from/to 时间窗口参数。
func TestLogsSearchRequiresTimeWindow(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ListRoutes:     func() []pb.Route { return nil },
			ListServices:   func() []pb.Service { return nil },
			ListSessions:   func() []registry.SessionRuntime { return nil },
			ListTunnels:    func() []registry.TunnelRuntime { return nil },
			TunnelSnapshot: func() registry.TunnelSnapshot { return registry.TunnelSnapshot{} },
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/logs/search", nil)
	request.Header.Set("Authorization", "Bearer viewer-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusBadRequest)
	}
}
