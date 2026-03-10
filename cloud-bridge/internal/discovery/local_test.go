package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFileResolverResolve(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "discovery.yaml")
	content := `
routes:
  - env: base
    serviceName: user
    protocol: http
    host: 127.0.0.1
    port: 8081
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write discovery file failed: %v", err)
	}

	resolver, err := NewLocalFileResolver(configPath)
	if err != nil {
		t.Fatalf("new local resolver failed: %v", err)
	}

	result, matched, err := resolver.Resolve(context.Background(), Query{
		Env:         "base",
		ServiceName: "user",
		Protocol:    "http",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !matched {
		t.Fatalf("expected local route matched")
	}
	if result.Host != "127.0.0.1" || result.Port != 8081 {
		t.Fatalf("unexpected endpoint: %+v", result)
	}
}

func TestLocalFileResolverMissingFile(t *testing.T) {
	resolver, err := NewLocalFileResolver(filepath.Join(t.TempDir(), "not-exists.yaml"))
	if err != nil {
		t.Fatalf("new local resolver with missing file should not fail: %v", err)
	}
	_, matched, err := resolver.Resolve(context.Background(), Query{
		Env:         "base",
		ServiceName: "user",
		Protocol:    "http",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if matched {
		t.Fatalf("unexpected matched for missing local file")
	}
}

func TestLocalFileResolverResolveFromNestedDiscoveryRoutes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bridge.yaml")
	content := `
httpAddr: 0.0.0.0:38080
discovery:
  local:
    routes:
      - env: base
        serviceName: order
        protocol: grpc
        host: 127.0.0.1
        port: 9091
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write nested discovery file failed: %v", err)
	}

	resolver, err := NewLocalFileResolver(configPath)
	if err != nil {
		t.Fatalf("new local resolver failed: %v", err)
	}

	result, matched, err := resolver.Resolve(context.Background(), Query{
		Env:         "base",
		ServiceName: "order",
		Protocol:    "grpc",
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !matched {
		t.Fatalf("expected nested discovery route matched")
	}
	if result.Host != "127.0.0.1" || result.Port != 9091 {
		t.Fatalf("unexpected endpoint: %+v", result)
	}
}
