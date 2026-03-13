package routing

import (
	"sort"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const routeMetadataIngressModeKey = "ingress_mode"

type scoredRoute struct {
	route pb.Route
	score int
}

// Matcher 按入口模式和 RouteMatch 规则筛选候选路由。
type Matcher struct{}

// NewMatcher 创建路由匹配器。
func NewMatcher() *Matcher {
	return &Matcher{}
}

// Match 返回按优先级排序的候选路由（高优先级在前）。
func (matcher *Matcher) Match(request ingress.RouteLookupRequest, routes []pb.Route) []pb.Route {
	_ = matcher
	normalizedRequest := normalizeLookupRequest(request)
	candidates := make([]scoredRoute, 0, len(routes))
	for _, route := range routes {
		if resolveRouteIngressMode(route) != normalizedRequest.IngressMode {
			// 入口模式不一致时直接跳过，避免三类入口串扰。
			continue
		}
		score, matched := scoreRouteMatch(normalizedRequest, route.Match)
		if !matched {
			continue
		}
		candidates = append(candidates, scoredRoute{
			route: route,
			score: score,
		})
	}
	sort.Slice(candidates, func(leftIndex, rightIndex int) bool {
		left := candidates[leftIndex]
		right := candidates[rightIndex]
		if left.route.Priority != right.route.Priority {
			return left.route.Priority > right.route.Priority
		}
		if left.score != right.score {
			return left.score > right.score
		}
		return left.route.RouteID < right.route.RouteID
	})
	result := make([]pb.Route, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.route)
	}
	return result
}

func normalizeLookupRequest(request ingress.RouteLookupRequest) ingress.RouteLookupRequest {
	normalized := request
	normalized.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	normalized.Host = strings.ToLower(strings.TrimSpace(request.Host))
	normalized.Authority = strings.ToLower(strings.TrimSpace(request.Authority))
	normalized.Path = normalizePath(request.Path)
	normalized.SNI = strings.ToLower(strings.TrimSpace(request.SNI))
	normalized.Namespace = strings.TrimSpace(request.Namespace)
	normalized.Environment = strings.TrimSpace(request.Environment)
	return normalized
}

func scoreRouteMatch(request ingress.RouteLookupRequest, match pb.RouteMatch) (int, bool) {
	score := 0
	if normalizedProtocol := strings.ToLower(strings.TrimSpace(match.Protocol)); normalizedProtocol != "" {
		if normalizedProtocol != request.Protocol {
			return 0, false
		}
		score += 8
	}
	if normalizedHost := strings.ToLower(strings.TrimSpace(match.Host)); normalizedHost != "" {
		if normalizedHost != request.Host {
			return 0, false
		}
		score += 10
	}
	if normalizedAuthority := strings.ToLower(strings.TrimSpace(match.Authority)); normalizedAuthority != "" {
		if normalizedAuthority != request.Authority {
			return 0, false
		}
		score += 10
	}
	if match.ListenPort != 0 {
		if match.ListenPort != request.ListenPort {
			return 0, false
		}
		score += 6
	}
	if normalizedPrefix := normalizePath(match.PathPrefix); strings.TrimSpace(match.PathPrefix) != "" {
		if !strings.HasPrefix(request.Path, normalizedPrefix) {
			return 0, false
		}
		score += len(normalizedPrefix)
	}
	if normalizedSNI := strings.ToLower(strings.TrimSpace(match.SNI)); normalizedSNI != "" {
		if normalizedSNI != request.SNI {
			return 0, false
		}
		score += 10
	}
	return score, true
}

func resolveRouteIngressMode(route pb.Route) pb.IngressMode {
	if modeValue, exists := route.Metadata[routeMetadataIngressModeKey]; exists {
		normalizedMode := pb.IngressMode(strings.TrimSpace(modeValue))
		switch normalizedMode {
		case pb.IngressModeL7Shared, pb.IngressModeTLSSNIShared, pb.IngressModeL4DedicatedPort:
			return normalizedMode
		}
	}
	normalizedProtocol := strings.ToLower(strings.TrimSpace(route.Match.Protocol))
	if strings.TrimSpace(route.Match.SNI) != "" {
		return pb.IngressModeTLSSNIShared
	}
	if route.Match.ListenPort != 0 &&
		strings.TrimSpace(route.Match.Host) == "" &&
		strings.TrimSpace(route.Match.Authority) == "" &&
		strings.TrimSpace(route.Match.PathPrefix) == "" &&
		(normalizedProtocol == "" || normalizedProtocol == "tcp") {
		return pb.IngressModeL4DedicatedPort
	}
	return pb.IngressModeL7Shared
}

func normalizePath(path string) string {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return "/"
	}
	if !strings.HasPrefix(normalized, "/") {
		return "/" + normalized
	}
	return normalized
}
