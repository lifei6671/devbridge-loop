package testkit

import "github.com/lifei6671/devbridge-loop/ltfp/pb"

// GoldenForwardIntentPortMapping 返回端口映射模式的转发意图样例。
func GoldenForwardIntentPortMapping() pb.ForwardIntent {
	// 该样例覆盖 port_mapping 模式下 scope 与 serviceName 绑定语义。
	return pb.ForwardIntent{
		RequestID:    "forward-req-001",
		TrafficID:    "traffic-001",
		Mode:         pb.ForwardModePortMapping,
		Namespace:    "dev",
		Environment:  "alice",
		ServiceName:  "order-service",
		ProtocolHint: "tcp",
		SourceOrder: []pb.ForwardSource{
			pb.ForwardSourceTunnel,
			pb.ForwardSourceLocalRoute,
			pb.ForwardSourceServiceDiscovery,
		},
	}
}

// GoldenForwardIntentAutoNegotiation 返回自动协商模式的转发意图样例。
func GoldenForwardIntentAutoNegotiation() pb.ForwardIntent {
	// 该样例覆盖 auto_negotiation 模式下 negotiationProfile 语义。
	return pb.ForwardIntent{
		RequestID: "forward-req-002",
		TrafficID: "traffic-002",
		Mode:      pb.ForwardModeAutoNegotiation,
		NegotiationProfile: pb.NegotiationProfile{
			VersionMajor:     2,
			VersionMinor:     1,
			RequiredFeatures: []string{"session_epoch"},
			OptionalFeatures: []string{"port_mapping_forward"},
		},
	}
}

// GoldenForwardDecisionAccepted 返回成功决策样例。
func GoldenForwardDecisionAccepted() pb.ForwardDecision {
	// 成功决策应包含 selectedSource 和协商结果。
	return pb.ForwardDecision{
		Accepted:       true,
		RequestID:      "forward-req-002",
		TrafficID:      "traffic-002",
		Mode:           pb.ForwardModeAutoNegotiation,
		SelectedSource: pb.ForwardSourceTunnel,
		NegotiationResult: pb.NegotiationResult{
			Accepted:           true,
			NegotiatedFeatures: []string{"session_epoch"},
		},
	}
}
