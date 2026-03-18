package adapter

import (
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/health"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// LocalRegistration 描述本地运行态服务注册模型。
type LocalRegistration struct {
	ServiceID       string
	ServiceKey      string
	Namespace       string
	Environment     string
	ServiceName     string
	ServiceType     string
	Endpoints       []pb.ServiceEndpoint
	Exposure        pb.ServiceExposure
	HealthCheck     pb.HealthCheckConfig
	DiscoveryPolicy pb.DiscoveryPolicy
	Labels          map[string]string
	Metadata        map[string]string
}

// BuildServiceKey 基于 service_name 与 protocol 构造 canonical service_key。
func BuildServiceKey(serviceName string, protocol string) string {
	normalizedServiceName := strings.TrimSpace(serviceName)
	normalizedProtocol := strings.ToLower(strings.TrimSpace(protocol))
	if normalizedServiceName == "" || normalizedProtocol == "" {
		// canonical key 任一关键段缺失时返回空值，交由上游校验失败。
		return ""
	}
	// service_key 固定格式为 <service_name>/<protocol>。
	return fmt.Sprintf("%s/%s", normalizedServiceName, normalizedProtocol)
}

// ResolveServiceProtocol 解析服务协议，优先 endpoint.protocol，其次 service_type。
func ResolveServiceProtocol(serviceType string, endpoints []pb.ServiceEndpoint) string {
	for _, endpoint := range endpoints {
		normalizedProtocol := strings.ToLower(strings.TrimSpace(endpoint.Protocol))
		if normalizedProtocol != "" {
			// 以 endpoint 为准，避免 service_type 与 endpoint 不一致。
			return normalizedProtocol
		}
	}
	// endpoint 未声明协议时回退 service_type，兼容历史调用路径。
	return strings.ToLower(strings.TrimSpace(serviceType))
}

// ToPublishService 将本地注册对象转换为 PublishService 消息。
func ToPublishService(local LocalRegistration) pb.PublishService {
	serviceKey := strings.TrimSpace(local.ServiceKey)
	if serviceKey == "" {
		// 未显式提供 serviceKey 时按 canonical 规则自动生成。
		serviceKey = BuildServiceKey(
			local.ServiceName,
			ResolveServiceProtocol(local.ServiceType, local.Endpoints),
		)
	}
	return pb.PublishService{
		ServiceID:       strings.TrimSpace(local.ServiceID),
		ServiceKey:      serviceKey,
		Namespace:       strings.TrimSpace(local.Namespace),
		Environment:     strings.TrimSpace(local.Environment),
		ServiceName:     strings.TrimSpace(local.ServiceName),
		ServiceType:     strings.TrimSpace(local.ServiceType),
		Endpoints:       cloneEndpoints(local.Endpoints),
		Exposure:        local.Exposure,
		HealthCheck:     local.HealthCheck,
		DiscoveryPolicy: local.DiscoveryPolicy,
		Labels:          cloneStringMap(local.Labels),
		Metadata:        cloneStringMap(local.Metadata),
	}
}

// ToUnpublishService 将本地下线事件转换为 UnpublishService 消息。
func ToUnpublishService(local LocalRegistration, reason string) pb.UnpublishService {
	serviceKey := strings.TrimSpace(local.ServiceKey)
	if serviceKey == "" {
		// 未显式提供 serviceKey 时按 canonical 规则自动生成。
		serviceKey = BuildServiceKey(
			local.ServiceName,
			ResolveServiceProtocol(local.ServiceType, local.Endpoints),
		)
	}
	return pb.UnpublishService{
		ServiceID:   strings.TrimSpace(local.ServiceID),
		ServiceKey:  serviceKey,
		Namespace:   strings.TrimSpace(local.Namespace),
		Environment: strings.TrimSpace(local.Environment),
		Reason:      strings.TrimSpace(reason),
	}
}

// ToHealthReport 将本地 endpoint 健康结果转换为 ServiceHealthReport。
func ToHealthReport(serviceID string, serviceKey string, endpointStatuses []pb.EndpointHealthStatus, checkTime time.Time, reason string, metadata map[string]string) pb.ServiceHealthReport {
	aggregated := health.AggregateServiceHealth(endpointStatuses)
	timestamp := checkTime.Unix()
	if timestamp <= 0 {
		// 未提供有效时间时使用当前 UTC 秒级时间戳。
		timestamp = time.Now().UTC().Unix()
	}
	return pb.ServiceHealthReport{
		ServiceID:           strings.TrimSpace(serviceID),
		ServiceKey:          strings.TrimSpace(serviceKey),
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
		// 原样复制 metadata，保持与调用方语义一致。
		cloned[key] = value
	}
	return cloned
}
