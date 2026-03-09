package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

func TestResolveDiscoverEnvByConfiguredOrder(t *testing.T) {
	handler := NewHandler(config.Config{
		EnvName: "dev-runtime",
		EnvResolve: config.EnvResolveConfig{
			Order: []string{"payload", "requestHeader", "runtimeDefault", "baseFallback"},
		},
	}, store.NewMemoryStore(), nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/discover", nil)
	req.Header.Set("x-env", "dev-header")

	resolved := handler.resolveDiscoverEnv(req, "dev-payload")
	if resolved != "dev-payload" {
		t.Fatalf("unexpected resolved env: %s", resolved)
	}
}

func TestResolveDiscoverEnvFallbackToBase(t *testing.T) {
	handler := NewHandler(config.Config{
		EnvName: "",
		EnvResolve: config.EnvResolveConfig{
			Order: []string{"requestHeader", "payload", "runtimeDefault", "baseFallback"},
		},
	}, store.NewMemoryStore(), nil, nil)

	req := httptest.NewRequest("POST", "/api/v1/discover", nil)
	resolved := handler.resolveDiscoverEnv(req, "")
	if resolved != "base" {
		t.Fatalf("unexpected resolved env: %s", resolved)
	}
}
