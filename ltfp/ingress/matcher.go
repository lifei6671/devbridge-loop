package ingress

import (
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RequestContext 描述 ingress 请求匹配所需字段。
type RequestContext struct {
	Protocol   string
	Host       string
	Authority  string
	Path       string
	SNI        string
	ListenPort uint32
}

// RouteMapping 描述 route 到 service/exposure/listener 的关联结果。
type RouteMapping struct {
	RouteID     string
	ServiceID   string
	ServiceKey  string
	TargetType  pb.RouteTargetType
	IngressMode pb.IngressMode
	Host        string
	SNI         string
	ListenPort  uint32
}

// MatchRoute 判断请求上下文是否命中 route 匹配条件。
func MatchRoute(route pb.Route, request RequestContext) (bool, string) {
	match := route.Match
	if !matchStringField(match.Protocol, request.Protocol) {
		return false, "protocol_mismatch"
	}
	if !matchStringField(match.Host, request.Host) {
		return false, "host_mismatch"
	}
	if !matchStringField(match.Authority, request.Authority) {
		return false, "authority_mismatch"
	}
	if !matchPathPrefix(match.PathPrefix, request.Path) {
		return false, "path_prefix_mismatch"
	}
	if !matchStringField(match.SNI, request.SNI) {
		return false, "sni_mismatch"
	}
	if match.ListenPort > 0 && match.ListenPort != request.ListenPort {
		// listen_port 一旦配置必须精确匹配。
		return false, "listen_port_mismatch"
	}
	return true, ""
}

// ValidateDedicatedPortAssignments 校验 l4_dedicated_port 的端口唯一性。
func ValidateDedicatedPortAssignments(services []pb.Service) error {
	portToServiceID := make(map[uint32]string)
	for _, service := range services {
		exposure := service.Exposure
		if exposure.IngressMode != pb.IngressModeL4DedicatedPort {
			continue
		}
		if exposure.ListenPort == 0 {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "l4_dedicated_port requires exposure.listenPort")
		}
		existingServiceID, exists := portToServiceID[exposure.ListenPort]
		if !exists {
			portToServiceID[exposure.ListenPort] = service.ServiceID
			continue
		}
		if existingServiceID != service.ServiceID {
			// 裸 TCP 场景必须一服务一端口，冲突直接拒绝。
			return ltfperrors.New(
				ltfperrors.CodeIngressPortConflict,
				fmt.Sprintf("dedicated port conflict: port=%d serviceA=%s serviceB=%s", exposure.ListenPort, existingServiceID, service.ServiceID),
			)
		}
	}
	return nil
}

// BuildRouteMappings 构建 route 与 service/exposure/listener 关联映射。
func BuildRouteMappings(routes []pb.Route, services []pb.Service) []RouteMapping {
	serviceByKey := make(map[string]pb.Service, len(services))
	for _, service := range services {
		// service_key 是 route target 的 lookup key。
		serviceByKey[strings.TrimSpace(service.ServiceKey)] = service
	}
	mappings := make([]RouteMapping, 0, len(routes))
	for _, route := range routes {
		service, serviceKey := resolveTargetService(route, serviceByKey)
		mappings = append(mappings, RouteMapping{
			RouteID:     route.RouteID,
			ServiceID:   service.ServiceID,
			ServiceKey:  serviceKey,
			TargetType:  route.Target.Type,
			IngressMode: service.Exposure.IngressMode,
			Host:        service.Exposure.Host,
			SNI:         service.Exposure.SNIName,
			ListenPort:  service.Exposure.ListenPort,
		})
	}
	return mappings
}

// matchStringField 判断 route 字段与请求字段是否匹配。
func matchStringField(routeField string, requestField string) bool {
	normalizedRouteField := strings.TrimSpace(routeField)
	if normalizedRouteField == "" {
		// route 字段为空表示不限制该维度。
		return true
	}
	return strings.EqualFold(normalizedRouteField, strings.TrimSpace(requestField))
}

// matchPathPrefix 判断 pathPrefix 与请求 path 是否匹配。
func matchPathPrefix(pathPrefix string, path string) bool {
	normalizedPrefix := strings.TrimSpace(pathPrefix)
	if normalizedPrefix == "" {
		// 未配置 pathPrefix 时不做路径限制。
		return true
	}
	normalizedPath := strings.TrimSpace(path)
	return strings.HasPrefix(normalizedPath, normalizedPrefix)
}

// resolveTargetService 根据 route target 提取关联 service。
func resolveTargetService(route pb.Route, serviceByKey map[string]pb.Service) (pb.Service, string) {
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		if route.Target.ConnectorService == nil {
			return pb.Service{}, ""
		}
		serviceKey := strings.TrimSpace(route.Target.ConnectorService.ServiceKey)
		return serviceByKey[serviceKey], serviceKey
	case pb.RouteTargetTypeHybridGroup:
		if route.Target.HybridGroup == nil {
			return pb.Service{}, ""
		}
		serviceKey := strings.TrimSpace(route.Target.HybridGroup.PrimaryConnectorService.ServiceKey)
		return serviceByKey[serviceKey], serviceKey
	default:
		// external_service 没有本地 service 绑定。
		return pb.Service{}, ""
	}
}
