package testkit

import (
	"encoding/json"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// VersionCase 描述一组 schema 兼容性测试样例。
type VersionCase struct {
	Name         string
	VersionMajor uint32
	VersionMinor uint32
	RawPayload   json.RawMessage
	ShouldAccept bool
}

// GoldenVersionCases 返回协议版本兼容性测试样例集合。
func GoldenVersionCases() []VersionCase {
	hello := GoldenConnectorHello()
	helloRaw, _ := json.Marshal(hello)
	return []VersionCase{
		{
			Name:         "v2_1_current",
			VersionMajor: 2,
			VersionMinor: 1,
			RawPayload:   helloRaw,
			ShouldAccept: true,
		},
		{
			Name:         "v2_0_backward_compatible",
			VersionMajor: 2,
			VersionMinor: 0,
			RawPayload:   helloRaw,
			ShouldAccept: true,
		},
		{
			Name:         "v1_9_unsupported_major",
			VersionMajor: 1,
			VersionMinor: 9,
			RawPayload:   helloRaw,
			ShouldAccept: false,
		},
	}
}

// GoldenEnvelopeWithUnknownFields 返回包含未知字段的兼容性样例。
func GoldenEnvelopeWithUnknownFields() []byte {
	// 模拟未来版本新增字段，验证当前解码器对未知字段保持兼容。
	return []byte(`{
  "versionMajor": 2,
  "versionMinor": 1,
  "messageType": "ConnectorHello",
  "sessionEpoch": 1,
  "payload": {
    "connectorId": "connector-dev-01",
    "namespace": "dev",
    "environment": "alice",
    "futureField": "reserved"
  },
  "futureEnvelopeField": "reserved"
}`)
}

// GoldenFullSyncSnapshot 返回 full-sync 兼容性样例。
func GoldenFullSyncSnapshot() pb.FullSyncSnapshot {
	// 快照样例用于验证 full-sync 与增量同步边界。
	return pb.FullSyncSnapshot{
		RequestID:       "full-sync-001",
		SessionEpoch:    1,
		SnapshotVersion: 10,
		Services: []pb.Service{
			{
				ServiceID:       "svc_01J8Z6C4X9K7M2P4",
				ServiceKey:      "dev/alice/order-service",
				Namespace:       "dev",
				Environment:     "alice",
				ConnectorID:     "connector-dev-01",
				ServiceName:     "order-service",
				Status:          pb.ServiceStatusActive,
				ResourceVersion: 10,
				HealthStatus:    pb.HealthStatusHealthy,
			},
		},
		Routes: []pb.Route{
			{
				RouteID:         "route-001",
				Namespace:       "dev",
				Environment:     "alice",
				ResourceVersion: 10,
				Target: pb.RouteTarget{
					Type: pb.RouteTargetTypeConnectorService,
				},
			},
		},
		Completed: true,
	}
}
