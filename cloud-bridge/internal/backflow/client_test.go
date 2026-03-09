package backflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

func TestForwardHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/backflow/http" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(domain.BackflowHTTPResponse{
			StatusCode: 201,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: []byte(`{"ok":true}`),
		})
	}))
	defer srv.Close()

	client := NewClient(2 * time.Second)
	resp, err := client.ForwardHTTP(context.Background(), srv.URL, domain.BackflowHTTPRequest{
		Method:     http.MethodPost,
		Path:       "/ping",
		TargetHost: "127.0.0.1",
		TargetPort: 19090,
		Protocol:   "http",
	})
	if err != nil {
		t.Fatalf("forward should succeed: %v", err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("unexpected status code: %+v", resp)
	}
}

func TestForwardHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(domain.BackflowHTTPResponse{
			StatusCode: http.StatusServiceUnavailable,
			ErrorCode:  domain.IngressErrorTunnelOffline,
			Message:    "agent offline",
		})
	}))
	defer srv.Close()

	client := NewClient(2 * time.Second)
	_, err := client.ForwardHTTP(context.Background(), srv.URL, domain.BackflowHTTPRequest{
		Method:     http.MethodGet,
		Path:       "/",
		TargetHost: "127.0.0.1",
		TargetPort: 19090,
		Protocol:   "http",
	})
	if err == nil {
		t.Fatalf("forward should return error")
	}
}
