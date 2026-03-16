package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type testEndpointHealthProbe struct {
	resultByEndpointID map[string]pb.HealthStatus
}

func (probe *testEndpointHealthProbe) Probe(
	_ context.Context,
	_ adapter.LocalRegistration,
	endpoint pb.ServiceEndpoint,
) (pb.HealthStatus, string) {
	if probe == nil {
		return pb.HealthStatusUnknown, "probe is nil"
	}
	if status, exists := probe.resultByEndpointID[endpoint.EndpointID]; exists {
		return status, "stubbed"
	}
	return pb.HealthStatusUnknown, "not_found"
}

// TestHealthReporterBuildServiceReport 验证 endpoint 状态会聚合为 service 健康状态。
func TestHealthReporterBuildServiceReport(t *testing.T) {
	t.Parallel()

	reporter := NewHealthReporter(HealthReporterOptions{
		Probe: &testEndpointHealthProbe{
			resultByEndpointID: map[string]pb.HealthStatus{
				"ep-1": pb.HealthStatusHealthy,
				"ep-2": pb.HealthStatusUnhealthy,
			},
		},
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	report := reporter.BuildServiceReport(context.Background(), adapter.LocalRegistration{
		ServiceID:   "svc-3001",
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18080},
			{EndpointID: "ep-2", Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	if report.ServiceID != "svc-3001" {
		t.Fatalf("unexpected service_id: %s", report.ServiceID)
	}
	if report.ServiceHealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected aggregated health: got=%s want=%s", report.ServiceHealthStatus, pb.HealthStatusHealthy)
	}
	if len(report.EndpointStatuses) != 2 {
		t.Fatalf("unexpected endpoint status count: got=%d want=2", len(report.EndpointStatuses))
	}
}

// TestHealthReporterBuildReports 验证批量构建会输出等量报告。
func TestHealthReporterBuildReports(t *testing.T) {
	t.Parallel()

	reporter := NewHealthReporter(HealthReporterOptions{
		Probe: &testEndpointHealthProbe{
			resultByEndpointID: map[string]pb.HealthStatus{
				"ep-1": pb.HealthStatusHealthy,
				"ep-2": pb.HealthStatusUnhealthy,
			},
		},
		Now: func() time.Time { return time.Unix(1700000010, 0).UTC() },
	})
	reports := reporter.BuildReports(context.Background(), []adapter.LocalRegistration{
		{
			ServiceID:   "svc-1",
			ServiceKey:  "dev/demo/s1",
			Namespace:   "dev",
			Environment: "demo",
			ServiceName: "s1",
			Endpoints: []pb.ServiceEndpoint{
				{EndpointID: "ep-1", Protocol: "tcp", Host: "127.0.0.1", Port: 18080},
			},
		},
		{
			ServiceID:   "svc-2",
			ServiceKey:  "dev/demo/s2",
			Namespace:   "dev",
			Environment: "demo",
			ServiceName: "s2",
			Endpoints: []pb.ServiceEndpoint{
				{EndpointID: "ep-2", Protocol: "tcp", Host: "127.0.0.1", Port: 18081},
			},
		},
	})
	if len(reports) != 2 {
		t.Fatalf("unexpected report count: got=%d want=2", len(reports))
	}
	if reports[0].ServiceID != "svc-1" {
		t.Fatalf("unexpected first report service_id: %s", reports[0].ServiceID)
	}
	if reports[1].ServiceID != "svc-2" {
		t.Fatalf("unexpected second report service_id: %s", reports[1].ServiceID)
	}
}

// TestHealthReporterHTTPProbeAccepts4xx 验证 HTTP/HTTPS HEAD 探测接受 2xx/3xx/4xx 状态码。
func TestHealthReporterHTTPProbeAccepts4xx(t *testing.T) {
	t.Parallel()

	requestPath := ""
	requestMethod := ""
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestPath = request.URL.Path
		requestMethod = request.Method
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer testServer.Close()

	serverURL, err := url.Parse(testServer.URL)
	if err != nil {
		t.Fatalf("parse test server url failed: %v", err)
	}
	host := serverURL.Hostname()
	portText := serverURL.Port()
	if host == "" || portText == "" {
		t.Fatalf("unexpected server host/port: %s", serverURL.Host)
	}
	portValue, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse server port failed: %v", err)
	}

	reporter := NewHealthReporter(HealthReporterOptions{})
	report := reporter.BuildServiceReport(context.Background(), adapter.LocalRegistration{
		ServiceID:   "svc-http",
		ServiceKey:  "dev/demo/http-service",
		ServiceName: "http-service",
		ServiceType: "http",
		HealthCheck: pb.HealthCheckConfig{
			Type:     "http",
			Endpoint: "healthz",
		},
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-http",
				Protocol:   "http",
				Host:       host,
				Port:       uint32(portValue),
			},
		},
	})
	if report.ServiceHealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected service health status: got=%s want=%s", report.ServiceHealthStatus, pb.HealthStatusHealthy)
	}
	if requestMethod != http.MethodHead {
		t.Fatalf("expected HEAD probe method, got=%s", requestMethod)
	}
	if requestPath != "/healthz" {
		t.Fatalf("unexpected probe path: got=%s want=%s", requestPath, "/healthz")
	}
	if report.Metadata["probe_mode"] != "http" {
		t.Fatalf("unexpected probe_mode metadata: %+v", report.Metadata["probe_mode"])
	}
}

// TestHealthReporterHTTPProbeUsesDefaultPath 验证 HTTP/HTTPS 探测默认路径为根目录。
func TestHealthReporterHTTPProbeUsesDefaultPath(t *testing.T) {
	t.Parallel()

	requestPath := ""
	testServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestPath = request.URL.Path
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer testServer.Close()

	serverURL, err := url.Parse(testServer.URL)
	if err != nil {
		t.Fatalf("parse test server url failed: %v", err)
	}
	portValue, err := strconv.Atoi(serverURL.Port())
	if err != nil {
		t.Fatalf("parse server port failed: %v", err)
	}

	reporter := NewHealthReporter(HealthReporterOptions{})
	report := reporter.BuildServiceReport(context.Background(), adapter.LocalRegistration{
		ServiceID:   "svc-http-default-path",
		ServiceKey:  "dev/demo/http-default-path",
		ServiceName: "http-default-path",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: "ep-http-default-path",
				Protocol:   "http",
				Host:       serverURL.Hostname(),
				Port:       uint32(portValue),
			},
		},
	})
	if report.ServiceHealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected service health status: got=%s want=%s", report.ServiceHealthStatus, pb.HealthStatusHealthy)
	}
	if requestPath != "/" {
		t.Fatalf("unexpected default probe path: got=%s want=%s", requestPath, "/")
	}
}

// TestNewHTTPProbeClientSetsTLSServerName 验证 HTTPS 探测会透传 endpoint.sni_name。
func TestNewHTTPProbeClientSetsTLSServerName(t *testing.T) {
	t.Parallel()

	client := newHTTPProbeClient("https", pb.ServiceEndpoint{
		ServerName: "order.dev.example.com",
	})
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected *http.Transport, got=%T", client.Transport)
	}
	if transport.TLSClientConfig == nil {
		t.Fatalf("expected tls config for https probe client")
	}
	if transport.TLSClientConfig.ServerName != "order.dev.example.com" {
		t.Fatalf(
			"unexpected tls server name: got=%s want=%s",
			transport.TLSClientConfig.ServerName,
			"order.dev.example.com",
		)
	}
}
