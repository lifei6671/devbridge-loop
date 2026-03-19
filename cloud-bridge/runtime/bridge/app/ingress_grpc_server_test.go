package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestBootstrapInitializesIngressGRPCServerWhenGRPCAddrSet(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Ingress.GRPCAddr = ":18081"

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.ingressGRPCServer == nil {
		testingObject.Fatalf("expected ingress grpc server initialized")
	}
	if runtime.ingressGRPCServer.Addr != config.Ingress.GRPCAddr {
		testingObject.Fatalf("unexpected ingress grpc addr: got=%s want=%s", runtime.ingressGRPCServer.Addr, config.Ingress.GRPCAddr)
	}
}

func TestBootstrapSkipsIngressGRPCServerWhenGRPCAddrEmpty(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.Ingress.GRPCAddr = ""

	runtime, err := Bootstrap(context.Background(), config)
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	if runtime.ingressGRPCServer != nil {
		testingObject.Fatalf("expected ingress grpc server nil when ingress.grpc_addr empty")
	}
}

func TestIngressGRPCHandlerRejectsNonGRPCContentType(testingObject *testing.T) {
	testingObject.Parallel()

	runtime := newRuntimeWithDataPlaneDependenciesForTest(testingObject, runtimeDataPlaneDependencies{})
	runtime.ingressGRPCServer = newIngressGRPCServer(runtime, ":0")
	if runtime.ingressGRPCServer == nil {
		testingObject.Fatalf("expected ingress grpc server initialized")
	}

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/devbridge.loop.v1.Runtime/Ping", bytes.NewReader([]byte("payload")))
	request.Host = "api.grpc.local"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	runtime.ingressGRPCServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnsupportedMediaType {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusUnsupportedMediaType, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		testingObject.Fatalf("decode response failed: %v", err)
	}
	if response.Error.Code != ltfperrors.CodeUnsupportedValue {
		testingObject.Fatalf(
			"unexpected error code: got=%s want=%s body=%s",
			response.Error.Code,
			ltfperrors.CodeUnsupportedValue,
			recorder.Body.String(),
		)
	}
}

func TestIngressGRPCHandlerExternalProxyRelaysResponse(testingObject *testing.T) {
	testingObject.Parallel()

	received := struct {
		path string
		host string
		body []byte
	}{}
	endpointAddress, shutdownUpstream := startH2CUpstreamServerForTest(
		testingObject,
		func(writer http.ResponseWriter, request *http.Request) {
			payload, _ := ioReadAllForTest(request.Body)
			received.path = request.URL.Path
			received.host = request.Host
			received.body = payload
			writer.Header().Set("Content-Type", "application/grpc")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("grpc-external-ok"))
		},
	)
	defer shutdownUpstream()

	cfg := DefaultConfig()
	enableExternalFallbackPolicyForTest(&cfg, "dev")
	runtime := newRuntimeWithConfigAndDataPlaneDependenciesForTest(testingObject, cfg, runtimeDataPlaneDependencies{})
	runtime.ingressGRPCServer = newIngressGRPCServer(runtime, ":0")
	now := time.Now().UTC()
	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-grpc-external-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "grpc",
			Host:       "api.grpc.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "grpc-order",
				Selector: map[string]string{
					"endpoint": endpointAddress,
				},
			},
		},
	})

	requestBody := []byte{0x00, 0x00, 0x00, 0x00, 0x00}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/devbridge.loop.v1.Runtime/Ping", bytes.NewReader(requestBody))
	request.Host = "api.grpc.local"
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("X-Bridge-Namespace", "dev")
	request.Header.Set("X-Bridge-Environment", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressGRPCServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "grpc-external-ok" {
		testingObject.Fatalf("unexpected grpc external response body: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-DevBridge-Route-Id") != "route-grpc-external-1" {
		testingObject.Fatalf("unexpected route header: %s", recorder.Header().Get("X-DevBridge-Route-Id"))
	}
	if recorder.Header().Get("X-DevBridge-Target-Kind") != string(pb.RouteTargetTypeExternalService) {
		testingObject.Fatalf("unexpected target kind header: %s", recorder.Header().Get("X-DevBridge-Target-Kind"))
	}
	if received.path != "/devbridge.loop.v1.Runtime/Ping" {
		testingObject.Fatalf("unexpected upstream path: %s", received.path)
	}
	if received.host != "api.grpc.local" {
		testingObject.Fatalf("unexpected upstream host: %s", received.host)
	}
	if !bytes.Equal(received.body, requestBody) {
		testingObject.Fatalf("unexpected upstream body: got=%v want=%v", received.body, requestBody)
	}
}

func TestIngressGRPCHandlerHybridFallsBackToExternal(testingObject *testing.T) {
	testingObject.Parallel()

	endpointAddress, shutdownUpstream := startH2CUpstreamServerForTest(
		testingObject,
		func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/grpc")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("grpc-hybrid-fallback-ok"))
		},
	)
	defer shutdownUpstream()

	cfg := DefaultConfig()
	enableExternalFallbackPolicyForTest(&cfg, "dev")
	runtime := newRuntimeWithConfigAndDataPlaneDependenciesForTest(testingObject, cfg, runtimeDataPlaneDependencies{})
	runtime.ingressGRPCServer = newIngressGRPCServer(runtime, ":0")
	now := time.Now().UTC()
	seedConnectorServiceAndSession(runtime, now)
	runtime.dataPlane.routeRegistry.Upsert(now, pb.Route{
		RouteID: "route-grpc-hybrid-1",
		Scope: pb.Scope{
			Namespace:   "dev",
			Environment: "demo",
		},
		Match: pb.RouteMatch{
			Protocol:   "grpc",
			Host:       "api.grpc.hybrid.local",
			PathPrefix: "/",
		},
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeExternalService,
			ExternalService: &pb.ExternalServiceTarget{
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "grpc-order-fallback",
				Selector: map[string]string{
					"endpoint": endpointAddress,
				},
			},
		},
	})

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/devbridge.loop.v1.Runtime/Ping", bytes.NewReader([]byte{0x00, 0, 0, 0, 0}))
	request.Host = "api.grpc.hybrid.local"
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("X-Bridge-Namespace", "dev")
	request.Header.Set("X-Bridge-Environment", "demo")
	recorder := httptest.NewRecorder()
	runtime.ingressGRPCServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		testingObject.Fatalf("unexpected status code: got=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if recorder.Body.String() != "grpc-hybrid-fallback-ok" {
		testingObject.Fatalf("unexpected grpc external body: %s", recorder.Body.String())
	}
	if recorder.Header().Get("X-DevBridge-Target-Kind") != string(pb.RouteTargetTypeExternalService) {
		testingObject.Fatalf("unexpected target kind header: %s", recorder.Header().Get("X-DevBridge-Target-Kind"))
	}
}

func startH2CUpstreamServerForTest(
	testingObject *testing.T,
	handler http.HandlerFunc,
) (string, func()) {
	testingObject.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		testingObject.Fatalf("listen h2c upstream failed: %v", err)
	}
	server := &http.Server{
		Handler: h2c.NewHandler(handler, &http2.Server{}),
	}
	go func() {
		_ = server.Serve(listener)
	}()
	return listener.Addr().String(), func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		_ = listener.Close()
	}
}

func ioReadAllForTest(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, nil
	}
	buffer := bytes.NewBuffer(nil)
	if _, err := buffer.ReadFrom(reader); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
