package directproxy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type directProxyTestDiscoveryProvider struct {
	mutex     sync.Mutex
	endpoints []ExternalEndpoint
	err       error
	calls     int
}

func (provider *directProxyTestDiscoveryProvider) Discover(ctx context.Context, request DiscoveryRequest) ([]ExternalEndpoint, error) {
	_ = ctx
	_ = request
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.calls++
	return cloneEndpoints(provider.endpoints), provider.err
}

func (provider *directProxyTestDiscoveryProvider) SetError(err error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.err = err
}

func (provider *directProxyTestDiscoveryProvider) Calls() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.calls
}

type directProxyTestConnection struct {
	closeCalls int
}

func (connection *directProxyTestConnection) Close() error {
	connection.closeCalls++
	return nil
}

type directProxyTestDialer struct {
	mutex sync.Mutex
	calls int
}

func (dialer *directProxyTestDialer) Dial(ctx context.Context, endpoint ExternalEndpoint) (UpstreamConn, error) {
	_ = ctx
	if endpoint.Address == "" {
		return nil, errors.New("empty endpoint")
	}
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	dialer.calls++
	return &directProxyTestConnection{}, nil
}

func (dialer *directProxyTestDialer) Calls() int {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	return dialer.calls
}

type directProxyFailDialer struct {
	err error
}

func (dialer *directProxyFailDialer) Dial(ctx context.Context, endpoint ExternalEndpoint) (UpstreamConn, error) {
	_ = ctx
	_ = endpoint
	return nil, dialer.err
}

type directProxyTestRelay struct {
	mutex     sync.Mutex
	calls     int
	trafficID string
}

func (relay *directProxyTestRelay) Relay(ctx context.Context, connection UpstreamConn, trafficID string) error {
	_ = ctx
	if connection == nil {
		return errors.New("nil connection")
	}
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.calls++
	relay.trafficID = trafficID
	return nil
}

func (relay *directProxyTestRelay) Calls() int {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	return relay.calls
}

func (relay *directProxyTestRelay) TrafficID() string {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	return relay.trafficID
}

// TestNewExecutorRequiresRelay 验证未注入 relay 时构造器应失败，避免默认空实现吞流量。
func TestNewExecutorRequiresRelay(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: &directProxyTestDiscoveryProvider{},
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	_, err = NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Dialer:    dialer,
	})
	if !errors.Is(err, ErrExecutorDependencyMissing) {
		testingObject.Fatalf("expected dependency missing error, got: %v", err)
	}
}

// TestExecutorExecuteExternalPath 验证 external_service 走 discovery->dial->relay 闭环。
func TestExecutorExecuteExternalPath(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryProvider := &directProxyTestDiscoveryProvider{
		endpoints: []ExternalEndpoint{
			{EndpointID: "ep-1", Address: "10.0.0.10:443"},
		},
	}
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: discoveryProvider,
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialerDependency := &directProxyTestDialer{}
	dialer, err := NewDialer(DialerOptions{
		Upstream: dialerDependency,
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	relay := &directProxyTestRelay{}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay:     relay,
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}

	result, err := executor.Execute(context.Background(), ExecuteRequest{
		TrafficID: "traffic-1",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	})
	if err != nil {
		testingObject.Fatalf("execute external path failed: %v", err)
	}
	if result.Endpoint.EndpointID != "ep-1" {
		testingObject.Fatalf("unexpected endpoint id: %s", result.Endpoint.EndpointID)
	}
	if result.Endpoint.Address != "10.0.0.10:443" {
		testingObject.Fatalf("unexpected endpoint address: %s", result.Endpoint.Address)
	}
	if result.FromCache {
		testingObject.Fatalf("first request should not be cache hit")
	}
	if result.UsedStale {
		testingObject.Fatalf("first request should not use stale endpoint")
	}
	if discoveryProvider.Calls() != 1 {
		testingObject.Fatalf("expected discovery called once, got=%d", discoveryProvider.Calls())
	}
	if dialerDependency.Calls() != 1 {
		testingObject.Fatalf("expected dial called once, got=%d", dialerDependency.Calls())
	}
	if relay.Calls() != 1 {
		testingObject.Fatalf("expected relay called once, got=%d", relay.Calls())
	}
	if relay.TrafficID() != "traffic-1" {
		testingObject.Fatalf("unexpected relay traffic_id: %s", relay.TrafficID())
	}
	if result.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected http status: %d", result.HTTPStatus)
	}
	if result.ErrorCode != "" {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
}

// TestExecutorExecuteCacheHit 验证 endpoint cache 命中可绕过 discovery。
func TestExecutorExecuteCacheHit(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryProvider := &directProxyTestDiscoveryProvider{
		endpoints: []ExternalEndpoint{
			{EndpointID: "ep-1", Address: "10.0.0.10:443"},
		},
	}
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: discoveryProvider,
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay:     &directProxyTestRelay{},
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	request := ExecuteRequest{
		TrafficID: "traffic-cache",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		testingObject.Fatalf("first execute failed: %v", err)
	}
	discoveryProvider.SetError(errors.New("provider unavailable"))
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		testingObject.Fatalf("second execute should use cache hit: %v", err)
	}
	if !result.FromCache {
		testingObject.Fatalf("expected second execute from cache")
	}
	if discoveryProvider.Calls() != 1 {
		testingObject.Fatalf("expected discovery still called once, got=%d", discoveryProvider.Calls())
	}
}

// TestExecutorExecuteStaleIfError 验证 stale_if_error 窗口允许在刷新失败时继续使用旧 endpoint。
func TestExecutorExecuteStaleIfError(testingObject *testing.T) {
	testingObject.Parallel()
	nowTime := time.Date(2026, time.March, 13, 0, 0, 0, 0, time.UTC)
	discoveryProvider := &directProxyTestDiscoveryProvider{
		endpoints: []ExternalEndpoint{
			{EndpointID: "ep-1", Address: "10.0.0.10:443"},
		},
	}
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: discoveryProvider,
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	cache := NewEndpointCache(EndpointCacheOptions{
		Now: func() time.Time { return nowTime },
	})
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     cache,
		Dialer:    dialer,
		Relay:     &directProxyTestRelay{},
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	request := ExecuteRequest{
		TrafficID: "traffic-stale",
		Target: pb.ExternalServiceTarget{
			Provider:        "k8s",
			Namespace:       "dev",
			Environment:     "alice",
			ServiceName:     "pay",
			CacheTTLSeconds: 1,
			StaleIfErrorSec: 10,
		},
	}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		testingObject.Fatalf("first execute failed: %v", err)
	}
	nowTime = nowTime.Add(3 * time.Second)
	discoveryProvider.SetError(errors.New("temporary discovery failure"))
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		testingObject.Fatalf("stale fallback execute failed: %v", err)
	}
	if !result.FromCache {
		testingObject.Fatalf("expected stale response from cache")
	}
	if !result.UsedStale {
		testingObject.Fatalf("expected stale flag true")
	}
	if discoveryProvider.Calls() != 2 {
		testingObject.Fatalf("expected discovery retried once on stale cache, got=%d", discoveryProvider.Calls())
	}
	if result.HTTPStatus != 200 {
		testingObject.Fatalf("unexpected http status for stale fallback: %d", result.HTTPStatus)
	}
}

// TestExecutorExecuteDiscoveryMissClassification 验证 discovery miss 会映射为 503 + DISCOVERY_NO_ENDPOINT。
func TestExecutorExecuteDiscoveryMissClassification(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: &directProxyTestDiscoveryProvider{},
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay:     &directProxyTestRelay{},
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		TrafficID: "traffic-discovery-miss",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	})
	if err == nil {
		testingObject.Fatalf("expected discovery miss error")
	}
	if result.HTTPStatus != 503 {
		testingObject.Fatalf("unexpected http status: %d", result.HTTPStatus)
	}
	if result.ErrorCode != ltfperrors.CodeDiscoveryNoEndpoint {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
}

// TestExecutorExecuteDiscoveryPolicyFailureClassification 验证 discovery 安全策略错误会保留原始错误码。
func TestExecutorExecuteDiscoveryPolicyFailureClassification(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: &directProxyTestDiscoveryProvider{
			err: ltfperrors.New(ltfperrors.CodeDiscoveryEndpointDenied, "endpoint denied"),
		},
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay:     &directProxyTestRelay{},
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		TrafficID: "traffic-discovery-policy-denied",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	})
	if err == nil {
		testingObject.Fatalf("expected discovery policy failure")
	}
	if result.HTTPStatus != 403 {
		testingObject.Fatalf("unexpected http status: %d", result.HTTPStatus)
	}
	if result.ErrorCode != ltfperrors.CodeDiscoveryEndpointDenied {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
}

// TestExecutorExecuteDialFailureClassification 验证 direct dial 失败会映射为 502 + DIRECT_PROXY_DIAL_FAILED。
func TestExecutorExecuteDialFailureClassification(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: &directProxyTestDiscoveryProvider{
			endpoints: []ExternalEndpoint{{EndpointID: "ep-1", Address: "10.0.0.1:443"}},
		},
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyFailDialer{err: errors.New("dial timeout")},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay:     &directProxyTestRelay{},
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		TrafficID: "traffic-dial-failed",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	})
	if err == nil {
		testingObject.Fatalf("expected dial failure")
	}
	if result.HTTPStatus != 502 {
		testingObject.Fatalf("unexpected http status: %d", result.HTTPStatus)
	}
	if result.ErrorCode != ltfperrors.CodeDirectProxyDialFailed {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
}

// TestExecutorExecuteRelayFailureClassification 验证 relay 失败会映射为 502 + DIRECT_PROXY_RELAY_FAILED。
func TestExecutorExecuteRelayFailureClassification(testingObject *testing.T) {
	testingObject.Parallel()
	discoveryAdapter, err := NewDiscoveryAdapter(DiscoveryAdapterOptions{
		Provider: &directProxyTestDiscoveryProvider{
			endpoints: []ExternalEndpoint{{EndpointID: "ep-1", Address: "10.0.0.1:443"}},
		},
	})
	if err != nil {
		testingObject.Fatalf("new discovery adapter failed: %v", err)
	}
	dialer, err := NewDialer(DialerOptions{
		Upstream: &directProxyTestDialer{},
	})
	if err != nil {
		testingObject.Fatalf("new dialer failed: %v", err)
	}
	executor, err := NewExecutor(ExecutorOptions{
		Discovery: discoveryAdapter,
		Cache:     NewEndpointCache(EndpointCacheOptions{}),
		Dialer:    dialer,
		Relay: RelayFunc(func(ctx context.Context, connection UpstreamConn, trafficID string) error {
			_ = ctx
			_ = connection
			_ = trafficID
			return errors.New("relay broken")
		}),
	})
	if err != nil {
		testingObject.Fatalf("new direct executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		TrafficID: "traffic-relay-failed",
		Target: pb.ExternalServiceTarget{
			Provider:    "k8s",
			Namespace:   "dev",
			Environment: "alice",
			ServiceName: "pay",
		},
	})
	if err == nil {
		testingObject.Fatalf("expected relay failure")
	}
	if result.HTTPStatus != 502 {
		testingObject.Fatalf("unexpected http status: %d", result.HTTPStatus)
	}
	if result.ErrorCode != ltfperrors.CodeDirectProxyRelayFailed {
		testingObject.Fatalf("unexpected error code: %s", result.ErrorCode)
	}
}
