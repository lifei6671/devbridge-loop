package app

import "testing"

func TestBuildConnectorManagedTokenStoreUsesMemoryDriverDefaultDevToken(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ConnectorAuth.TokenStore.Driver = "memory"
	config.ConnectorAuth.TokenStore.File.Path = ""

	store, err := buildConnectorManagedTokenStore(config.ConnectorAuth.TokenStore)
	if err != nil {
		testingObject.Fatalf("build connector managed token store failed: %v", err)
	}
	records, err := store.List()
	if err != nil {
		testingObject.Fatalf("list memory token store records failed: %v", err)
	}
	if len(records) != 1 {
		testingObject.Fatalf("unexpected memory token store size: got=%d want=1", len(records))
	}
	if records[0].TokenID != "agent-local" {
		testingObject.Fatalf("unexpected default dev token id: got=%s want=agent-local", records[0].TokenID)
	}
	if records[0].ConnectorID != "agent-local" {
		testingObject.Fatalf("unexpected default dev connector id: got=%s want=agent-local", records[0].ConnectorID)
	}
}

func TestBuildConnectorManagedTokenStoreUsesFileDriverWithoutDefaultDevToken(testingObject *testing.T) {
	testingObject.Parallel()

	config := DefaultConfig()
	config.ConnectorAuth.TokenStore.Driver = "file"
	config.ConnectorAuth.TokenStore.File.Path = testingObject.TempDir() + "/bridge.tokens.yaml"

	store, err := buildConnectorManagedTokenStore(config.ConnectorAuth.TokenStore)
	if err != nil {
		testingObject.Fatalf("build connector managed token store failed: %v", err)
	}
	records, err := store.List()
	if err != nil {
		testingObject.Fatalf("list file token store records failed: %v", err)
	}
	if len(records) != 0 {
		testingObject.Fatalf("unexpected file token store size: got=%d want=0", len(records))
	}
}
