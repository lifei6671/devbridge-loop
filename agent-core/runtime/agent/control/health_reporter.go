package control

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// defaultEndpointProbeTimeout 定义 endpoint 探测默认超时。
	defaultEndpointProbeTimeout = 800 * time.Millisecond
)

// EndpointHealthProbe 定义 endpoint 健康探测接口。
type EndpointHealthProbe interface {
	// Probe 返回单个 endpoint 的健康状态与探测原因。
	Probe(ctx context.Context, service adapter.LocalRegistration, endpoint pb.ServiceEndpoint) (pb.HealthStatus, string)
}

// HealthReporterOptions 定义健康上报器构造参数。
type HealthReporterOptions struct {
	Probe EndpointHealthProbe
	Now   func() time.Time
}

// HealthReporter 负责将本地 endpoint 健康聚合为 service 健康报告。
type HealthReporter struct {
	probe EndpointHealthProbe
	now   func() time.Time
}

// NewHealthReporter 创建健康上报器。
func NewHealthReporter(options HealthReporterOptions) *HealthReporter {
	probe := options.Probe
	if probe == nil {
		// 未注入探测器时回落到默认 TCP 拨号探测。
		probe = &tcpEndpointHealthProbe{}
	}
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	return &HealthReporter{
		probe: probe,
		now:   nowFunc,
	}
}

// BuildServiceReport 基于本地 service 配置生成一次健康上报。
func (reporter *HealthReporter) BuildServiceReport(
	ctx context.Context,
	service adapter.LocalRegistration,
) pb.ServiceHealthReport {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	normalizedService := normalizeLocalRegistration(service)
	endpointStatuses := make([]pb.EndpointHealthStatus, 0, len(normalizedService.Endpoints))
	for _, endpoint := range normalizedService.Endpoints {
		healthStatus, reason := reporter.probe.Probe(normalizedContext, normalizedService, endpoint)
		endpointStatuses = append(endpointStatuses, pb.EndpointHealthStatus{
			EndpointID:   resolveEndpointID(endpoint),
			HealthStatus: normalizeHealthStatus(healthStatus),
			Reason:       strings.TrimSpace(reason),
		})
	}

	checkTime := reporter.now().UTC()
	return adapter.ToHealthReport(
		normalizedService.ServiceID,
		normalizedService.ServiceKey,
		endpointStatuses,
		checkTime,
		"agent.endpoint_probe",
		map[string]string{
			"probe_mode": "tcp_dial",
		},
	)
}

// BuildReports 基于本地服务列表批量生成健康上报。
func (reporter *HealthReporter) BuildReports(
	ctx context.Context,
	services []adapter.LocalRegistration,
) []pb.ServiceHealthReport {
	if len(services) == 0 {
		return nil
	}
	reports := make([]pb.ServiceHealthReport, 0, len(services))
	for _, service := range services {
		reports = append(reports, reporter.BuildServiceReport(ctx, service))
	}
	return reports
}

// tcpEndpointHealthProbe 使用 TCP 拨号判断 endpoint 可达性。
type tcpEndpointHealthProbe struct{}

// Probe 执行单 endpoint 可达性探测。
func (probe *tcpEndpointHealthProbe) Probe(
	ctx context.Context,
	service adapter.LocalRegistration,
	endpoint pb.ServiceEndpoint,
) (pb.HealthStatus, string) {
	_ = probe
	_ = service
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	dialTimeout := defaultEndpointProbeTimeout
	if endpoint.DialTimeoutMS > 0 {
		dialTimeout = time.Duration(endpoint.DialTimeoutMS) * time.Millisecond
	}
	if _, hasDeadline := normalizedContext.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		normalizedContext, cancel = context.WithTimeout(normalizedContext, dialTimeout)
		defer cancel()
	}
	endpointAddress, addressErr := endpointAddress(endpoint)
	if addressErr != nil {
		return pb.HealthStatusUnhealthy, addressErr.Error()
	}
	connection, dialErr := (&net.Dialer{}).DialContext(normalizedContext, "tcp", endpointAddress)
	if dialErr != nil {
		return pb.HealthStatusUnhealthy, fmt.Sprintf("dial failed: %v", dialErr)
	}
	_ = connection.Close()
	return pb.HealthStatusHealthy, "dial ok"
}

// endpointAddress 组装 endpoint 探测地址。
func endpointAddress(endpoint pb.ServiceEndpoint) (string, error) {
	normalizedHost := strings.TrimSpace(endpoint.Host)
	if normalizedHost == "" {
		return "", fmt.Errorf("empty endpoint host")
	}
	if endpoint.Port == 0 {
		return "", fmt.Errorf("invalid endpoint port=0")
	}
	return net.JoinHostPort(normalizedHost, strconv.Itoa(int(endpoint.Port))), nil
}

// resolveEndpointID 解析 endpoint 标识，缺失时回退 host:port。
func resolveEndpointID(endpoint pb.ServiceEndpoint) string {
	if endpointID := strings.TrimSpace(endpoint.EndpointID); endpointID != "" {
		return endpointID
	}
	address, err := endpointAddress(endpoint)
	if err != nil {
		// 地址不可用时回落到 protocol，确保字段不为空。
		return strings.TrimSpace(endpoint.Protocol)
	}
	return address
}

// normalizeLocalRegistration 归一化并深拷贝本地 service 对象。
func normalizeLocalRegistration(service adapter.LocalRegistration) adapter.LocalRegistration {
	normalized := adapter.LocalRegistration{
		ServiceID:       strings.TrimSpace(service.ServiceID),
		ServiceKey:      strings.TrimSpace(service.ServiceKey),
		Namespace:       strings.TrimSpace(service.Namespace),
		Environment:     strings.TrimSpace(service.Environment),
		ServiceName:     strings.TrimSpace(service.ServiceName),
		ServiceType:     strings.TrimSpace(service.ServiceType),
		Endpoints:       cloneEndpoints(service.Endpoints),
		Exposure:        service.Exposure,
		HealthCheck:     service.HealthCheck,
		DiscoveryPolicy: service.DiscoveryPolicy,
		Labels:          cloneStringMap(service.Labels),
		Metadata:        cloneStringMap(service.Metadata),
	}
	if normalized.ServiceKey == "" {
		// service_key 缺失时按协议推荐格式自动补齐。
		normalized.ServiceKey = adapter.BuildServiceKey(
			normalized.Namespace,
			normalized.Environment,
			normalized.ServiceName,
		)
	}
	if normalized.ServiceID == "" {
		// service_id 缺失时回退 service_key，便于资源定位。
		normalized.ServiceID = normalized.ServiceKey
	}
	return normalized
}

// normalizeHealthStatus 将非法健康状态回退为 UNKNOWN。
func normalizeHealthStatus(status pb.HealthStatus) pb.HealthStatus {
	switch status {
	case pb.HealthStatusHealthy, pb.HealthStatusUnhealthy, pb.HealthStatusUnknown:
		return status
	default:
		return pb.HealthStatusUnknown
	}
}

// cloneEndpoints 深拷贝 endpoint 列表。
func cloneEndpoints(endpoints []pb.ServiceEndpoint) []pb.ServiceEndpoint {
	cloned := make([]pb.ServiceEndpoint, len(endpoints))
	copy(cloned, endpoints)
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
