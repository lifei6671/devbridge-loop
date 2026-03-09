package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

func TestStateErrorsEndpoint(t *testing.T) {
	stateStore := store.NewMemoryStore()
	stateStore.AddError("ROUTE_NOT_FOUND", "route not found", map[string]string{"serviceName": "user"})
	stateStore.AddError("TUNNEL_OFFLINE", "tunnel offline", map[string]string{"tunnelId": "tunnel-a"})
	stateStore.AddError("ROUTE_NOT_FOUND", "route not found again", nil)

	handler := NewHandler(routing.NewPipeline([]string{"host"}), stateStore, &mockBackflowCaller{}, "")
	req := httptest.NewRequest(http.MethodGet, "http://bridge.internal/api/v1/state/errors", nil)
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}

	var stats domain.ErrorStats
	if err := json.Unmarshal(resp.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if stats.Total != 3 || stats.UniqueCode != 2 {
		t.Fatalf("unexpected error stats: %+v", stats)
	}
	if len(stats.ByCode) == 0 || stats.ByCode[0].Code != "ROUTE_NOT_FOUND" || stats.ByCode[0].Count != 2 {
		t.Fatalf("unexpected byCode aggregate: %+v", stats.ByCode)
	}
}

func TestTunnelEventInvalidPayloadRecordedAsError(t *testing.T) {
	stateStore := store.NewMemoryStore()
	handler := NewHandler(routing.NewPipeline([]string{"host"}), stateStore, &mockBackflowCaller{}, "")

	req := httptest.NewRequest(http.MethodPost, "http://bridge.internal/api/v1/tunnel/events", strings.NewReader("{"))
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}

	stats := stateStore.ListErrorStats()
	if stats.Total != 1 {
		t.Fatalf("expected total=1, got %d", stats.Total)
	}
	if len(stats.ByCode) != 1 || stats.ByCode[0].Code != domain.SyncErrorInvalidPayload {
		t.Fatalf("unexpected byCode: %+v", stats.ByCode)
	}
	if len(stats.Recent) != 1 || stats.Recent[0].Code != domain.SyncErrorInvalidPayload {
		t.Fatalf("unexpected recent errors: %+v", stats.Recent)
	}
}
