package routing

import (
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const routeMetadataIngressModeKey = "ingress_mode"

type scoredRoute struct {
	route pb.Route
	score int
}

// Matcher 按入口模式和 RouteMatch 规则筛选候选路由。
type Matcher struct {
	regexCache sync.Map
}

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
			continue
		}
		score, matched := matcher.scoreRouteMatch(normalizedRequest, route.Match)
		if !matched {
			continue
		}
		candidates = append(candidates, scoredRoute{route: route, score: score})
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
	normalized.Headers = normalizeLookupHeaders(request.Headers)
	normalized.Queries = normalizeLookupQueryParams(request.Queries)
	return normalized
}

func (matcher *Matcher) scoreRouteMatch(request ingress.RouteLookupRequest, match pb.RouteMatch) (int, bool) {
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
	if normalizedQueryMatchers := normalizeRouteQueryMatchers(match.Queries); len(normalizedQueryMatchers) > 0 {
		if !matcher.matchLookupQueries(normalizedQueryMatchers, request.Queries) {
			return 0, false
		}
		score += len(normalizedQueryMatchers) * 5
	}
	if normalizedHeaderMatchers := normalizeRouteHeaderMatchers(match.Headers); len(normalizedHeaderMatchers) > 0 {
		if !matcher.matchLookupHeaders(normalizedHeaderMatchers, request.Headers) {
			return 0, false
		}
		score += len(normalizedHeaderMatchers) * 6
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

func normalizeLookupHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	normalized := make(map[string][]string, len(headers))
	for headerName, headerValues := range headers {
		normalizedHeaderName := strings.ToLower(strings.TrimSpace(headerName))
		if normalizedHeaderName == "" {
			continue
		}
		values := normalized[normalizedHeaderName]
		for _, headerValue := range headerValues {
			normalizedHeaderValue := strings.TrimSpace(headerValue)
			if normalizedHeaderValue == "" {
				continue
			}
			values = append(values, normalizedHeaderValue)
		}
		if len(values) == 0 {
			continue
		}
		normalized[normalizedHeaderName] = values
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeRouteHeaderMatchers(headerMatchers []pb.HeaderMatcher) []pb.HeaderMatcher {
	if len(headerMatchers) == 0 {
		return nil
	}
	normalized := make([]pb.HeaderMatcher, 0, len(headerMatchers))
	for _, headerMatcher := range headerMatchers {
		normalizedName := strings.ToLower(strings.TrimSpace(headerMatcher.Name))
		if normalizedName == "" {
			continue
		}
		normalized = append(normalized, pb.HeaderMatcher{
			Name:    normalizedName,
			Exact:   strings.TrimSpace(headerMatcher.Exact),
			Prefix:  strings.TrimSpace(headerMatcher.Prefix),
			Regex:   strings.TrimSpace(headerMatcher.Regex),
			Present: headerMatcher.Present,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeLookupQueryParams(queries map[string][]string) map[string][]string {
	if len(queries) == 0 {
		return nil
	}
	normalized := make(map[string][]string, len(queries))
	for queryName, queryValues := range queries {
		normalizedQueryName := strings.TrimSpace(queryName)
		if normalizedQueryName == "" {
			continue
		}
		values := normalized[normalizedQueryName]
		for _, queryValue := range queryValues {
			values = append(values, strings.TrimSpace(queryValue))
		}
		normalized[normalizedQueryName] = values
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeRouteQueryMatchers(queryMatchers []pb.QueryMatcher) []pb.QueryMatcher {
	if len(queryMatchers) == 0 {
		return nil
	}
	normalized := make([]pb.QueryMatcher, 0, len(queryMatchers))
	for _, queryMatcher := range queryMatchers {
		normalizedName := strings.TrimSpace(queryMatcher.Name)
		if normalizedName == "" {
			continue
		}
		normalized = append(normalized, pb.QueryMatcher{
			Name:    normalizedName,
			Exact:   strings.TrimSpace(queryMatcher.Exact),
			Prefix:  strings.TrimSpace(queryMatcher.Prefix),
			Regex:   strings.TrimSpace(queryMatcher.Regex),
			Present: queryMatcher.Present,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (matcher *Matcher) matchLookupHeaders(routeHeaderMatchers []pb.HeaderMatcher, requestHeaders map[string][]string) bool {
	return matcher.matchLookupKeyValues(routeHeaderMatchers, requestHeaders, false)
}

func (matcher *Matcher) matchLookupQueries(routeQueryMatchers []pb.QueryMatcher, requestQueries map[string][]string) bool {
	if len(routeQueryMatchers) == 0 {
		return true
	}
	adapted := make([]pb.HeaderMatcher, 0, len(routeQueryMatchers))
	for _, queryMatcher := range routeQueryMatchers {
		adapted = append(adapted, pb.HeaderMatcher(queryMatcher))
	}
	return matcher.matchLookupKeyValues(adapted, requestQueries, true)
}

func (matcher *Matcher) matchLookupKeyValues(routeMatchers []pb.HeaderMatcher, requestValues map[string][]string, caseSensitiveName bool) bool {
	if len(routeMatchers) == 0 {
		return true
	}
	if len(requestValues) == 0 {
		for _, routeMatcher := range routeMatchers {
			if routeMatcher.Present != nil && !*routeMatcher.Present {
				continue
			}
			return false
		}
		return true
	}
	for _, routeMatcher := range routeMatchers {
		requestMatcherValues, exists := lookupKeyValues(requestValues, routeMatcher.Name, caseSensitiveName)
		if !exists || len(requestMatcherValues) == 0 {
			if routeMatcher.Present != nil && !*routeMatcher.Present {
				continue
			}
			return false
		}
		if !matchLookupValue(matcher, routeMatcher, requestMatcherValues) {
			return false
		}
	}
	return true
}

func lookupKeyValues(values map[string][]string, key string, caseSensitiveName bool) ([]string, bool) {
	if !caseSensitiveName {
		requestValues, exists := values[key]
		return requestValues, exists
	}
	for candidateKey, candidateValues := range values {
		if strings.TrimSpace(candidateKey) == strings.TrimSpace(key) {
			return candidateValues, true
		}
	}
	return nil, false
}

func matchLookupValue(matcher *Matcher, routeMatcher pb.HeaderMatcher, requestValues []string) bool {
	if routeMatcher.Present != nil {
		return *routeMatcher.Present == (len(requestValues) > 0)
	}
	for _, requestValue := range requestValues {
		switch {
		case routeMatcher.Exact != "":
			if requestValue == routeMatcher.Exact {
				return true
			}
		case routeMatcher.Prefix != "":
			if strings.HasPrefix(requestValue, routeMatcher.Prefix) {
				return true
			}
		case routeMatcher.Regex != "":
			if compiled := matcher.lookupCompiledRegex(routeMatcher.Regex); compiled != nil && compiled.MatchString(requestValue) {
				return true
			}
		default:
			if requestValue != "" {
				return true
			}
		}
	}
	return false
}

func (matcher *Matcher) lookupCompiledRegex(pattern string) *regexp.Regexp {
	if matcher == nil {
		return nil
	}
	normalizedPattern := strings.TrimSpace(pattern)
	if normalizedPattern == "" {
		return nil
	}
	if cached, exists := matcher.regexCache.Load(normalizedPattern); exists {
		compiled, _ := cached.(*regexp.Regexp)
		return compiled
	}
	compiled, err := regexp.Compile(normalizedPattern)
	if err != nil {
		return nil
	}
	actual, _ := matcher.regexCache.LoadOrStore(normalizedPattern, compiled)
	stored, _ := actual.(*regexp.Regexp)
	return stored
}
