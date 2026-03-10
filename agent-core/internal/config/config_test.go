package config

import (
	"slices"
	"testing"
)

func TestLoadFromEnvDefaultResolveOrder(t *testing.T) {
	t.Setenv("DEVLOOP_ENV_RESOLVE_ORDER", "")
	cfg := LoadFromEnv()
	expected := []string{"requestHeader", "payload", "runtimeDefault", "baseFallback"}
	if !slices.Equal(cfg.EnvResolve.Order, expected) {
		t.Fatalf("unexpected resolve order: got=%v expected=%v", cfg.EnvResolve.Order, expected)
	}
}

func TestLoadFromEnvCustomResolveOrder(t *testing.T) {
	t.Setenv("DEVLOOP_ENV_RESOLVE_ORDER", "payload,requestHeader,base")
	cfg := LoadFromEnv()
	expected := []string{"payload", "requestHeader", "baseFallback"}
	if !slices.Equal(cfg.EnvResolve.Order, expected) {
		t.Fatalf("unexpected resolve order: got=%v expected=%v", cfg.EnvResolve.Order, expected)
	}
}

func TestLoadFromEnvDefaultHTTPAddrSupportsLAN(t *testing.T) {
	t.Setenv("DEVLOOP_AGENT_HTTP_ADDR", "")
	cfg := LoadFromEnv()
	if cfg.HTTPAddr != "0.0.0.0:39090" {
		t.Fatalf("unexpected default agent http addr: %s", cfg.HTTPAddr)
	}
}

func TestLoadFromEnvBackflowDefaultUsesLoopbackForWildcardListenAddr(t *testing.T) {
	t.Setenv("DEVLOOP_AGENT_HTTP_ADDR", "0.0.0.0:49090")
	t.Setenv("DEVLOOP_TUNNEL_BACKFLOW_BASE_URL", "")
	cfg := LoadFromEnv()
	if cfg.Tunnel.BackflowBaseURL != "http://127.0.0.1:49090" {
		t.Fatalf("unexpected backflow default: %s", cfg.Tunnel.BackflowBaseURL)
	}
}
