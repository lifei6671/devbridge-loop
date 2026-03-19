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
	ServiceInstance     pb.ServiceInstance
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

// CheckEligibility 校验 service instance 是否满足 export 准入条件。
func CheckEligibility(input EligibilityInput) error {
	instance := input.ServiceInstance
	connector := input.Connector
	session := input.Session
	if isOfflineStatus(connector.Status) {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "connector is offline")
	}
	if strings.TrimSpace(session.SessionID) == "" || session.State != pb.SessionStateActive {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "active session is required")
	}
	if strings.TrimSpace(session.ConnectorID) != strings.TrimSpace(instance.ConnectorID) {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "session connector does not match service instance connector")
	}
	if instance.InstanceStatus != pb.ServiceStatusActive {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "service instance is not active")
	}
	if instance.HealthStatus != pb.HealthStatusHealthy {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "service instance is not healthy")
	}
	if !instance.DiscoveryPolicy.Enabled {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "discovery_policy.enabled must be true")
	}
	if !instance.Exposure.AllowExport {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "exposure.allow_export must be true")
	}
	if !input.HasReachableIngress {
		return ltfperrors.New(ltfperrors.CodeExportNotEligible, "server reachable ingress is not ready")
	}
	return nil
}

// BuildEndpoint 基于 ingress mode 生成 export endpoint。
func BuildEndpoint(instance pb.ServiceInstance, options EndpointBuildOptions) (EndpointBuildResult, error) {
	switch instance.Exposure.IngressMode {
	case pb.IngressModeL7Shared:
		host := strings.TrimSpace(instance.Exposure.Host)
		if host == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.host is required for l7_shared")
		}
		port := resolvePort(instance.Exposure.ListenPort, options.SharedPort)
		address := fmt.Sprintf("%s:%d", host, port)
		if matchesUpstreamEndpoint(address, instance.Endpoints) {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeExportNotEligible, "l7 export address must not equal upstream endpoint")
		}
		return EndpointBuildResult{
			Address: address,
			Metadata: map[string]string{
				"ingress_mode": string(pb.IngressModeL7Shared),
			},
		}, nil
	case pb.IngressModeTLSSNIShared:
		gatewayHost := strings.TrimSpace(options.GatewayHost)
		if gatewayHost == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "gatewayHost is required for tls_sni_shared")
		}
		port := resolvePort(instance.Exposure.ListenPort, options.SharedPort)
		sni := strings.TrimSpace(instance.Exposure.SNIName)
		if sni == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.sniName is required for tls_sni_shared")
		}
		address := fmt.Sprintf("%s:%d", gatewayHost, port)
		if matchesUpstreamEndpoint(address, instance.Endpoints) {
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
		gatewayHost := strings.TrimSpace(options.GatewayHost)
		if gatewayHost == "" {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "gatewayHost is required for l4_dedicated_port")
		}
		if instance.Exposure.ListenPort == 0 {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "exposure.listenPort is required for l4_dedicated_port")
		}
		address := fmt.Sprintf("%s:%d", gatewayHost, instance.Exposure.ListenPort)
		if matchesUpstreamEndpoint(address, instance.Endpoints) {
			return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeExportNotEligible, "l4 export address must not equal upstream endpoint")
		}
		return EndpointBuildResult{
			Address: address,
			Metadata: map[string]string{
				"ingress_mode": string(pb.IngressModeL4DedicatedPort),
			},
		}, nil
	default:
		return EndpointBuildResult{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported ingress mode: %s", instance.Exposure.IngressMode))
	}
}

// BuildDesiredProjections 基于 service instance 配置生成期望投影列表。
func BuildDesiredProjections(instance pb.ServiceInstance, endpoint EndpointBuildResult) []pb.DiscoveryProjection {
	providers := slices.Clone(instance.DiscoveryPolicy.Providers)
	slices.Sort(providers)

	projections := make([]pb.DiscoveryProjection, 0, len(providers))
	for _, provider := range providers {
		normalizedProvider := strings.TrimSpace(provider)
		if normalizedProvider == "" {
			continue
		}
		projections = append(projections, pb.DiscoveryProjection{
			ProjectionID:     projectionID(instance.LogicalServiceID, instance.InstanceID, normalizedProvider, instance.DiscoveryPolicy.Namespace),
			LogicalServiceID: instance.LogicalServiceID,
			InstanceID:       instance.InstanceID,
			Provider:         normalizedProvider,
			Namespace:        instance.DiscoveryPolicy.Namespace,
			Environment:      instance.Metadata["scope.environment"],
			ExportedAddr:     endpoint.Address,
			Status:           "ACTIVE",
			Metadata: mergeMetadata(
				instance.DiscoveryPolicy.Metadata,
				instance.DiscoveryPolicy.Tags,
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
			plan.Create = append(plan.Create, desiredProjection)
			continue
		}
		if !reflect.DeepEqual(currentProjection, desiredProjection) {
			plan.Update = append(plan.Update, desiredProjection)
		}
	}
	for projectionID, currentProjection := range currentMap {
		if _, exists := desiredMap[projectionID]; !exists {
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
func projectionID(logicalServiceID string, instanceID string, provider string, namespace string) string {
	return strings.Join([]string{
		strings.TrimSpace(logicalServiceID),
		strings.TrimSpace(instanceID),
		strings.TrimSpace(provider),
		strings.TrimSpace(namespace),
	}, "|")
}

// mergeMetadata 合并多个 metadata 映射，后写覆盖先写。
func mergeMetadata(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, source := range maps {
		for key, value := range source {
			if strings.TrimSpace(key) == "" {
				continue
			}
			result[key] = value
		}
	}
	return result
}
