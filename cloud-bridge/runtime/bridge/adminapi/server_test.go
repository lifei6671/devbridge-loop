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

// TestOverviewRequiresSession 验证未建立登录会话时会被拒绝。
func TestOverviewRequiresSession(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ListRoutes:           func() []pb.Route { return nil },
			ListLogicalServices:  func() []pb.LogicalService { return nil },
			ListServiceInstances: func() []pb.ServiceInstance { return nil },
			ListSessions:         func() []registry.SessionRuntime { return nil },
			ListTunnels:          func() []registry.TunnelRuntime { return nil },
			TunnelSnapshot:       func() registry.TunnelSnapshot { return registry.TunnelSnapshot{} },
			BuildConfigSnapshot:  func() map[string]any { return map[string]any{} },
		},
		AuthProviders:  newAuthProvidersForTest(testAuthAccount{username: "viewer-user", password: "viewer-pass", role: RoleViewer}),
		AllowedOrigins: []string{testAllowedOrigin},
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

// TestAuthSessionEchoesConfiguredCSRFHeaderName 验证会话接口会回传服务端实际使用的 CSRF Header 名。
func TestAuthSessionEchoesConfiguredCSRFHeaderName(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies:      Dependencies{},
		AuthProviders:     newAuthProvidersForTest(testAuthAccount{username: "viewer-user", password: "viewer-pass", role: RoleViewer}),
		CSRFHeaderName:    "X-Bridge-CSRF",
		AllowedOrigins:    []string{testAllowedOrigin},
		SessionCookieName: "bridge_admin_session",
	})
	if err != nil {
		testingObject.Fatalf("new admin api server failed: %v", err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	sessionRecorder := httptest.NewRecorder()
	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/admin/auth/session", nil)
	mux.ServeHTTP(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected anonymous session status: got=%d want=%d", sessionRecorder.Code, http.StatusOK)
	}
	var anonymousPayload map[string]any
	if err := json.Unmarshal(sessionRecorder.Body.Bytes(), &anonymousPayload); err != nil {
		testingObject.Fatalf("decode anonymous session payload failed: %v body=%s", err, sessionRecorder.Body.String())
	}
	if anonymousPayload["csrf_header_name"] != "X-Bridge-CSRF" {
		testingObject.Fatalf("unexpected anonymous csrf header name: %+v", anonymousPayload)
	}

	session := loginAsTestUser(testingObject, mux, "viewer-user", "viewer-pass")
	authenticatedRecorder := httptest.NewRecorder()
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/admin/auth/session", nil)
	applyTestSession(authenticatedRequest, session)
	mux.ServeHTTP(authenticatedRecorder, authenticatedRequest)
	if authenticatedRecorder.Code != http.StatusOK {
		testingObject.Fatalf(
			"unexpected authenticated session status: got=%d want=%d body=%s",
			authenticatedRecorder.Code,
			http.StatusOK,
			authenticatedRecorder.Body.String(),
		)
	}
	var authenticatedPayload map[string]any
	if err := json.Unmarshal(authenticatedRecorder.Body.Bytes(), &authenticatedPayload); err != nil {
		testingObject.Fatalf("decode authenticated session payload failed: %v body=%s", err, authenticatedRecorder.Body.String())
	}
	if authenticatedPayload["csrf_header_name"] != "X-Bridge-CSRF" {
		testingObject.Fatalf("unexpected authenticated csrf header name: %+v", authenticatedPayload)
	}
}

// TestViewerSessionCanReadOverview 验证 viewer 角色可以访问只读总览接口。
func TestViewerSessionCanReadOverview(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now:                 func() time.Time { return now },
			ListRoutes:          func() []pb.Route { return []pb.Route{{RouteID: "route-1"}} },
			ListLogicalServices: func() []pb.LogicalService { return []pb.LogicalService{{LogicalServiceID: "ls-1"}} },
			ListServiceInstances: func() []pb.ServiceInstance {
				return []pb.ServiceInstance{{InstanceID: "inst-1", ConnectorID: "conn-1"}}
			},
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
						"listen_addr": ":39080",
					},
					"control_plane": map[string]any{
						"listen_addr":         ":39081",
						"grpc_h2_listen_addr": ":39082",
					},
				}
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	applyTestSession(request, session)
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
			ListRoutes:           func() []pb.Route { return nil },
			ListLogicalServices:  func() []pb.LogicalService { return nil },
			ListServiceInstances: func() []pb.ServiceInstance { return nil },
			ListSessions:         func() []registry.SessionRuntime { return nil },
			ListTunnels:          func() []registry.TunnelRuntime { return nil },
			TunnelSnapshot:       func() registry.TunnelSnapshot { return registry.TunnelSnapshot{} },
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/logs/search", nil)
	applyTestSession(request, session)
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/tunnels/summary?connector_id=agent-a", nil)
	applyTestSession(request, session)
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

// TestReadonlyListsExposeQUICBinding 验证只读管理面会返回 QUIC binding 与 QUIC listener 概览。
func TestReadonlyListsExposeQUICBinding(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1773492430, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now: func() time.Time { return now },
			ListSessions: func() []registry.SessionRuntime {
				return []registry.SessionRuntime{
					{
						SessionID:     "session-quic-1",
						ConnectorID:   "agent-quic",
						Epoch:         7,
						Binding:       "quic_native",
						State:         registry.SessionActive,
						LastHeartbeat: now,
						UpdatedAt:     now,
					},
				}
			},
			ListTunnels: func() []registry.TunnelRuntime {
				return []registry.TunnelRuntime{
					{
						TunnelID:    "tun-quic-1",
						ConnectorID: "agent-quic",
						SessionID:   "session-quic-1",
						Binding:     "quic_native",
						State:       registry.TunnelStateIdle,
						UpdatedAt:   now,
					},
				}
			},
			TunnelSnapshot: func() registry.TunnelSnapshot {
				return registry.TunnelSnapshot{
					IdleCount:  1,
					TotalCount: 1,
					UpdatedAt:  now,
				}
			},
			BuildConfigSnapshot: func() map[string]any {
				return map[string]any{
					"config_version": uint64(1),
					"control_plane": map[string]any{
						"listen_addr":         ":39080",
						"grpc_h2_listen_addr": ":39082",
						"quic_listen_addr":    ":39083",
						"tls_mode":            "required",
					},
				}
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

	sessionsRecorder := httptest.NewRecorder()
	sessionsRequest := httptest.NewRequest(http.MethodGet, "/api/admin/sessions", nil)
	applyTestSession(sessionsRequest, session)
	mux.ServeHTTP(sessionsRecorder, sessionsRequest)
	if sessionsRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected sessions status: got=%d want=%d body=%s", sessionsRecorder.Code, http.StatusOK, sessionsRecorder.Body.String())
	}
	if !strings.Contains(sessionsRecorder.Body.String(), "\"binding\":\"quic_native\"") {
		testingObject.Fatalf("expected sessions payload to include quic binding: %s", sessionsRecorder.Body.String())
	}

	tunnelsRecorder := httptest.NewRecorder()
	tunnelsRequest := httptest.NewRequest(http.MethodGet, "/api/admin/tunnels", nil)
	applyTestSession(tunnelsRequest, session)
	mux.ServeHTTP(tunnelsRecorder, tunnelsRequest)
	if tunnelsRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected tunnels status: got=%d want=%d body=%s", tunnelsRecorder.Code, http.StatusOK, tunnelsRecorder.Body.String())
	}
	if !strings.Contains(tunnelsRecorder.Body.String(), "\"binding\":\"quic_native\"") {
		testingObject.Fatalf("expected tunnels payload to include quic binding: %s", tunnelsRecorder.Body.String())
	}

	overviewRecorder := httptest.NewRecorder()
	overviewRequest := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview", nil)
	applyTestSession(overviewRequest, session)
	mux.ServeHTTP(overviewRecorder, overviewRequest)
	if overviewRecorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected overview status: got=%d want=%d body=%s", overviewRecorder.Code, http.StatusOK, overviewRecorder.Body.String())
	}
	if !strings.Contains(overviewRecorder.Body.String(), "\"listener_id\":\"control_plane_quic\"") {
		testingObject.Fatalf("expected overview payload to include quic listener: %s", overviewRecorder.Body.String())
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
			ListLogicalServices: func() []pb.LogicalService {
				return []pb.LogicalService{
					{
						LogicalServiceID:     "ls-order",
						ServiceName:          "order-service",
						Scope:                pb.Scope{Namespace: "dev", Environment: "demo"},
						Status:               pb.ServiceStatusActive,
						ActiveInstanceCount:  1,
						HealthyInstanceCount: 1,
					},
				}
			},
			ListServiceInstances: func() []pb.ServiceInstance {
				return []pb.ServiceInstance{
					{
						InstanceID:       "inst-order",
						LogicalServiceID: "ls-order",
						ConnectorID:      "agent-a",
						SessionID:        "session-a",
						InstanceStatus:   pb.ServiceStatusActive,
						HealthStatus:     pb.HealthStatusHealthy,
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/services", nil)
	applyTestSession(request, session)
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
	if item["route_target"] != "connector_service.selector={serviceName:order-service,scope:dev/demo}" {
		testingObject.Fatalf("unexpected route_target: %+v", item["route_target"])
	}
	if item["sni_name"] != "order.demo.example.com" {
		testingObject.Fatalf("unexpected sni_name: %+v", item["sni_name"])
	}
}

// TestTrafficOwnershipLookupByTrafficID 验证可按 traffic_id 查询服务归属。
func TestTrafficOwnershipLookupByTrafficID(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ResolveTrafficOwnership: func(trafficID string) (TrafficOwnershipRecord, bool) {
				if strings.TrimSpace(trafficID) != "traffic-ownership-1" {
					return TrafficOwnershipRecord{}, false
				}
				return TrafficOwnershipRecord{
					TrafficID:        "traffic-ownership-1",
					RouteID:          "route-1",
					TargetKind:       "connector_service",
					IngressMode:      "l7_shared",
					LogicalServiceID: "svc-1",
					ServiceName:      "order-service",
					Scope:            pb.Scope{Namespace: "dev", Environment: "demo"},
					InstanceID:       "svcinst:svc-1|connector-1|session-1",
					ConnectorID:      "connector-1",
					SessionID:        "session-1",
					UpdatedAtMS:      1700000000000,
				}, true
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/traffic/ownership?traffic_id=traffic-ownership-1", nil)
	applyTestSession(request, session)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		testingObject.Fatalf("decode traffic ownership payload failed: %v body=%s", err, recorder.Body.String())
	}
	ownership, ok := payload["ownership"].(map[string]any)
	if !ok {
		testingObject.Fatalf("ownership payload missing: %+v", payload)
	}
	if ownership["logical_service_id"] != "svc-1" {
		testingObject.Fatalf("unexpected logical_service_id: %+v", ownership["logical_service_id"])
	}
	if ownership["instance_id"] != "svcinst:svc-1|connector-1|session-1" {
		testingObject.Fatalf("unexpected instance_id: %+v", ownership["instance_id"])
	}
	scope, ok := ownership["scope"].(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected ownership scope: %+v", ownership["scope"])
	}
	if scope["namespace"] != "dev" || scope["environment"] != "demo" {
		testingObject.Fatalf("unexpected ownership scope payload: %+v", scope)
	}
}

// TestTrafficOwnershipLookupRequiresTrafficID 验证 traffic ownership 查询必须携带 traffic_id。
func TestTrafficOwnershipLookupRequiresTrafficID(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ResolveTrafficOwnership: func(trafficID string) (TrafficOwnershipRecord, bool) {
				_ = trafficID
				return TrafficOwnershipRecord{}, false
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/traffic/ownership", nil)
	applyTestSession(request, session)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusBadRequest)
	}
}

// TestTrafficOwnershipLookupNotFound 验证未知 traffic_id 返回 not found。
func TestTrafficOwnershipLookupNotFound(testingObject *testing.T) {
	testingObject.Parallel()

	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			ResolveTrafficOwnership: func(trafficID string) (TrafficOwnershipRecord, bool) {
				_ = trafficID
				return TrafficOwnershipRecord{}, false
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
	request := httptest.NewRequest(http.MethodGet, "/api/admin/traffic/ownership?traffic_id=traffic-missing", nil)
	applyTestSession(request, session)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusNotFound)
	}
}
