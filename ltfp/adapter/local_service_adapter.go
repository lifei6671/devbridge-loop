package adapter

import (
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/health"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// LocalRegistration 描述本地运行态服务注册模型。
type LocalRegistration struct {
	LogicalServiceID string
	InstanceID       string
	Scope            pb.Scope
	ServiceName      string
	ServiceType      string
	Endpoints        []pb.ServiceEndpoint
	Exposure         pb.ServiceExposure
	HealthCheck      pb.HealthCheckConfig
	DiscoveryPolicy  pb.DiscoveryPolicy
	RouteHint        pb.RouteHint
	Labels           map[string]string
	Metadata         map[string]string
}

// ResolveServiceProtocol 解析服务协议，优先 endpoint.protocol，其次 service_type。
func ResolveServiceProtocol(serviceType string, endpoints []pb.ServiceEndpoint) string {
	for _, endpoint := range endpoints {
		normalizedProtocol := strings.ToLower(strings.TrimSpace(endpoint.Protocol))
		if normalizedProtocol != "" {
			return normalizedProtocol
		}
	}
	return strings.ToLower(strings.TrimSpace(serviceType))
}

// ToPublishService 将本地注册对象转换为 PublishService 消息。
func ToPublishService(local LocalRegistration) pb.PublishService {
	return pb.PublishService{
		InstanceID:      strings.TrimSpace(local.InstanceID),
		ServiceName:     strings.TrimSpace(local.ServiceName),
		Scope:           local.Scope,
		ServiceType:     strings.TrimSpace(local.ServiceType),
		Endpoints:       cloneEndpoints(local.Endpoints),
		Exposure:        local.Exposure,
		HealthCheck:     local.HealthCheck,
		DiscoveryPolicy: local.DiscoveryPolicy,
		RouteHint:       cloneRouteHint(local.RouteHint),
		Labels:          cloneStringMap(local.Labels),
		Metadata:        cloneStringMap(local.Metadata),
	}
}

// ToUnpublishService 将本地下线事件转换为 UnpublishService 消息。
func ToUnpublishService(local LocalRegistration, reason string) pb.UnpublishService {
	return pb.UnpublishService{
		InstanceID:       strings.TrimSpace(local.InstanceID),
		LogicalServiceID: strings.TrimSpace(local.LogicalServiceID),
		ServiceName:      strings.TrimSpace(local.ServiceName),
		Scope:            local.Scope,
		Reason:           strings.TrimSpace(reason),
	}
}

// ToHealthReport 将本地 endpoint 健康结果转换为 ServiceHealthReport。
func ToHealthReport(
	instanceID string,
	logicalServiceID string,
	endpointStatuses []pb.EndpointHealthStatus,
	checkTime time.Time,
	reason string,
	metadata map[string]string,
) pb.ServiceHealthReport {
	aggregated := health.AggregateServiceHealth(endpointStatuses)
	timestamp := checkTime.Unix()
	if timestamp <= 0 {
		timestamp = time.Now().UTC().Unix()
	}
	return pb.ServiceHealthReport{
		InstanceID:          strings.TrimSpace(instanceID),
		LogicalServiceID:    strings.TrimSpace(logicalServiceID),
		ServiceHealthStatus: aggregated,
		EndpointStatuses:    cloneEndpointStatuses(endpointStatuses),
		CheckTimeUnix:       timestamp,
		Reason:              strings.TrimSpace(reason),
		Metadata:            cloneStringMap(metadata),
	}
}

// cloneEndpoints 深拷贝 endpoint 切片，避免调用方共享底层数组。
func cloneEndpoints(endpoints []pb.ServiceEndpoint) []pb.ServiceEndpoint {
	cloned := make([]pb.ServiceEndpoint, len(endpoints))
	copy(cloned, endpoints)
	return cloned
}

// cloneEndpointStatuses 深拷贝 endpoint 健康状态切片。
func cloneEndpointStatuses(statuses []pb.EndpointHealthStatus) []pb.EndpointHealthStatus {
	cloned := make([]pb.EndpointHealthStatus, len(statuses))
	copy(cloned, statuses)
	return cloned
}

// cloneStringMap 深拷贝字符串 map。
func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// cloneRouteHint 深拷贝 route hint，避免调用方共享 matcher 切片。
func cloneRouteHint(routeHint pb.RouteHint) pb.RouteHint {
	return pb.RouteHint{
		MatchHeaders: append([]pb.HeaderMatcher(nil), routeHint.MatchHeaders...),
		MatchQueries: append([]pb.QueryMatcher(nil), routeHint.MatchQueries...),
		Priority:     routeHint.Priority,
	}
}
