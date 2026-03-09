package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func newTestHandlerForBackflowGRPC() *Handler {
	return NewHandler(config.Config{
		EnvName: "dev-alice",
		Tunnel: config.TunnelConfig{
			RequestTimeout: 50 * time.Millisecond,
		},
	}, store.NewMemoryStore(), nil, nil)
}

func TestBackflowGRPCInvalidPayload(t *testing.T) {
	handler := newTestHandlerForBackflowGRPC()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backflow/grpc", strings.NewReader("{"))
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "invalid backflow grpc payload") {
		t.Fatalf("unexpected response body: %s", resp.Body.String())
	}
}

func TestBackflowGRPCMissingTarget(t *testing.T) {
	handler := newTestHandlerForBackflowGRPC()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backflow/grpc", strings.NewReader(`{"healthService":"demo"}`))
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "targetHost/targetPort are required") {
		t.Fatalf("unexpected response body: %s", resp.Body.String())
	}
}

func TestClassifyBackflowGRPCError(t *testing.T) {
	statusCode, errorCode, message := classifyBackflowGRPCError(grpcstatus.Error(codes.DeadlineExceeded, "timeout"))
	if statusCode != http.StatusGatewayTimeout || errorCode != domain.ErrorUpstreamTimeout || !strings.Contains(message, "timeout") {
		t.Fatalf("unexpected deadline classification: status=%d code=%s message=%s", statusCode, errorCode, message)
	}

	statusCode, errorCode, message = classifyBackflowGRPCError(grpcstatus.Error(codes.Unavailable, "down"))
	if statusCode != http.StatusBadGateway || errorCode != domain.ErrorLocalEndpointDown || !strings.Contains(message, "down") {
		t.Fatalf("unexpected unavailable classification: status=%d code=%s message=%s", statusCode, errorCode, message)
	}
}
