package testkit

import (
	"encoding/json"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// GoldenConnectorHello 返回可复用的握手测试样例。
func GoldenConnectorHello() pb.ConnectorHello {
	// 固定样例值，保证不同模块测试输入一致。
	return pb.ConnectorHello{
		ConnectorID:       "connector-dev-01",
		Namespace:         "dev",
		Environment:       "alice",
		NodeName:          "node-a",
		Version:           "v2.1.0",
		SupportedBindings: []string{"http", "masque"},
		Capabilities:      []string{"publish_service", "traffic_open"},
	}
}

// GoldenPublishService 返回可复用的服务发布测试样例。
func GoldenPublishService() pb.PublishService {
	// 使用单 endpoint 样例覆盖首批最小闭环场景。
	return pb.PublishService{
		ServiceID:   "svc_01J8Z6C4X9K7M2P4",
		ServiceKey:  "dev/alice/order-service",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-1",
				Protocol:   "http",
				Host:       "127.0.0.1",
				Port:       18080,
			},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			Host:        "api.dev.example.com",
			PathPrefix:  "/order",
			AllowExport: true,
		},
	}
}

// GoldenTrafficOpen 返回可复用的 TrafficOpen 测试样例。
func GoldenTrafficOpen() pb.TrafficOpen {
	// endpointSelectionHint 仅作提示，不携带权威 target 地址。
	return pb.TrafficOpen{
		TrafficID:    "traffic-001",
		RouteID:      "route-001",
		ServiceID:    "svc_01J8Z6C4X9K7M2P4",
		SourceAddr:   "10.0.0.1:54000",
		ProtocolHint: "http",
		TraceID:      "trace-001",
		EndpointSelectionHint: map[string]string{
			"zone": "az-a",
		},
	}
}

// GoldenTrafficOpenAckSuccess 返回可复用的成功 ACK 样例。
func GoldenTrafficOpenAckSuccess() pb.TrafficOpenAck {
	// success=true 表示 pre-open 阶段已经通过。
	return pb.TrafficOpenAck{
		TrafficID: "traffic-001",
		Success:   true,
	}
}

// GoldenControlEnvelope 构造控制面封装 golden 样例。
func GoldenControlEnvelope(messageType pb.ControlMessageType, payload any) (pb.ControlEnvelope, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		// 测试夹具在编码失败时直接返回错误给测试调用方。
		return pb.ControlEnvelope{}, err
	}

	// 固定元信息，便于跨模块复用同一组断言。
	return pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     messageType,
		RequestID:       "req-001",
		SessionID:       "session-001",
		SessionEpoch:    1,
		ConnectorID:     "connector-dev-01",
		EventID:         "event-001",
		ResourceVersion: 1,
		Payload:         rawPayload,
	}, nil
}

// GoldenHeartbeat 返回可复用的心跳样例。
func GoldenHeartbeat() pb.Heartbeat {
	// 使用当前时间戳模拟真实心跳上报场景。
	return pb.Heartbeat{
		TimestampUnix: time.Now().Unix(),
		SessionState:  pb.SessionStateActive,
		LoadHint:      "normal",
	}
}
