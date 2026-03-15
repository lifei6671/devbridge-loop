package routing

import (
	"net"
	"sort"
	"strconv"
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
	normalized.Host = normalizeRouteHostValue(request.Host)
	normalized.Authority = normalizeRouteAuthorityValue(request.Authority)
	if normalized.Authority == "" && normalized.Host != "" {
		normalized.Authority = normalized.Host
	}
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
	if normalizedHost := normalizeRouteHostValue(match.Host); normalizedHost != "" {
		if normalizedHost != request.Host {
			return 0, false
		}
		score += 10
	}
	if normalizedAuthority := normalizeRouteAuthorityValue(match.Authority); normalizedAuthority != "" {
		if !routeAuthorityMatches(normalizedAuthority, request.Authority, request.Host) {
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

func routeAuthorityMatches(routeAuthority string, requestAuthority string, requestHost string) bool {
	normalizedRouteAuthority := normalizeRouteAuthorityValue(routeAuthority)
	normalizedRequestAuthority := normalizeRouteAuthorityValue(requestAuthority)
	if normalizedRouteAuthority == normalizedRequestAuthority {
		return true
	}
	routePort, hasRoutePort := extractAuthorityPort(normalizedRouteAuthority)
	requestPort, hasRequestPort := extractAuthorityPort(normalizedRequestAuthority)
	if hasRoutePort && hasRequestPort && routePort != requestPort {
		return false
	}
	routeHost := normalizeRouteHostValue(normalizedRouteAuthority)
	requestAuthorityHost := normalizeRouteHostValue(normalizedRequestAuthority)
	if requestAuthorityHost == "" {
		requestAuthorityHost = normalizeRouteHostValue(requestHost)
	}
	return routeHost != "" && routeHost == requestAuthorityHost
}

func normalizeRouteAuthorityValue(authority string) string {
	normalized := strings.ToLower(strings.TrimSpace(authority))
	if normalized == "" {
		return ""
	}
	host, port, splitErr := net.SplitHostPort(normalized)
	if splitErr != nil {
		// authority 可能是 host 或 host:port（非标准格式），统一返回 host 去尾点的小写串。
		if rawHost, rawPort, found := strings.Cut(normalized, ":"); found && !strings.Contains(rawHost, ":") && isNumericPort(rawPort) {
			return strings.TrimSuffix(strings.TrimSpace(rawHost), ".") + ":" + rawPort
		}
		return strings.TrimSuffix(normalized, ".")
	}
	normalizedHost := normalizeRouteHostValue(host)
	if normalizedHost == "" {
		return ""
	}
	return net.JoinHostPort(normalizedHost, port)
}

func normalizeRouteHostValue(host string) string {
	normalized := strings.ToLower(strings.TrimSpace(host))
	if normalized == "" {
		return ""
	}
	parsedHost, _, splitErr := net.SplitHostPort(normalized)
	if splitErr == nil {
		normalized = parsedHost
	} else if rawHost, rawPort, found := strings.Cut(normalized, ":"); found && !strings.Contains(rawHost, ":") && isNumericPort(rawPort) {
		normalized = rawHost
	}
	normalized = strings.TrimPrefix(normalized, "[")
	normalized = strings.TrimSuffix(normalized, "]")
	return strings.TrimSuffix(normalized, ".")
}

func isNumericPort(value string) bool {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return false
	}
	port, parseErr := strconv.Atoi(normalized)
	if parseErr != nil {
		return false
	}
	return port >= 0 && port <= 65535
}

func extractAuthorityPort(authority string) (string, bool) {
	normalized := strings.TrimSpace(authority)
	if normalized == "" {
		return "", false
	}
	if _, port, splitErr := net.SplitHostPort(normalized); splitErr == nil {
		return port, true
	}
	_, port, found := strings.Cut(normalized, ":")
	if !found || !isNumericPort(port) {
		return "", false
	}
	return strings.TrimSpace(port), true
}
