package forwarder

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

func TestHTTPForwarderForward(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	host, portText, err := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	if err != nil {
		t.Fatalf("split target host failed: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse target port failed: %v", err)
	}
	if host == "" {
		host = "127.0.0.1"
	}

	f := NewHTTPForwarder(2 * time.Second)
	resp, err := f.Forward(context.Background(), domain.BackflowHTTPRequest{
		Method:     http.MethodPost,
		Path:       "/api/ping",
		RawQuery:   "a=1",
		Host:       "user.dev-alice.internal",
		TargetHost: host,
		TargetPort: port,
		Protocol:   "http",
		Body:       []byte(`{"name":"devloop"}`),
	})
	if err != nil {
		t.Fatalf("forward should succeed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %+v", resp)
	}
}

func TestHTTPForwarderTimeout(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	host, portText, err := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	if err != nil {
		t.Fatalf("split target host failed: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse target port failed: %v", err)
	}
	if host == "" {
		host = "127.0.0.1"
	}

	f := NewHTTPForwarder(30 * time.Millisecond)
	_, err = f.Forward(context.Background(), domain.BackflowHTTPRequest{
		Method:     http.MethodGet,
		Path:       "/",
		TargetHost: host,
		TargetPort: port,
		Protocol:   "http",
	})
	if err == nil {
		t.Fatalf("forward should timeout")
	}

	var forwardErr *ForwardError
	if !errors.As(err, &forwardErr) {
		t.Fatalf("expected ForwardError, got %v", err)
	}
	if forwardErr.Code != domain.ErrorUpstreamTimeout {
		t.Fatalf("unexpected error code: %+v", forwardErr)
	}
}
