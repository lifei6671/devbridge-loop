package serviceregistry

import (
	"context"
	"testing"
	"time"
)

func TestBasicServiceDefaults(t *testing.T) {
	service := NewBasicService("user-service")
	if service.GetName() != "user-service" {
		t.Fatalf("unexpected service name: %s", service.GetName())
	}
	if service.GetVersion() != "latest" {
		t.Fatalf("unexpected service version: %s", service.GetVersion())
	}
	if service.GetPrefix() != "/services/default/user-service" {
		t.Fatalf("unexpected service prefix: %s", service.GetPrefix())
	}
	if service.GetKey() != "/services/default/user-service/latest/user-service" {
		t.Fatalf("unexpected service key: %s", service.GetKey())
	}
}

func TestPollingWatcherProceed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	watcher, err := NewPollingWatcher(ctx, "/services", 5*time.Millisecond, func(_ context.Context, _ SearchInput) ([]Service, error) {
		calls++
		if calls == 1 {
			return []Service{}, nil
		}
		return []Service{
			NewBasicService("user", WithServiceVersion("v1.0.0"), WithServiceEndpoints(Endpoints{
				NewBasicEndpoint("127.0.0.1", 8080),
			})),
		}, nil
	})
	if err != nil {
		t.Fatalf("new polling watcher failed: %v", err)
	}
	defer watcher.Close()

	first, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("proceed first round failed: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("expected empty first round result")
	}

	second, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("proceed second round failed: %v", err)
	}
	if len(second) != 1 || second[0].GetName() != "user" {
		t.Fatalf("unexpected second round result: %+v", second)
	}
}
