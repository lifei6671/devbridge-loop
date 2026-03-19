package registry

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCanonicalRegistryLogicalServiceLookup 验证 service_name + scope 到 logical_service_id 映射查询。
func TestCanonicalRegistryLogicalServiceLookup(t *testing.T) {
	t.Parallel()

	registry := NewCanonicalRegistry()
	service := pb.LogicalService{
		LogicalServiceID: "ls-001",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
	}
	registry.UpsertLogicalService(service)

	loaded, exists := registry.FindLogicalServiceByNameScope("order-service", pb.Scope{Namespace: "dev", Environment: "alice"})
	if !exists {
		t.Fatalf("expected logical service exists")
	}
	if loaded.LogicalServiceID != "ls-001" {
		t.Fatalf("unexpected logical service id: %s", loaded.LogicalServiceID)
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
	registry.UpsertLogicalServiceWithAudit(pb.LogicalService{
		LogicalServiceID: "ls-001",
		ServiceName:      "order-service",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		Status:           pb.ServiceStatusActive,
		ResourceVersion:  33,
	}, "evt-ls-1", 33)
	registry.UpsertServiceInstanceWithAudit(pb.ServiceInstance{
		InstanceID:       "si-001",
		LogicalServiceID: "ls-001",
		ConnectorID:      "conn-001",
		InstanceStatus:   pb.ServiceStatusActive,
		HealthStatus:     pb.HealthStatusHealthy,
		ResourceVersion:  34,
	}, "evt-si-1", 34)
	registry.UpsertRouteWithAudit(pb.Route{
		RouteID:         "route-001",
		Scope:           pb.Scope{Namespace: "dev", Environment: "alice"},
		ResourceVersion: 44,
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				Selector: pb.ServiceSelector{
					LogicalServiceID: "ls-001",
				},
			},
		},
	}, "evt-route-1", 44)
	registry.UpsertProjectionWithAudit(pb.DiscoveryProjection{
		ProjectionID:     "proj-001",
		LogicalServiceID: "ls-001",
		InstanceID:       "si-001",
		Provider:         "nacos",
	}, "evt-proj-1", 55)

	service, exists := registry.FindLogicalServiceByNameScope("order-service", pb.Scope{Namespace: "dev", Environment: "alice"})
	if !exists || service.LogicalServiceID != "ls-001" {
		t.Fatalf("unexpected logical service mapping: service=%+v exists=%v", service, exists)
	}

	sessions := registry.ListSessionsByConnector("conn-001")
	if len(sessions) != 1 {
		t.Fatalf("unexpected session count: %d", len(sessions))
	}
	instances := registry.ListServiceInstancesByConnector("conn-001")
	if len(instances) != 1 {
		t.Fatalf("unexpected instance count: %d", len(instances))
	}
	routes := registry.ListRoutesByLogicalService("ls-001")
	if len(routes) != 1 || routes[0].RouteID != "route-001" {
		t.Fatalf("unexpected route list: %+v", routes)
	}

	audit, exists := registry.GetAuditInfo("logical_service", "ls-001")
	if !exists {
		t.Fatalf("expected logical service audit exists")
	}
	if audit.LastResourceVersion != 33 || audit.LastEventID != "evt-ls-1" {
		t.Fatalf("unexpected audit info: %+v", audit)
	}

	snapshot := registry.Snapshot()
	if len(snapshot.Connectors) != 1 || len(snapshot.Sessions) != 1 || len(snapshot.LogicalServices) != 1 || len(snapshot.ServiceInstances) != 1 || len(snapshot.Routes) != 1 || len(snapshot.Projections) != 1 {
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
