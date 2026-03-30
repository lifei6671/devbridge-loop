package app

import (
	"fmt"
	"strings"

	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

type connectorAuthRuntime struct {
	tokenStore  appauth.ManagedTokenStore
	tokenAdmin  appauth.TokenAdmin
	coordinator appauth.Coordinator
}

func buildConnectorAuthRuntime(
	config Config,
	sessionRegistry *registry.SessionRegistry,
	metrics *obs.Metrics,
) (connectorAuthRuntime, error) {
	tokenStore, err := buildConnectorManagedTokenStore(config.ConnectorAuth.TokenStore)
	if err != nil {
		return connectorAuthRuntime{}, err
	}
	return connectorAuthRuntime{
		tokenStore: tokenStore,
		tokenAdmin: appauth.NewTokenAdmin(tokenStore),
		coordinator: appauth.NewCoordinator(appauth.CoordinatorOptions{
			SessionRegistry: sessionRegistry,
			TokenStore:      tokenStore,
			Metrics:         metrics,
		}),
	}, nil
}

func buildConnectorManagedTokenStore(config ConnectorTokenStoreConfig) (appauth.ManagedTokenStore, error) {
	switch normalizedDriver := strings.ToLower(strings.TrimSpace(config.Driver)); normalizedDriver {
	case "memory":
		return appauth.NewDefaultDevManagedTokenStore(), nil
	case "file":
		tokenStore, err := appauth.NewFileTokenStore(config.File.Path)
		if err != nil {
			return nil, fmt.Errorf("build connector token store: %w", err)
		}
		return tokenStore, nil
	default:
		return nil, fmt.Errorf("build connector token store: unsupported driver=%s", config.Driver)
	}
}
