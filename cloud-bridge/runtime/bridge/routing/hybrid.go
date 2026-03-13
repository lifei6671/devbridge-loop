package routing

import "github.com/lifei6671/devbridge-loop/ltfp/pb"

// HybridResolver 判定 hybrid 路径是否允许 fallback。
type HybridResolver struct {
	defaultPolicy pb.FallbackPolicy
}

// NewHybridResolver 创建 hybrid 策略判定器。
func NewHybridResolver(defaultPolicy pb.FallbackPolicy) *HybridResolver {
	return &HybridResolver{defaultPolicy: defaultPolicy}
}

// AllowPreOpenFallback 判断当前 fallback policy 是否允许 pre-open fallback。
func (resolver *HybridResolver) AllowPreOpenFallback(policy pb.FallbackPolicy) bool {
	effectivePolicy := policy
	if effectivePolicy == "" {
		effectivePolicy = resolver.defaultPolicy
	}
	if effectivePolicy == "" {
		effectivePolicy = pb.FallbackPolicyPreOpenOnly
	}
	return effectivePolicy == pb.FallbackPolicyPreOpenOnly
}
