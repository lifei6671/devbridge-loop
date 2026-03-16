package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestBootstrapInitializesIngressHTTPServerWhenHTTPAddrSet 验证配置了 ingress.http_addr 时会初始化监听器。
func TestBootstrapInitializesIngressHTTPServerWhenHTTPAddrSet(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Ingress.HTTPAddr = ":18080"

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.ingressHTTPServer == nil {
		testingObject.Fatalf("expected ingress http server initialized")
	}
	if runtime.ingressHTTPServer.Addr != config.Ingress.HTTPAddr {
		testingObject.Fatalf("unexpected ingress http addr: got=%s want=%s", runtime.ingressHTTPServer.Addr, config.Ingress.HTTPAddr)
	}
}

// TestBootstrapSkipsIngressHTTPServerWhenHTTPAddrEmpty 验证未配置 http_addr 时不会启动 HTTP ingress 监听。
func TestBootstrapSkipsIngressHTTPServerWhenHTTPAddrEmpty(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Ingress.HTTPAddr = ""

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.ingressHTTPServer != nil {
		testingObject.Fatalf("expected ingress http server nil when ingress.http_addr empty")
	}
}

// TestIngressHTTPHandlerReturnsRouteMismatch 验证无匹配路由时返回结构化 404 错误。
func TestIngressHTTPHandlerReturnsRouteMismatch(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	if runtime.ingressHTTPServer == nil {
		testingObject.Fatalf("expected ingress http server initialized")
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
	request.Host = "api.missing.local"
	request.Header.Set("X-Request-Id", "trace-missing-1")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	var response struct {
		TrafficID string `json:"traffic_id"`
		TraceID   string `json:"trace_id"`
		Error     struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode response failed: %v", err)
	}
	if response.Error.Code != ltfperrors.CodeIngressRouteMismatch {
		testingObject.Fatalf("unexpected error code: got=%s want=%s", response.Error.Code, ltfperrors.CodeIngressRouteMismatch)
	}
	if response.TrafficID == "" {
		testingObject.Fatalf("expected non-empty traffic id")
	}
	if response.TraceID != "trace-missing-1" {
		testingObject.Fatalf("unexpected trace id: got=%s want=%s", response.TraceID, "trace-missing-1")
	}
}

// TestIngressHTTPHandlerRetriesRouteResolveOnTransientMismatch 验证路由短暂未就绪时会自动重试解析。
func TestIngressHTTPHandlerRetriesRouteResolveOnTransientMismatch(testingObject *testing.T) {
	testingObject.Parallel()

	externalServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("route-ready"))
	}))
	defer externalServer.Close()
	externalEndpoint := strings.TrimPrefix(externalServer.URL, "http://")

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()

	go func() {
		time.Sleep(50 * time.Millisecond)
		runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
			RouteID:     "route-retry-ready",
			Namespace:   "dev",
			Environment: "demo",
			Match: pb.RouteMatch{
				Protocol:   "http",
				Host:       "api.retry.local",
				PathPrefix: "/",
			},
			Target: pb.RouteTarget{
				Type: pb.RouteTargetTypeExternalService,
				ExternalService: &pb.ExternalServiceTarget{
					Namespace:   "dev",
					Environment: "demo",
					ServiceName: "retry-upstream",
					Selector: map[string]string{
						"endpoint": externalEndpoint,
					},
				},
			},
		})
	}()

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/ready", nil)
	request.Host = "api.retry.local:8080"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code after retry: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "route-ready" {
		testingObject.Fatalf("unexpected body after retry: %s", recorder.Body.String())
	}
}

// TestIngressHTTPHandlerUsesScopeHeaders 验证请求头 namespace/environment 会参与路由解析。
func TestIngressHTTPHandlerUsesScopeHeaders(testingObject *testing.T) {
	testingObject.Parallel()

	externalServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("external-ok"))
	}))
	defer externalServer.Close()
	externalEndpoint := strings.TrimPrefix(externalServer.URL, "http://")

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-ingress-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "order-service",
				Selector: map[string]string{
					"endpoint": externalEndpoint,
				},
			},
		},
	})

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.dev.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Header().Get("X-DevBridge-Route-Id") != "route-ingress-1" {
		testingObject.Fatalf("unexpected route header: %s", recorder.Header().Get("X-DevBridge-Route-Id"))
	}
	if recorder.Header().Get("X-DevBridge-Target-Kind") != string(pb.RouteTargetTypeExternalService) {
		testingObject.Fatalf("unexpected target kind header: %s", recorder.Header().Get("X-DevBridge-Target-Kind"))
	}
	if recorder.Header().Get("X-DevBridge-Traffic-Id") == "" {
		testingObject.Fatalf("expected non-empty traffic id header")
	}
	if recorder.Body.String() != "external-ok" {
		testingObject.Fatalf("unexpected external response body: %s", recorder.Body.String())
	}
}

// TestIngressHTTPHandlerConnectorProxyRelaysHTTPResponse 验证 connector 路径可透传 HTTP 响应内容。
func TestIngressHTTPHandlerConnectorProxyRelaysHTTPResponse(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-http-connector-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.dev.local",
			PathPrefix: "/v1",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
	})

	testTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-connector-1")
	go func() {
		for {
			writes := testTunnel.Writes()
			if len(writes) == 0 || writes[0].OpenReq == nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			testTunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
				TrafficID: writes[0].OpenReq.TrafficID,
				Success:   true,
			}})
			testTunnel.EnqueueReadPayload(pb.StreamPayload{
				Data: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello"),
			})
			testTunnel.EnqueueReadPayload(pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: writes[0].OpenReq.TrafficID,
					Accepted:  true,
				},
			})
			testTunnel.EnqueueReadPayload(pb.StreamPayload{
				RecycleAck: &pb.TunnelRecycleAck{
					TunnelID:   "tunnel-http-connector-1",
					RecycleSeq: 1,
					Accepted:   true,
				},
			})
			return
		}
	}()
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", testTunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.dev.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	request.Header.Set("X-Request-Id", "trace-connector-1")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "hello" {
		testingObject.Fatalf("unexpected response body: got=%s want=%s", recorder.Body.String(), "hello")
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/plain") {
		testingObject.Fatalf("unexpected content type: %s", recorder.Header().Get("Content-Type"))
	}
	if recorder.Header().Get("X-DevBridge-Route-Id") != "route-http-connector-1" {
		testingObject.Fatalf("unexpected route header: %s", recorder.Header().Get("X-DevBridge-Route-Id"))
	}
	if recorder.Header().Get("X-DevBridge-Target-Kind") != string(pb.RouteTargetTypeConnectorService) {
		testingObject.Fatalf("unexpected target kind header: %s", recorder.Header().Get("X-DevBridge-Target-Kind"))
	}

	writes := testTunnel.Writes()
	if len(writes) < 4 {
		testingObject.Fatalf("expected at least open+data+close+recycle writes, got=%d", len(writes))
	}
	if writes[0].OpenReq == nil {
		testingObject.Fatalf("expected first write to be open request")
	}
	var tunneledHTTPRequest bytes.Buffer
	for _, payload := range writes {
		if len(payload.Data) == 0 {
			continue
		}
		_, _ = tunneledHTTPRequest.Write(payload.Data)
	}
	serializedRequest := tunneledHTTPRequest.String()
	if !strings.Contains(serializedRequest, "GET /v1/orders HTTP/1.1") {
		testingObject.Fatalf("unexpected tunneled request line: %s", serializedRequest)
	}
	if !strings.Contains(serializedRequest, "Host: api.dev.local") {
		testingObject.Fatalf("unexpected tunneled request host header: %s", serializedRequest)
	}
	hasClose := false
	hasRecycle := false
	for _, payload := range writes {
		if payload.Close != nil {
			hasClose = true
		}
		if payload.Recycle != nil {
			hasRecycle = true
		}
	}
	if !hasClose {
		testingObject.Fatalf("expected close payload written during connector proxy flow")
	}
	if !hasRecycle {
		testingObject.Fatalf("expected recycle payload written during connector proxy flow")
	}
	if testTunnel.closeCount != 0 {
		testingObject.Fatalf("expected tunnel kept for reuse, close_count=%d", testTunnel.closeCount)
	}
	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 1 || snapshot.IdleCount != 1 {
		testingObject.Fatalf("expected one recycled idle tunnel after connector proxy, total=%d idle=%d", snapshot.TotalCount, snapshot.IdleCount)
	}
}

// TestIngressHTTPHandlerConnectorReusesTunnelAcrossSequentialRequests 验证同一 tunnel 连续请求可回收入池并继续复用。
func TestIngressHTTPHandlerConnectorReusesTunnelAcrossSequentialRequests(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-http-connector-reuse",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.reuse.local",
			PathPrefix: "/v1",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
	})

	const expectedRequests = 3
	testTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-connector-reuse-1")
	go func() {
		handledRequests := 0
		seenTrafficIDs := make(map[string]struct{}, expectedRequests)
		for handledRequests < expectedRequests {
			matchedOpen := false
			writes := testTunnel.Writes()
			for _, payload := range writes {
				if payload.OpenReq == nil {
					continue
				}
				trafficID := strings.TrimSpace(payload.OpenReq.TrafficID)
				if trafficID == "" {
					continue
				}
				if _, exists := seenTrafficIDs[trafficID]; exists {
					continue
				}
				seenTrafficIDs[trafficID] = struct{}{}
				handledRequests++
				matchedOpen = true
				responseBody := fmt.Sprintf("reuse-hello-%d", handledRequests)
				testTunnel.EnqueueReadPayload(pb.StreamPayload{
					OpenAck: &pb.TrafficOpenAck{
						TrafficID: trafficID,
						Success:   true,
					},
				})
				testTunnel.EnqueueReadPayload(pb.StreamPayload{
					Data: []byte(fmt.Sprintf(
						"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
						len(responseBody),
						responseBody,
					)),
				})
				testTunnel.EnqueueReadPayload(pb.StreamPayload{
					CloseAck: &pb.TrafficCloseAck{
						TrafficID: trafficID,
						Accepted:  true,
					},
				})
				testTunnel.EnqueueReadPayload(pb.StreamPayload{
					RecycleAck: &pb.TunnelRecycleAck{
						TunnelID:   "tunnel-http-connector-reuse-1",
						RecycleSeq: uint64(handledRequests),
						Accepted:   true,
					},
				})
				break
			}
			if matchedOpen {
				continue
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", testTunnel); err != nil {
		testingObject.Fatalf("register idle tunnel failed: %v", err)
	}

	for requestIndex := 1; requestIndex <= expectedRequests; requestIndex++ {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1/v1/orders?req=%d", requestIndex), nil)
		request.Host = "api.reuse.local"
		request.Header.Set("X-Namespace", "dev")
		request.Header.Set("X-Env", "demo")
		recorder := httptest.NewRecorder()

		doneChannel := make(chan struct{})
		go func() {
			runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)
			close(doneChannel)
		}()
		select {
		case <-doneChannel:
		case <-time.After(2 * time.Second):
			testingObject.Fatalf("connector request #%d timed out (possible stall)", requestIndex)
		}

		if recorder.Code != http.StatusOK {
			testingObject.Fatalf(
				"unexpected status code for request #%d: got=%d want=%d body=%s",
				requestIndex,
				recorder.Code,
				http.StatusOK,
				recorder.Body.String(),
			)
		}
		expectedBody := fmt.Sprintf("reuse-hello-%d", requestIndex)
		if recorder.Body.String() != expectedBody {
			testingObject.Fatalf(
				"unexpected body for request #%d: got=%s want=%s",
				requestIndex,
				recorder.Body.String(),
				expectedBody,
			)
		}
	}

	writes := testTunnel.Writes()
	openCount := 0
	closeCount := 0
	recycleCount := 0
	for _, payload := range writes {
		if payload.OpenReq != nil {
			openCount++
		}
		if payload.Close != nil {
			closeCount++
		}
		if payload.Recycle != nil {
			recycleCount++
		}
	}
	if openCount != expectedRequests {
		testingObject.Fatalf("unexpected open request count: got=%d want=%d", openCount, expectedRequests)
	}
	if closeCount != expectedRequests {
		testingObject.Fatalf("unexpected close write count: got=%d want=%d", closeCount, expectedRequests)
	}
	if recycleCount != expectedRequests {
		testingObject.Fatalf("unexpected recycle write count: got=%d want=%d", recycleCount, expectedRequests)
	}
	if testTunnel.closeCount != 0 {
		testingObject.Fatalf("expected tunnel kept for reuse, close_count=%d", testTunnel.closeCount)
	}
	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 1 || snapshot.IdleCount != 1 || snapshot.ActiveCount != 0 {
		testingObject.Fatalf(
			"unexpected registry snapshot after sequential reuse: total=%d idle=%d active=%d",
			snapshot.TotalCount,
			snapshot.IdleCount,
			snapshot.ActiveCount,
		)
	}
}

// TestIngressHTTPHandlerConnectorRetriesUnexpectedEOF 验证 connector 响应读 EOF 时会对幂等请求重试一次。
func TestIngressHTTPHandlerConnectorRetriesUnexpectedEOF(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-http-connector-retry",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.retry-eof.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
	})

	firstTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-retry-1")
	go func() {
		for {
			writes := firstTunnel.Writes()
			if len(writes) == 0 || writes[0].OpenReq == nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			firstTunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
				TrafficID: writes[0].OpenReq.TrafficID,
				Success:   true,
			}})
			firstTunnel.readQueue <- runtimeDataPlaneReadResult{err: io.ErrUnexpectedEOF}
			return
		}
	}()

	secondTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-retry-2")
	go func() {
		for {
			writes := secondTunnel.Writes()
			if len(writes) == 0 || writes[0].OpenReq == nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
				TrafficID: writes[0].OpenReq.TrafficID,
				Success:   true,
			}})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				Data: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 11\r\n\r\nretry-hello"),
			})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: writes[0].OpenReq.TrafficID,
					Accepted:  true,
				},
			})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				RecycleAck: &pb.TunnelRecycleAck{
					TunnelID:   "tunnel-http-retry-2",
					RecycleSeq: 1,
					Accepted:   true,
				},
			})
			return
		}
	}()

	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", firstTunnel); err != nil {
		testingObject.Fatalf("register first retry tunnel failed: %v", err)
	}
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", secondTunnel); err != nil {
		testingObject.Fatalf("register second retry tunnel failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.retry-eof.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code after eof retry: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "retry-hello" {
		testingObject.Fatalf("unexpected response body after eof retry: %s", recorder.Body.String())
	}
	if firstTunnel.closeCount != 1 {
		testingObject.Fatalf("expected first tunnel closed once after eof, got=%d", firstTunnel.closeCount)
	}
	if secondTunnel.closeCount != 0 {
		testingObject.Fatalf("expected second tunnel kept for reuse after success, close_count=%d", secondTunnel.closeCount)
	}
	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 1 || snapshot.IdleCount != 1 {
		testingObject.Fatalf("expected one recycled idle tunnel after retry success, total=%d idle=%d", snapshot.TotalCount, snapshot.IdleCount)
	}
}

// TestIngressHTTPHandlerConnectorNoRetryAfterCommittedEOF 验证响应已写回后出现 EOF 不会触发二次重试。
func TestIngressHTTPHandlerConnectorNoRetryAfterCommittedEOF(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-http-connector-committed-eof",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.committed-eof.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
			ConnectorService: &pb.ConnectorServiceTarget{
				ServiceKey: "dev/demo/order-service",
			},
		},
	})

	firstTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-committed-eof-1")
	go func() {
		for {
			writes := firstTunnel.Writes()
			if len(writes) == 0 || writes[0].OpenReq == nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			trafficID := strings.TrimSpace(writes[0].OpenReq.TrafficID)
			firstTunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
				TrafficID: trafficID,
				Success:   true,
			}})
			firstTunnel.EnqueueReadPayload(pb.StreamPayload{
				Data: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 11\r\n\r\nhello"),
			})
			firstTunnel.EnqueueReadPayload(pb.StreamPayload{
				Close: &pb.TrafficClose{
					TrafficID: trafficID,
					Reason:    "upstream_body_eof",
				},
			})
			return
		}
	}()

	secondTunnel := newRuntimeDataPlaneTestTunnel("tunnel-http-committed-eof-2")
	go func() {
		deadline := time.Now().UTC().Add(1500 * time.Millisecond)
		for time.Now().UTC().Before(deadline) {
			writes := secondTunnel.Writes()
			if len(writes) == 0 || writes[0].OpenReq == nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			trafficID := strings.TrimSpace(writes[0].OpenReq.TrafficID)
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{OpenAck: &pb.TrafficOpenAck{
				TrafficID: trafficID,
				Success:   true,
			}})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				Data: []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 10\r\n\r\nretry-body"),
			})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: trafficID,
					Accepted:  true,
				},
			})
			secondTunnel.EnqueueReadPayload(pb.StreamPayload{
				RecycleAck: &pb.TunnelRecycleAck{
					TunnelID:   "tunnel-http-committed-eof-2",
					RecycleSeq: 1,
					Accepted:   true,
				},
			})
			return
		}
	}()

	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", firstTunnel); err != nil {
		testingObject.Fatalf("register first tunnel failed: %v", err)
	}
	if _, err := runtime.RegisterIdleTunnel("connector-1", "session-1", secondTunnel); err != nil {
		testingObject.Fatalf("register second tunnel failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.committed-eof.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code after committed eof: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "hello" {
		testingObject.Fatalf("unexpected response body after committed eof: %s", recorder.Body.String())
	}
	if firstTunnel.closeCount != 1 {
		testingObject.Fatalf("expected first tunnel closed once after committed eof, got=%d", firstTunnel.closeCount)
	}
	if secondTunnel.closeCount != 0 {
		testingObject.Fatalf("expected second tunnel untouched, close_count=%d", secondTunnel.closeCount)
	}
	if len(secondTunnel.Writes()) != 0 {
		testingObject.Fatalf("expected no retry dispatch to second tunnel, writes=%d", len(secondTunnel.Writes()))
	}
	snapshot := runtime.dataPlane.tunnelRegistry.Snapshot()
	if snapshot.TotalCount != 1 || snapshot.IdleCount != 1 {
		testingObject.Fatalf(
			"expected only second tunnel kept idle after committed eof, total=%d idle=%d",
			snapshot.TotalCount,
			snapshot.IdleCount,
		)
	}
}

// TestWrapIngressConnectorDispatchErrorPreservesCommittedMarker 验证 dispatch 错误包装会保留 committed 语义。
func TestWrapIngressConnectorDispatchErrorPreservesCommittedMarker(testingObject *testing.T) {
	testingObject.Parallel()

	committedByStateErr := wrapIngressConnectorDispatchError(
		"proxy connector ingress: dispatch connector path",
		io.ErrUnexpectedEOF,
		true,
	)
	if !errors.Is(committedByStateErr, errIngressResponseCommitted) {
		testingObject.Fatalf("expected committed marker from responseCommitted=true, got=%v", committedByStateErr)
	}

	committedBySourceErr := wrapIngressConnectorDispatchError(
		"proxy connector ingress: dispatch connector path",
		markIngressResponseCommitted(io.EOF),
		false,
	)
	if !errors.Is(committedBySourceErr, errIngressResponseCommitted) {
		testingObject.Fatalf("expected committed marker from source error, got=%v", committedBySourceErr)
	}

	normalErr := wrapIngressConnectorDispatchError(
		"proxy connector ingress: dispatch connector path",
		io.EOF,
		false,
	)
	if errors.Is(normalErr, errIngressResponseCommitted) {
		testingObject.Fatalf("unexpected committed marker for non-committed error: %v", normalErr)
	}
	if !errors.Is(normalErr, io.EOF) {
		testingObject.Fatalf("expected wrapped EOF retained, got=%v", normalErr)
	}
}

// TestIngressHTTPHandlerHybridFallsBackToExternal 验证 hybrid 在 connector 无 idle 时回落 external。
func TestIngressHTTPHandlerHybridFallsBackToExternal(testingObject *testing.T) {
	testingObject.Parallel()

	externalServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("hybrid-fallback-ok"))
	}))
	defer externalServer.Close()
	externalEndpoint := strings.TrimPrefix(externalServer.URL, "http://")

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-hybrid-http-1",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.hybrid.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeHybridGroup,
			HybridGroup: &pb.HybridGroupTarget{
				PrimaryConnectorService: pb.ConnectorServiceTarget{
					ServiceKey: "dev/demo/order-service",
				},
				FallbackExternalService: pb.ExternalServiceTarget{
					Namespace:   "dev",
					Environment: "demo",
					ServiceName: "order-fallback",
					Selector: map[string]string{
						"endpoint": externalEndpoint,
					},
				},
				FallbackPolicy: pb.FallbackPolicyPreOpenOnly,
			},
		},
	})

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.hybrid.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected hybrid status: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "hybrid-fallback-ok" {
		testingObject.Fatalf("unexpected hybrid fallback body: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-DevBridge-Target-Kind") != string(pb.RouteTargetTypeHybridGroup) {
		testingObject.Fatalf("unexpected hybrid target kind header: %s", recorder.Header().Get("X-DevBridge-Target-Kind"))
	}
	if recorder.Header().Get("X-DevBridge-Hybrid-Path") != "fallback" {
		testingObject.Fatalf("unexpected hybrid path header: %s", recorder.Header().Get("X-DevBridge-Hybrid-Path"))
	}
	if recorder.Header().Get("X-DevBridge-Hybrid-Fallback-Stage") == "" {
		testingObject.Fatalf("expected non-empty hybrid fallback stage")
	}
}

// TestIngressHTTPHandlerExternalMissingEndpointReturnsStructuredError 验证 external endpoint 缺失会返回协议错误码。
func TestIngressHTTPHandlerExternalMissingEndpointReturnsStructuredError(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, ":0")
	now := time.Now().UTC()

	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID:     "route-external-missing-endpoint",
		Namespace:   "dev",
		Environment: "demo",
		Match: pb.RouteMatch{
			Protocol:   "http",
			Host:       "api.external.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "svc-without-endpoint",
				Selector:    map[string]string{},
			},
		},
	})

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/orders", nil)
	request.Host = "api.external.local"
	request.Header.Set("X-Namespace", "dev")
	request.Header.Set("X-Env", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressHTTPServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode error response failed: %v", err)
	}
	if response.Error.Code != ltfperrors.CodeDiscoveryNoEndpoint {
		testingObject.Fatalf(
			"unexpected error code: got=%s want=%s body=%s",
			response.Error.Code,
			ltfperrors.CodeDiscoveryNoEndpoint,
			recorder.Body.String(),
		)
	}
}
