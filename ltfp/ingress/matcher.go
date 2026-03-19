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

// RouteMapping 描述 route 到 logical service / instance / exposure / listener 的关联结果。
type RouteMapping struct {
	RouteID          string
	LogicalServiceID string
	InstanceID       string
	TargetType       pb.RouteTargetType
	IngressMode      pb.IngressMode
	Host             string
	SNI              string
	ListenPort       uint32
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
		return false, "listen_port_mismatch"
	}
	return true, ""
}

// ValidateDedicatedPortAssignments 校验 l4_dedicated_port 的端口唯一性。
func ValidateDedicatedPortAssignments(instances []pb.ServiceInstance) error {
	portToLogicalServiceID := make(map[uint32]string)
	for _, instance := range instances {
		exposure := instance.Exposure
		if exposure.IngressMode != pb.IngressModeL4DedicatedPort {
			continue
		}
		if exposure.ListenPort == 0 {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "l4_dedicated_port requires exposure.listenPort")
		}
		existingLogicalServiceID, exists := portToLogicalServiceID[exposure.ListenPort]
		if !exists {
			portToLogicalServiceID[exposure.ListenPort] = instance.LogicalServiceID
			continue
		}
		if existingLogicalServiceID != instance.LogicalServiceID {
			return ltfperrors.New(
				ltfperrors.CodeIngressPortConflict,
				fmt.Sprintf("dedicated port conflict: port=%d logicalServiceA=%s logicalServiceB=%s", exposure.ListenPort, existingLogicalServiceID, instance.LogicalServiceID),
			)
		}
	}
	return nil
}

// BuildRouteMappings 构建 route 与 logical service / instance / exposure / listener 关联映射。
func BuildRouteMappings(routes []pb.Route, logicalServices []pb.LogicalService, instances []pb.ServiceInstance) []RouteMapping {
	logicalServiceByID := make(map[string]pb.LogicalService, len(logicalServices))
	for _, logicalService := range logicalServices {
		logicalServiceByID[strings.TrimSpace(logicalService.LogicalServiceID)] = logicalService
	}
	instanceByLogicalServiceID := make(map[string]pb.ServiceInstance)
	for _, instance := range instances {
		logicalServiceID := strings.TrimSpace(instance.LogicalServiceID)
		if logicalServiceID == "" {
			continue
		}
		if _, exists := instanceByLogicalServiceID[logicalServiceID]; !exists {
			instanceByLogicalServiceID[logicalServiceID] = instance
		}
	}
	mappings := make([]RouteMapping, 0, len(routes))
	for _, route := range routes {
		logicalServiceID, instance := resolveTargetInstance(route, logicalServiceByID, instanceByLogicalServiceID)
		mappings = append(mappings, RouteMapping{
			RouteID:          route.RouteID,
			LogicalServiceID: logicalServiceID,
			InstanceID:       instance.InstanceID,
			TargetType:       route.Target.Type,
			IngressMode:      instance.Exposure.IngressMode,
			Host:             instance.Exposure.Host,
			SNI:              instance.Exposure.SNIName,
			ListenPort:       instance.Exposure.ListenPort,
		})
	}
	return mappings
}

// matchStringField 判断 route 字段与请求字段是否匹配。
func matchStringField(routeField string, requestField string) bool {
	normalizedRouteField := strings.TrimSpace(routeField)
	if normalizedRouteField == "" {
		return true
	}
	return strings.EqualFold(normalizedRouteField, strings.TrimSpace(requestField))
}

// matchPathPrefix 判断 pathPrefix 与请求 path 是否匹配。
func matchPathPrefix(pathPrefix string, path string) bool {
	normalizedPrefix := strings.TrimSpace(pathPrefix)
	if normalizedPrefix == "" {
		return true
	}
	normalizedPath := strings.TrimSpace(path)
	return strings.HasPrefix(normalizedPath, normalizedPrefix)
}

// resolveTargetInstance 根据 route target 提取关联 logical service 与实例。
func resolveTargetInstance(
	route pb.Route,
	logicalServiceByID map[string]pb.LogicalService,
	instanceByLogicalServiceID map[string]pb.ServiceInstance,
) (string, pb.ServiceInstance) {
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		if route.Target.ConnectorService == nil {
			return "", pb.ServiceInstance{}
		}
		return resolveTargetSelector(route.Target.ConnectorService.Selector, logicalServiceByID, instanceByLogicalServiceID)
	default:
		return "", pb.ServiceInstance{}
	}
}

// resolveTargetSelector 根据 selector 提取关联 logical service 与实例。
func resolveTargetSelector(
	selector pb.ServiceSelector,
	logicalServiceByID map[string]pb.LogicalService,
	instanceByLogicalServiceID map[string]pb.ServiceInstance,
) (string, pb.ServiceInstance) {
	logicalServiceID := strings.TrimSpace(selector.LogicalServiceID)
	if logicalServiceID == "" {
		for candidateID, logicalService := range logicalServiceByID {
			if strings.TrimSpace(logicalService.ServiceName) == strings.TrimSpace(selector.ServiceName) &&
				strings.TrimSpace(logicalService.Scope.Namespace) == strings.TrimSpace(selector.Scope.Namespace) &&
				strings.TrimSpace(logicalService.Scope.Environment) == strings.TrimSpace(selector.Scope.Environment) {
				logicalServiceID = candidateID
				break
			}
		}
	}
	if logicalServiceID == "" {
		return "", pb.ServiceInstance{}
	}
	return logicalServiceID, instanceByLogicalServiceID[logicalServiceID]
}
