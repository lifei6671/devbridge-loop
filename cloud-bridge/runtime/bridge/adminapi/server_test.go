package adminapi

import (
	"encoding/json"
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
			ListTunnels:    func() []registry.TunnelRuntime { return []registry.TunnelRuntime{} },
			TunnelSnapshot: func() registry.TunnelSnapshot { return registry.TunnelSnapshot{IdleCount: 1, TotalCount: 1} },
			BuildConfigSnapshot: func() map[string]any {
				return map[string]any{
					"config_version": uint64(1),
					"ingress": map[string]any{
						"http_addr": ":38080",
						"grpc_addr": ":38081",
					},
					"admin": map[string]any{
						"enabled":     true,
						"listen_addr": ":39081",
					},
					"control_plane": map[string]any{
						"listen_addr":         ":39080",
						"grpc_h2_listen_addr": ":39082",
					},
				}
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	request.Header.Set("Authorization", "Bearer viewer-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "\"connector_total\":1") {
		testingObject.Fatalf("unexpected overview payload: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "\"listener_id\":\"ingress_http\"") {
		testingObject.Fatalf("expected overview payload to include ingress listener: %s", recorder.Body.String())
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

// TestTunnelSummarySupportsConnectorFilter 验证 tunnel summary 支持按 connector_id 聚合。
func TestTunnelSummarySupportsConnectorFilter(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1773491430, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now: func() time.Time { return now },
			ListTunnels: func() []registry.TunnelRuntime {
				return []registry.TunnelRuntime{
					{
						TunnelID:    "tunnel-a-idle",
						ConnectorID: "agent-a",
						State:       registry.TunnelStateIdle,
						UpdatedAt:   now,
					},
					{
						TunnelID:    "tunnel-a-active",
						ConnectorID: "agent-a",
						State:       registry.TunnelStateActive,
						UpdatedAt:   now,
					},
					{
						TunnelID:    "tunnel-b-broken",
						ConnectorID: "agent-b",
						State:       registry.TunnelStateBroken,
						UpdatedAt:   now,
					},
				}
			},
			TunnelSnapshot: func() registry.TunnelSnapshot {
				return registry.TunnelSnapshot{
					IdleCount:   10,
					ActiveCount: 4,
					BrokenCount: 2,
					TotalCount:  16,
				}
			},
			ListTunnelPoolReports: func() []TunnelPoolReportSnapshot {
				return []TunnelPoolReportSnapshot{
					{
						ConnectorID:     "agent-a",
						SessionID:       "session-a",
						SessionEpoch:    8,
						IdleCount:       7,
						InUseCount:      3,
						TargetIdleCount: 8,
						UpdatedAtMS:     uint64(now.UnixMilli()),
					},
					{
						ConnectorID:     "agent-b",
						SessionID:       "session-b",
						SessionEpoch:    3,
						IdleCount:       2,
						InUseCount:      1,
						TargetIdleCount: 4,
						UpdatedAtMS:     uint64(now.UnixMilli()),
					},
				}
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/tunnels/summary?connector_id=agent-a", nil)
	request.Header.Set("Authorization", "Bearer viewer-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		testingObject.Fatalf("decode summary payload failed: %v body=%s", err, recorder.Body.String())
	}
	summary, ok := payload["summary"].(map[string]any)
	if !ok {
		testingObject.Fatalf("summary payload is missing: %s", recorder.Body.String())
	}
	if int(summary["idle"].(float64)) != 1 {
		testingObject.Fatalf("unexpected idle count: %+v", summary)
	}
	if int(summary["active"].(float64)) != 1 {
		testingObject.Fatalf("unexpected active count: %+v", summary)
	}
	if int(summary["broken"].(float64)) != 0 {
		testingObject.Fatalf("unexpected broken count: %+v", summary)
	}
	if int(summary["total"].(float64)) != 2 {
		testingObject.Fatalf("unexpected total count: %+v", summary)
	}
	if payload["connector_id_filter"] != "agent-a" {
		testingObject.Fatalf("unexpected connector filter echo: %+v", payload)
	}
	agentPoolSummary, ok := payload["agent_pool_summary"].(map[string]any)
	if !ok {
		testingObject.Fatalf("agent pool summary is missing: %+v", payload)
	}
	if int(agentPoolSummary["connected"].(float64)) != 10 {
		testingObject.Fatalf("unexpected agent connected count: %+v", agentPoolSummary)
	}
	if int(agentPoolSummary["idle"].(float64)) != 7 || int(agentPoolSummary["in_use"].(float64)) != 3 {
		testingObject.Fatalf("unexpected agent pool breakdown: %+v", agentPoolSummary)
	}
}

// TestBuildAgentTunnelPoolSummaryUsesLatestReportTimestamp
// 验证 agent pool 汇总的 updated_at_ms 来源于 report 数据，而不是请求当前时间。
func TestBuildAgentTunnelPoolSummaryUsesLatestReportTimestamp(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_000_000, 0).UTC()
	oldReportAt := uint64(now.Add(-30 * time.Minute).UnixMilli())
	latestReportAt := uint64(now.Add(-5 * time.Minute).UnixMilli())

	summary := buildAgentTunnelPoolSummary(now, []TunnelPoolReportSnapshot{
		{
			ConnectorID:     "agent-a",
			SessionID:       "session-a",
			SessionEpoch:    3,
			IdleCount:       2,
			InUseCount:      1,
			TargetIdleCount: 4,
			UpdatedAtMS:     oldReportAt,
		},
		{
			ConnectorID:     "agent-b",
			SessionID:       "session-b",
			SessionEpoch:    7,
			IdleCount:       5,
			InUseCount:      3,
			TargetIdleCount: 8,
			ReportedAtMS:    latestReportAt,
		},
	})

	if summary.UpdatedAtMS != latestReportAt {
		testingObject.Fatalf(
			"unexpected updated_at_ms: got=%d want=%d",
			summary.UpdatedAtMS,
			latestReportAt,
		)
	}
}

// TestServicesListIncludesConnectorAssociation 验证 services 接口可返回服务与 connector/session 关联信息。
func TestServicesListIncludesConnectorAssociation(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_200_000, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now: func() time.Time { return now },
			ListServices: func() []pb.Service {
				return []pb.Service{
					{
						ServiceID:    "svc-order",
						ServiceKey:   "dev/demo/order-service",
						Namespace:    "dev",
						Environment:  "demo",
						ConnectorID:  "agent-a",
						ServiceName:  "order-service",
						ServiceType:  "https",
						Status:       pb.ServiceStatusActive,
						HealthStatus: pb.HealthStatusHealthy,
						Endpoints: []pb.ServiceEndpoint{
							{
								EndpointID: "ep-order-1",
								Protocol:   "https",
								Host:       "127.0.0.1",
								Port:       18443,
								ServerName: "order.demo.example.com",
							},
						},
					},
				}
			},
			ListSessions: func() []registry.SessionRuntime {
				return []registry.SessionRuntime{
					{
						SessionID:   "session-a",
						ConnectorID: "agent-a",
						State:       registry.SessionActive,
						Epoch:       8,
						UpdatedAt:   now,
					},
				}
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/services", nil)
	request.Header.Set("Authorization", "Bearer viewer-token")
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		testingObject.Fatalf("decode services payload failed: %v body=%s", err, recorder.Body.String())
	}
	itemsRaw, ok := payload["items"].([]any)
	if !ok || len(itemsRaw) != 1 {
		testingObject.Fatalf("unexpected items payload: %+v", payload["items"])
	}
	item, ok := itemsRaw[0].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected item type: %+v", itemsRaw[0])
	}
	if item["connector_id"] != "agent-a" || item["session_id"] != "session-a" {
		testingObject.Fatalf("unexpected connector/session mapping: %+v", item)
	}
	if item["route_target"] != "connector_service.service_key=dev/demo/order-service" {
		testingObject.Fatalf("unexpected route_target: %+v", item["route_target"])
	}
	if item["sni_name"] != "order.demo.example.com" {
		testingObject.Fatalf("unexpected sni_name: %+v", item["sni_name"])
	}
}
