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
