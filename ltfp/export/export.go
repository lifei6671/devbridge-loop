package export

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// EligibilityInput 描述 export 准入检查输入。
type EligibilityInput struct {
	Connector           pb.Connector
	Session             pb.Session
	Service             pb.Service
	HasReachableIngress bool
}

// EndpointBuildOptions 描述 export endpoint 生成参数。
type EndpointBuildOptions struct {
	GatewayHost string
	SharedPort  uint32
}

// EndpointBuildResult 描述 export endpoint 生成结果。
type EndpointBuildResult struct {
	Address  string
	Metadata map[string]string
}

// ReconcilePlan 描述 projection create/update/delete 计划。
type ReconcilePlan struct {
	Create []pb.DiscoveryProjection
	Update []pb.DiscoveryProjection
	Delete []pb.DiscoveryProjection
}

// CheckEligibility 校验 service 是否满足 export 准入条件。
func CheckEligibility(input EligibilityInput) error {
	service := input.Service
	connector := input.Connector
	session := input.Session
	if isOfflineStatus(connector.Status) {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "connector is offline")
	}
	if strings.TrimSpace(session.SessionID) == "" || session.State != pb.SessionStateActive {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "active session is required")
	}
	if strings.TrimSpace(session.ConnectorID) != strings.TrimSpace(service.ConnectorID) {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "session connector does not match service connector")
	}
	if service.Status != pb.ServiceStatusActive {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "service is not active")
	}
	if service.HealthStatus != pb.HealthStatusHealthy {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "service is not healthy")
	}
	if !service.DiscoveryPolicy.Enabled {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "discovery_policy.enabled must be true")
	}
	if !service.Exposure.AllowExport {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "exposure.allow_export must be true")
	}
	if !input.HasReachableIngress {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "server reachable ingress is not ready")
	}
	return nil
}

// BuildEndpoint 基于 ingress mode 生成 export endpoint。
func BuildEndpoint(service pb.Service, options EndpointBuildOptions) (EndpointBuildResult, error) {
	switch service.Exposure.IngressMode {
	case pb.IngressModeL7Shared:
		// l7_shared 导出共享域名和端口。
		host := strings.TrimSpace(service.Exposure.Host)
		if host == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.host is required for l7_shared")
		}
		port := resolvePort(service.Exposure.ListenPort, options.SharedPort)
		address := fmt.Sprintf("%s:%d", host, port)
		if matchesUpstreamEndpoint(address, service.Endpoints) {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeExportNotEligible, "l7 export address must not equal upstream endpoint")
		}
		return EndpointBuildResult{
			Address: address,
			Metadata: map[string]string{
				"ingress_mode": string(pb.IngressModeL7Shared),
			},
		}, nil
	case pb.IngressModeTLSSNIShared:
		// tls_sni_shared 导出 server reachable address，并附带 sni metadata。
		gatewayHost := strings.TrimSpace(options.GatewayHost)
		if gatewayHost == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "gatewayHost is required for tls_sni_shared")
		}
		port := resolvePort(service.Exposure.ListenPort, options.SharedPort)
		sni := strings.TrimSpace(service.Exposure.SNIName)
		if sni == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.sniName is required for tls_sni_shared")
		}
		address := fmt.Sprintf("%s:%d", gatewayHost, port)
		if matchesUpstreamEndpoint(address, service.Endpoints) {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeExportNotEligible, "tls_sni export address must not equal upstream endpoint")
		}
		return EndpointBuildResult{
			Address: address,
			Metadata: map[string]string{
				"ingress_mode": string(pb.IngressModeTLSSNIShared),
				"sni":          sni,
			},
		}, nil
	case pb.IngressModeL4DedicatedPort:
		// l4_dedicated_port 导出 gateway_host:dedicated_port。
		gatewayHost := strings.TrimSpace(options.GatewayHost)
		if gatewayHost == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "gatewayHost is required for l4_dedicated_port")
		}
		if service.Exposure.ListenPort == 0 {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.listenPort is required for l4_dedicated_port")
		}
		address := fmt.Sprintf("%s:%d", gatewayHost, service.Exposure.ListenPort)
		if matchesUpstreamEndpoint(address, service.Endpoints) {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeExportNotEligible, "l4 export address must not equal upstream endpoint")
		}
		return EndpointBuildResult{
			Address: address,
			Metadata: map[string]string{
				"ingress_mode": string(pb.IngressModeL4DedicatedPort),
			},
		}, nil
	default:
		return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported ingress mode: %s", service.Exposure.IngressMode))
	}
}

// BuildDesiredProjections 基于 service 配置生成期望投影列表。
func BuildDesiredProjections(service pb.Service, endpoint EndpointBuildResult) []pb.DiscoveryProjection {
	providers := slices.Clone(service.DiscoveryPolicy.Providers)
	slices.Sort(providers)

	projections := make([]pb.DiscoveryProjection, 0, len(providers))
	for _, provider := range providers {
		normalizedProvider := strings.TrimSpace(provider)
		if normalizedProvider == "" {
			// provider 为空时直接忽略，避免生成脏 projection。
			continue
		}
		projections = append(projections, pb.DiscoveryProjection{
			ProjectionID: projectionID(service.ServiceID, normalizedProvider, service.Namespace, service.Environment),
			ServiceID:    service.ServiceID,
			Provider:     normalizedProvider,
			Namespace:    service.DiscoveryPolicy.Namespace,
			Environment:  service.Environment,
			ExportedAddr: endpoint.Address,
			Status:       "ACTIVE",
			Metadata: mergeMetadata(
				service.DiscoveryPolicy.Metadata,
				service.DiscoveryPolicy.Tags,
				endpoint.Metadata,
			),
		})
	}
	return projections
}

// BuildReconcilePlan 计算 projection 的 create/update/delete 集合。
func BuildReconcilePlan(current []pb.DiscoveryProjection, desired []pb.DiscoveryProjection) ReconcilePlan {
	currentMap := make(map[string]pb.DiscoveryProjection, len(current))
	for _, item := range current {
		currentMap[item.ProjectionID] = item
	}
	desiredMap := make(map[string]pb.DiscoveryProjection, len(desired))
	for _, item := range desired {
		desiredMap[item.ProjectionID] = item
	}

	plan := ReconcilePlan{
		Create: make([]pb.DiscoveryProjection, 0),
		Update: make([]pb.DiscoveryProjection, 0),
		Delete: make([]pb.DiscoveryProjection, 0),
	}
	for projectionID, desiredProjection := range desiredMap {
		currentProjection, exists := currentMap[projectionID]
		if !exists {
			// 当前不存在的 projection 需要创建。
			plan.Create = append(plan.Create, desiredProjection)
			continue
		}
		if !reflect.DeepEqual(currentProjection, desiredProjection) {
			// 字段有差异时放入更新列表。
			plan.Update = append(plan.Update, desiredProjection)
		}
	}
	for projectionID, currentProjection := range currentMap {
		if _, exists := desiredMap[projectionID]; !exists {
			// 期望中不存在的 projection 需要删除。
			plan.Delete = append(plan.Delete, currentProjection)
		}
	}
	return plan
}

// isOfflineStatus 判断连接器状态是否表示离线。
func isOfflineStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "offline", "stale", "closed":
		return true
	default:
		return false
	}
}

// resolvePort 解析导出端口，优先使用 service 端口，否则回退共享端口/443。
func resolvePort(servicePort uint32, sharedPort uint32) uint32 {
	if servicePort > 0 {
		return servicePort
	}
	if sharedPort > 0 {
		return sharedPort
	}
	return 443
}

// matchesUpstreamEndpoint 判断导出地址是否误指向了 agent upstream。
func matchesUpstreamEndpoint(address string, endpoints []pb.ServiceEndpoint) bool {
	normalizedAddress := strings.TrimSpace(strings.ToLower(address))
	for _, endpoint := range endpoints {
		endpointAddress := fmt.Sprintf("%s:%d", strings.ToLower(strings.TrimSpace(endpoint.Host)), endpoint.Port)
		if normalizedAddress == endpointAddress {
			return true
		}
	}
	return false
}

// projectionID 构造 projection 稳定主键。
func projectionID(serviceID string, provider string, namespace string, environment string) string {
	return strings.Join([]string{
		strings.TrimSpace(serviceID),
		strings.TrimSpace(provider),
		strings.TrimSpace(namespace),
		strings.TrimSpace(environment),
	}, "|")
}

// mergeMetadata 合并多个 metadata 映射，后写覆盖先写。
func mergeMetadata(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, source := range maps {
		for key, value := range source {
			// key 为空时跳过，避免污染导出结果。
			if strings.TrimSpace(key) == "" {
				continue
			}
			result[key] = value
		}
	}
	return result
}
