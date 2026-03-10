package discovery

import (
	"context"
	"testing"
	"time"

	serviceregistry "github.com/lifei6671/devbridge-loop/service-registry"
)

type staticResolver struct {
	name     string
	endpoint Endpoint
	matched  bool
	err      error
}

func (r staticResolver) Name() string {
	if r.name == "" {
		return "static"
	}
	return r.name
}

func (r staticResolver) Resolve(_ context.Context, _ Query) (Endpoint, bool, error) {
	return r.endpoint, r.matched, r.err
}

func TestSharedDiscoveryAdapterSearchMatched(t *testing.T) {
	adapter := NewSharedDiscoveryAdapter(staticResolver{
		name: "local",
		endpoint: Endpoint{
			Host:   "127.0.0.1",
			Port:   18080,
			Source: "local",
			Metadata: map[string]string{
				"zone": "cn-hz-a",
			},
		},
		matched: true,
	}, "base", "http", 20*time.Millisecond)

	result, err := adapter.Search(context.Background(), serviceregistry.SearchInput{
		Name:    "user-service",
		Version: "v1.0.0",
		Metadata: serviceregistry.Metadata{
			"env":      "dev-alice",
			"protocol": "grpc",
		},
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("unexpected service count: %d", len(result))
	}
	if result[0].GetName() != "user-service" || result[0].GetVersion() != "v1.0.0" {
		t.Fatalf("unexpected service info: name=%s version=%s", result[0].GetName(), result[0].GetVersion())
	}
	endpoints := result[0].GetEndpoints()
	if len(endpoints) != 1 || endpoints[0].Host() != "127.0.0.1" || endpoints[0].Port() != 18080 {
		t.Fatalf("unexpected endpoint result: %+v", endpoints)
	}
}

func TestSharedDiscoveryAdapterWatch(t *testing.T) {
	adapter := NewSharedDiscoveryAdapter(staticResolver{
		name:     "noop",
		endpoint: Endpoint{},
		matched:  false,
	}, "base", "http", 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := adapter.Watch(ctx, "/services")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	defer watcher.Close()

	result, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("proceed failed: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("unexpected watch result: %+v", result)
	}
}
