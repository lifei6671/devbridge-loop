package transport

import "testing"

// TestMapSessionStateToProtocolState 验证 transport 状态到协议态映射规则。
func TestMapSessionStateToProtocolState(testingObject *testing.T) {
	testCases := []struct {
		name            string
		state           SessionState
		mappingContext  FailedMappingContext
		expectedOutcome ProtocolState
	}{
		{
			name:            "idle maps to connecting",
			state:           SessionStateIdle,
			expectedOutcome: ProtocolStateConnecting,
		},
		{
			name:  "failed before auth but retrying maps to connecting",
			state: SessionStateFailed,
			mappingContext: FailedMappingContext{
				EverAuthenticated: false,
				Retrying:          true,
				GiveUp:            false,
			},
			expectedOutcome: ProtocolStateConnecting,
		},
		{
			name:  "failed after auth maps to stale",
			state: SessionStateFailed,
			mappingContext: FailedMappingContext{
				EverAuthenticated: true,
				Retrying:          false,
				GiveUp:            false,
			},
			expectedOutcome: ProtocolStateStale,
		},
		{
			name:  "failed before auth and give up maps to closed",
			state: SessionStateFailed,
			mappingContext: FailedMappingContext{
				EverAuthenticated: false,
				Retrying:          false,
				GiveUp:            true,
			},
			expectedOutcome: ProtocolStateClosed,
		},
		{
			name:            "closed maps to closed",
			state:           SessionStateClosed,
			expectedOutcome: ProtocolStateClosed,
		},
	}
	for _, testCase := range testCases {
		// 每个子用例独立验证一个映射分支，方便失败定位。
		testingObject.Run(testCase.name, func(subTestingObject *testing.T) {
			outcome := MapSessionStateToProtocolState(testCase.state, testCase.mappingContext)
			if outcome != testCase.expectedOutcome {
				subTestingObject.Fatalf("expected %s, got %s", testCase.expectedOutcome, outcome)
			}
		})
	}
}

// TestCanTransitionSessionState 验证会话状态机迁移约束。
func TestCanTransitionSessionState(testingObject *testing.T) {
	if !CanTransitionSessionState(SessionStateIdle, SessionStateConnecting) {
		testingObject.Fatalf("expected idle -> connecting transition allowed")
	}
	if CanTransitionSessionState(SessionStateIdle, SessionStateAuthenticated) {
		testingObject.Fatalf("expected idle -> authenticated transition disallowed")
	}
}

// TestCanTransitionTunnelState 验证 tunnel 状态机迁移约束。
func TestCanTransitionTunnelState(testingObject *testing.T) {
	if !CanTransitionTunnelState(TunnelStateReserved, TunnelStateActive) {
		testingObject.Fatalf("expected reserved -> active transition allowed")
	}
	if CanTransitionTunnelState(TunnelStateActive, TunnelStateIdle) {
		testingObject.Fatalf("expected active -> idle transition disallowed")
	}
}

// TestDefaultKeepalivePolicyForBinding 验证不同 binding 的默认 keepalive 策略输出。
func TestDefaultKeepalivePolicyForBinding(testingObject *testing.T) {
	grpcPolicy := DefaultKeepalivePolicyForBinding(BindingTypeGRPCH2)
	if grpcPolicy.IdleTTL <= 0 {
		testingObject.Fatalf("expected grpc idle ttl > 0, got %s", grpcPolicy.IdleTTL)
	}
	if grpcPolicy.ProbeInterval != 0 || grpcPolicy.ProbeTimeout != 0 || grpcPolicy.ProbeMaxFailures != 0 {
		testingObject.Fatalf("unexpected grpc probe policy: %+v", grpcPolicy)
	}

	tcpPolicy := DefaultKeepalivePolicyForBinding(BindingTypeTCPFramed)
	if tcpPolicy.ProbeInterval <= 0 || tcpPolicy.ProbeTimeout <= 0 || tcpPolicy.ProbeMaxFailures <= 0 {
		testingObject.Fatalf("expected tcp probe policy enabled, got %+v", tcpPolicy)
	}

	unknownPolicy := DefaultKeepalivePolicyForBinding(BindingType("unknown_binding"))
	if unknownPolicy.IdleTTL <= 0 {
		testingObject.Fatalf("expected unknown binding fallback policy, got %+v", unknownPolicy)
	}
}

// TestNewBindingInfoIncludesKeepalivePolicy 验证 NewBindingInfo 会填充 keepalive 策略。
func TestNewBindingInfoIncludesKeepalivePolicy(testingObject *testing.T) {
	bindingInfo := NewBindingInfo(BindingTypeTCPFramed)
	if bindingInfo.Type != BindingTypeTCPFramed {
		testingObject.Fatalf("expected tcp binding type, got %s", bindingInfo.Type)
	}
	if bindingInfo.KeepalivePolicy.IdleTTL <= 0 {
		testingObject.Fatalf("expected keepalive policy to be populated, got %+v", bindingInfo.KeepalivePolicy)
	}
}
