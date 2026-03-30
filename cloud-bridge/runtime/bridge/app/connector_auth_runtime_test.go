package app

import (
	"path/filepath"
	"testing"
	"time"

	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

func TestBuildConnectorManagedTokenStoreMemoryInjectsDefaultDevToken(testingObject *testing.T) {
	testingObject.Parallel()

	tokenStore, err := buildConnectorManagedTokenStore(ConnectorTokenStoreConfig{Driver: "memory"})
	if err != nil {
		testingObject.Fatalf("build connector managed token store failed: %v", err)
	}
	records, err := tokenStore.List()
	if err != nil {
		testingObject.Fatalf("list token records failed: %v", err)
	}
	if len(records) != 1 {
		testingObject.Fatalf("unexpected token record count: got=%d want=1", len(records))
	}
	if records[0].ConnectorID != "agent-local" {
		testingObject.Fatalf("unexpected connector id: got=%s want=agent-local", records[0].ConnectorID)
	}

	sessionRegistry := registry.NewSessionRegistry()
	coordinator := appauth.NewCoordinator(appauth.CoordinatorOptions{
		SessionRegistry: sessionRegistry,
		TokenStore:      tokenStore,
	})
	authResult := coordinator.AuthenticateAndCommit(
		appauth.Request{
			ConnectorID:          "agent-local",
			AssignedSessionEpoch: 1,
			AuthMethod:           "token",
			Token:                "dbt_agent-local.agent-dev-secret",
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if !authResult.Success {
		testingObject.Fatalf("expected default dev token auth to succeed, got code=%s msg=%s", authResult.ErrorCode, authResult.ErrorMessage)
	}
}

func TestBuildConnectorManagedTokenStoreFileLoadsPersistedTokensOnly(testingObject *testing.T) {
	testingObject.Parallel()

	tokenFilePath := filepath.Join(testingObject.TempDir(), "bridge.tokens.yaml")
	persistedStore, err := appauth.NewFileTokenStore(tokenFilePath)
	if err != nil {
		testingObject.Fatalf("new file token store failed: %v", err)
	}
	tokenAdmin := appauth.NewTokenAdmin(persistedStore)
	issueResult, err := tokenAdmin.Create(appauth.TokenCreateRequest{ConnectorID: "agent-file"})
	if err != nil {
		testingObject.Fatalf("create persisted token failed: %v", err)
	}

	tokenStore, err := buildConnectorManagedTokenStore(ConnectorTokenStoreConfig{
		Driver: "file",
		File:   ConnectorTokenFileStoreConfig{Path: tokenFilePath},
	})
	if err != nil {
		testingObject.Fatalf("build connector managed token store failed: %v", err)
	}

	sessionRegistry := registry.NewSessionRegistry()
	coordinator := appauth.NewCoordinator(appauth.CoordinatorOptions{
		SessionRegistry: sessionRegistry,
		TokenStore:      tokenStore,
	})
	fileAuthResult := coordinator.AuthenticateAndCommit(
		appauth.Request{
			ConnectorID:          "agent-file",
			AssignedSessionEpoch: 1,
			AuthMethod:           "token",
			Token:                issueResult.PlaintextToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if !fileAuthResult.Success {
		testingObject.Fatalf("expected persisted file token auth to succeed, got code=%s msg=%s", fileAuthResult.ErrorCode, fileAuthResult.ErrorMessage)
	}

	devAuthResult := coordinator.AuthenticateAndCommit(
		appauth.Request{
			ConnectorID:          "agent-local",
			AssignedSessionEpoch: 1,
			AuthMethod:           "token",
			Token:                "dbt_agent-local.agent-dev-secret",
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			return nil
		},
	)
	if devAuthResult.Success {
		testingObject.Fatalf("expected default dev token auth to fail for file store")
	}
	if devAuthResult.ErrorCode != appauth.AuthErrorInvalidToken {
		testingObject.Fatalf("unexpected dev token auth error code: got=%s want=%s", devAuthResult.ErrorCode, appauth.AuthErrorInvalidToken)
	}
}

func TestNewControlPlaneServerUsesInjectedAuthCoordinator(testingObject *testing.T) {
	testingObject.Parallel()

	injectedCoordinator := &stubAuthCoordinator{}
	server, err := newControlPlaneServer(DefaultConfig().ControlPlane, controlPlaneDependencies{
		authCoordinator: injectedCoordinator,
	})
	if err != nil {
		testingObject.Fatalf("new control plane server failed: %v", err)
	}
	if server.dispatcher == nil {
		testingObject.Fatalf("expected dispatcher initialized")
	}
	if server.dispatcher.authCoordinator != injectedCoordinator {
		testingObject.Fatalf("expected injected auth coordinator to be wired into dispatcher")
	}
}

type stubAuthCoordinator struct{}

func (stubAuthCoordinator *stubAuthCoordinator) AuthenticateAndCommit(
	request appauth.Request,
	commit func(now time.Time, sessionRuntime registry.SessionRuntime) error,
) appauth.Result {
	return appauth.Result{
		Success:      false,
		ErrorCode:    appauth.AuthErrorInternal,
		ErrorMessage: "stub",
	}
}
