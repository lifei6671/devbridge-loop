package registry

import "testing"

func TestNewAdapterAgent(t *testing.T) {
	adapter, err := NewAdapter(Options{
		Type:      RegistryTypeAgent,
		AgentAddr: "127.0.0.1:19090",
	})
	if err != nil {
		t.Fatalf("new adapter failed: %v", err)
	}
	if adapter == nil {
		t.Fatalf("adapter should not be nil")
	}
}

func TestNewAdapterUnsupportedType(t *testing.T) {
	adapter, err := NewAdapter(Options{
		Type: "consul",
	})
	if err == nil {
		t.Fatalf("expected error for unsupported type, got adapter=%T", adapter)
	}
}
