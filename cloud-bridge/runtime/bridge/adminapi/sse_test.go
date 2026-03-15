package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminview"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// newSSETestServer 构造 SSE 场景下最小可用的 admin server。
func newSSETestServer(testingObject *testing.T) *Server {
	testingObject.Helper()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now:          func() time.Time { return time.Unix(1773491430, 0).UTC() },
			ListRoutes:   func() []pb.Route { return []pb.Route{{RouteID: "route-1"}} },
			ListServices: func() []pb.Service { return []pb.Service{{ServiceID: "svc-1", ConnectorID: "connector-1"}} },
			ListSessions: func() []registry.SessionRuntime {
				return []registry.SessionRuntime{{
					SessionID:   "session-1",
					ConnectorID: "connector-1",
					State:       registry.SessionActive,
					UpdatedAt:   time.Unix(1773491430, 0).UTC(),
				}}
			},
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
	return server
}

// TestSSETopicsQuerySupportsAll 验证 topics=all 会展开为全部可订阅主题。
func TestSSETopicsQuerySupportsAll(testingObject *testing.T) {
	testingObject.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/api/admin/events/stream?topics=all", nil)
	topics, err := parseSSETopicsQuery(request)
	if err != nil {
		testingObject.Fatalf("parse topics failed: %v", err)
	}
	if len(topics) != 7 {
		testingObject.Fatalf("unexpected topics size: got=%d want=7", len(topics))
	}
}

// TestEventsStreamAllowsAccessTokenQuery 验证 SSE 路由支持 access_token query 鉴权。
func TestEventsStreamAllowsAccessTokenQuery(testingObject *testing.T) {
	testingObject.Parallel()

	server := newSSETestServer(testingObject)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/events/stream?topics=dashboard&interval_ms=1000&access_token=viewer-token",
		nil,
	).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	serveDoneChannel := make(chan struct{})
	go func() {
		mux.ServeHTTP(recorder, request)
		close(serveDoneChannel)
	}()

	// 给 handler 留出首帧 ready/snapshot 的输出时间，然后取消请求结束长连接。
	time.Sleep(40 * time.Millisecond)
	cancelRequest()
	select {
	case <-serveDoneChannel:
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("events stream request did not exit after context cancel")
	}

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
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

// TestOverviewRejectsAccessTokenQuery 验证非 SSE 路由不会接受 access_token query 作为鉴权。
func TestOverviewRejectsAccessTokenQuery(testingObject *testing.T) {
	testingObject.Parallel()

	server := newSSETestServer(testingObject)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/bridge/overview?access_token=viewer-token", nil)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		testingObject.Fatalf("unexpected status: got=%d want=%d", recorder.Code, http.StatusUnauthorized)
	}
}

// TestParseSSESnapshotQuerySupportsConnectorFilter 验证 SSE 查询参数支持 connector_id。
func TestParseSSESnapshotQuerySupportsConnectorFilter(testingObject *testing.T) {
	testingObject.Parallel()

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/admin/events/stream?topics=traffic&connector_id=agent-local&tunnel_state=active",
		nil,
	)
	snapshotQuery, err := parseSSESnapshotQuery(request)
	if err != nil {
		testingObject.Fatalf("parse snapshot query failed: %v", err)
	}
	if snapshotQuery.tunnelConnectorID != "agent-local" {
		testingObject.Fatalf("unexpected connector filter: %+v", snapshotQuery)
	}
	if snapshotQuery.tunnelStateFilter != "active" {
		testingObject.Fatalf("unexpected tunnel state filter: %+v", snapshotQuery)
	}
}

// TestBuildTrafficSnapshotHonorsConnectorFilter 验证 traffic topic 快照支持按 connector 过滤。
func TestBuildTrafficSnapshotHonorsConnectorFilter(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1773491430, 0).UTC()
	server, err := NewServer(ServerOptions{
		Dependencies: Dependencies{
			Now:        func() time.Time { return now },
			ListRoutes: func() []pb.Route { return nil },
			ListServices: func() []pb.Service {
				return []pb.Service{{ServiceID: "svc-a", ConnectorID: "agent-a"}}
			},
			ListSessions: func() []registry.SessionRuntime {
				return []registry.SessionRuntime{{
					SessionID:   "session-a",
					ConnectorID: "agent-a",
					State:       registry.SessionActive,
					UpdatedAt:   now,
				}}
			},
			ListTunnels: func() []registry.TunnelRuntime {
				return []registry.TunnelRuntime{
					{
						TunnelID:    "tunnel-a-idle",
						ConnectorID: "agent-a",
						State:       registry.TunnelStateIdle,
						UpdatedAt:   now,
					},
					{
						TunnelID:    "tunnel-b-active",
						ConnectorID: "agent-b",
						State:       registry.TunnelStateActive,
						UpdatedAt:   now,
					},
				}
			},
			TunnelSnapshot: func() registry.TunnelSnapshot {
				return registry.TunnelSnapshot{
					IdleCount:   1,
					ActiveCount: 1,
					TotalCount:  2,
					UpdatedAt:   now,
				}
			},
			ListTunnelPoolReports: func() []TunnelPoolReportSnapshot {
				return []TunnelPoolReportSnapshot{
					{
						ConnectorID:     "agent-a",
						SessionID:       "session-a",
						SessionEpoch:    8,
						IdleCount:       6,
						InUseCount:      4,
						TargetIdleCount: 8,
						UpdatedAtMS:     uint64(now.UnixMilli()),
					},
					{
						ConnectorID:     "agent-b",
						SessionID:       "session-b",
						SessionEpoch:    5,
						IdleCount:       3,
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

	payload := server.buildSSETopicPayload(sseTopicTraffic, sseSnapshotQuery{
		tunnelConnectorID: "agent-a",
	})
	tunnelSummary, ok := payload["tunnel_summary"].(adminview.TunnelSummarySnapshot)
	if !ok {
		testingObject.Fatalf("tunnel summary type mismatch: %#v", payload["tunnel_summary"])
	}
	if tunnelSummary.Total != 1 || tunnelSummary.Idle != 1 || tunnelSummary.Active != 0 {
		testingObject.Fatalf("unexpected filtered summary: %+v", tunnelSummary)
	}
	tunnelItems, ok := payload["tunnels"].([]adminview.TunnelItem)
	if !ok {
		testingObject.Fatalf("tunnel items type mismatch: %#v", payload["tunnels"])
	}
	if len(tunnelItems) != 1 || tunnelItems[0].ConnectorID != "agent-a" {
		testingObject.Fatalf("unexpected filtered tunnel items: %+v", tunnelItems)
	}
	if payload["tunnel_connector_filter"] != "agent-a" {
		testingObject.Fatalf("unexpected connector filter marker: %#v", payload["tunnel_connector_filter"])
	}
	agentPoolSummary, ok := payload["agent_pool_summary"].(AgentTunnelPoolSummary)
	if !ok {
		testingObject.Fatalf("agent pool summary type mismatch: %#v", payload["agent_pool_summary"])
	}
	if agentPoolSummary.Connected != 10 || agentPoolSummary.Idle != 6 || agentPoolSummary.InUse != 4 {
		testingObject.Fatalf("unexpected filtered agent pool summary: %+v", agentPoolSummary)
	}
}
