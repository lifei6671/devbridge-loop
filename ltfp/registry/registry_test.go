package registry

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCanonicalRegistryServiceLookup 验证 service_key 到 service_id 映射查询。
func TestCanonicalRegistryServiceLookup(t *testing.T) {
	t.Parallel()

	registry := NewCanonicalRegistry()
	service := pb.Service{
		ServiceID:   "svc-001",
		ServiceKey:  "dev/alice/order-service",
		Namespace:   "dev",
		Environment: "alice",
	}
	registry.UpsertService(service)

	loaded, exists := registry.GetServiceByKey("dev/alice/order-service")
	if !exists {
		t.Fatalf("expected service exists")
	}
	// 通过 service_key 查询应返回同一 identity。
	if loaded.ServiceID != "svc-001" {
		t.Fatalf("unexpected service id: %s", loaded.ServiceID)
	}
}

// TestRuntimeTrafficRegistryRecordBytes 验证流量字节计数累计逻辑。
func TestRuntimeTrafficRegistryRecordBytes(t *testing.T) {
	t.Parallel()

	registry := NewRuntimeTrafficRegistry()
	registry.UpsertTraffic(pb.Traffic{
		TrafficID: "traffic-001",
		State:     pb.TrafficStateOpening,
	}, pb.RouteTargetTypeConnectorService)
	registry.RecordBytes("traffic-001", 10, 20)
	registry.RecordBytes("traffic-001", 5, 8)

	loaded, exists := registry.GetTraffic("traffic-001")
	if !exists {
		t.Fatalf("expected traffic exists")
	}
	// 字节统计应按增量累加。
	if loaded.UpstreamBytes != 15 || loaded.DownstreamBytes != 28 {
		t.Fatalf("unexpected bytes: up=%d down=%d", loaded.UpstreamBytes, loaded.DownstreamBytes)
	}
}

// TestCanonicalRegistryIndexesAndSnapshot 验证 canonical 索引、审计与快照能力。
func TestCanonicalRegistryIndexesAndSnapshot(t *testing.T) {
	t.Parallel()

	registry := NewCanonicalRegistry()
	registry.UpsertConnectorWithAudit(pb.Connector{
		ConnectorID: "conn-001",
		Namespace:   "dev",
		Environment: "alice",
	}, "evt-conn-1", 11)
	registry.UpsertSessionWithAudit(pb.Session{
		SessionID:    "sess-001",
		ConnectorID:  "conn-001",
		SessionEpoch: 1,
		State:        pb.SessionStateActive,
	}, "evt-sess-1", 22)
	registry.UpsertServiceWithAudit(pb.Service{
		ServiceID:       "svc-001",
		ServiceKey:      "dev/alice/order-service",
		Namespace:       "dev",
		Environment:     "alice",
		ConnectorID:     "conn-001",
		Status:          pb.ServiceStatusActive,
		HealthStatus:    pb.HealthStatusHealthy,
		ResourceVersion: 33,
	}, "evt-svc-1", 33)
	registry.UpsertRouteWithAudit(pb.Route{
		RouteID:         "route-001",
		Namespace:       "dev",
		Environment:     "alice",
		ResourceVersion: 44,
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/alice/order-service",
			},
		},
	}, "evt-route-1", 44)
	registry.UpsertProjectionWithAudit(pb.DiscoveryProjection{
		ProjectionID: "proj-001",
		ServiceID:    "svc-001",
		Provider:     "nacos",
	}, "evt-proj-1", 55)

	serviceID, exists := registry.GetServiceIDByKey("dev/alice/order-service")
	if !exists || serviceID != "svc-001" {
		t.Fatalf("unexpected service id mapping: id=%s exists=%v", serviceID, exists)
	}

	sessions := registry.ListSessionsByConnector("conn-001")
	if len(sessions) != 1 {
		t.Fatalf("unexpected session count: %d", len(sessions))
	}
	services := registry.ListServicesByConnector("conn-001")
	if len(services) != 1 {
		t.Fatalf("unexpected service count: %d", len(services))
	}
	routes := registry.ListRoutesByServiceKey("dev/alice/order-service")
	if len(routes) != 1 || routes[0].RouteID != "route-001" {
		t.Fatalf("unexpected route list: %+v", routes)
	}

	audit, exists := registry.GetAuditInfo("service", "svc-001")
	if !exists {
		t.Fatalf("expected service audit exists")
	}
	// 资源版本应落到审计元数据中，便于管理面追溯。
	if audit.LastResourceVersion != 33 || audit.LastEventID != "evt-svc-1" {
		t.Fatalf("unexpected audit info: %+v", audit)
	}

	snapshot := registry.Snapshot()
	if len(snapshot.Connectors) != 1 || len(snapshot.Sessions) != 1 || len(snapshot.Services) != 1 || len(snapshot.Routes) != 1 || len(snapshot.Projections) != 1 {
		t.Fatalf("unexpected snapshot sizes: %+v", snapshot)
	}
}

// TestRuntimeTrafficRegistryPathAndReject 验证 runtime path 分类、拒绝原因与快照能力。
func TestRuntimeTrafficRegistryPathAndReject(t *testing.T) {
	t.Parallel()

	registry := NewRuntimeTrafficRegistry()
	registry.UpsertTrafficWithAudit(pb.Traffic{
		TrafficID: "traffic-connector",
		TraceID:   "trace-1",
		State:     pb.TrafficStateOpening,
	}, pb.RouteTargetTypeConnectorService, "evt-open-1")
	registry.UpsertTrafficWithAudit(pb.Traffic{
		TrafficID: "traffic-direct",
		TraceID:   "trace-2",
		State:     pb.TrafficStateOpening,
	}, pb.RouteTargetTypeExternalService, "evt-open-2")
	registry.RecordRejectReason("traffic-direct", "scope_denied")
	registry.RecordFailureWithAudit("traffic-direct", "DIAL_FAILED", "provider_down", "evt-fail-1")

	direct := registry.ListTrafficsByPath(pb.RouteTargetTypeExternalService)
	if len(direct) != 1 || direct[0].Traffic.TrafficID != "traffic-direct" {
		t.Fatalf("unexpected direct traffic list: %+v", direct)
	}
	if direct[0].RejectReason != "scope_denied" || direct[0].LastErrorCode != "DIAL_FAILED" {
		t.Fatalf("unexpected direct traffic details: %+v", direct[0])
	}

	snapshot := registry.Snapshot()
	if len(snapshot.Traffics) != 2 {
		t.Fatalf("unexpected runtime snapshot size: %d", len(snapshot.Traffics))
	}

	traceKey := BuildTraceKey("", "traffic-direct")
	if traceKey != "trace:traffic:traffic-direct" {
		t.Fatalf("unexpected trace key: %s", traceKey)
	}
}
