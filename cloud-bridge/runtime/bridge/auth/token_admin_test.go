package auth

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

func TestConnectorTokenAdminServiceIssueRotateAndRevoke(testingObject *testing.T) {
	testingObject.Parallel()

	currentTime := time.Date(2026, 3, 29, 18, 0, 0, 0, time.UTC)
	sessionRegistry := registry.NewSessionRegistry()
	store := newInMemoryConnectorTokenStore(nil)
	service := newTokenAdminService(tokenAdminServiceOptions{
		store: store,
		now: func() time.Time {
			return currentTime
		},
	})

	issueResult, err := service.Create(TokenCreateRequest{
		ConnectorID: "agent-demo",
	})
	if err != nil {
		testingObject.Fatalf("issue token failed: %v", err)
	}
	issuedToken := issueResult.PlaintextToken
	issuedRecord := issueResult.Record
	if !strings.HasPrefix(issuedToken, "dbt_") {
		testingObject.Fatalf("expected issued token prefix dbt_, got=%s", issuedToken)
	}
	tokenID, tokenSecret, parsed := parseConnectorToken(issuedToken)
	if !parsed {
		testingObject.Fatalf("expected issued token to be parseable: %s", issuedToken)
	}
	if tokenID != issuedRecord.TokenID {
		testingObject.Fatalf("unexpected token id: got=%s want=%s", tokenID, issuedRecord.TokenID)
	}
	if strings.TrimSpace(issuedRecord.TokenSecretHash) == "" {
		testingObject.Fatalf("expected issued record to contain token hash")
	}
	if issuedRecord.TokenSecretHash == tokenSecret {
		testingObject.Fatalf("expected issued record to store hash instead of raw secret")
	}

	recordFromStore, found, err := store.LookupByTokenID(tokenID)
	if err != nil {
		testingObject.Fatalf("lookup issued token failed: %v", err)
	}
	if !found {
		testingObject.Fatalf("expected issued token record in store")
	}
	if recordFromStore.ConnectorID != "agent-demo" {
		testingObject.Fatalf("unexpected connector_id in store: got=%s want=agent-demo", recordFromStore.ConnectorID)
	}

	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		tokenStore:      store,
		now: func() time.Time {
			return currentTime
		},
	})
	authResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "agent-demo",
			assignedSessionEpoch: 1,
			authMethod:           "token",
			token:                issuedToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if !authResult.success {
		testingObject.Fatalf("expected issued token auth success, got code=%s", authResult.errorCode)
	}

	currentTime = currentTime.Add(5 * time.Minute)
	rotateResult, err := service.Rotate(TokenRotateRequest{TokenID: tokenID})
	if err != nil {
		testingObject.Fatalf("rotate token failed: %v", err)
	}
	rotatedToken := rotateResult.PlaintextToken
	rotatedRecord := rotateResult.Record
	if rotatedRecord.TokenID == tokenID {
		testingObject.Fatalf("expected rotate to issue replacement token id: got=%s want!=%s", rotatedRecord.TokenID, tokenID)
	}
	if !rotatedRecord.RotatedAt.IsZero() {
		testingObject.Fatalf("expected replacement token rotated_at to stay zero")
	}

	oldTokenResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "agent-demo",
			assignedSessionEpoch: 2,
			authMethod:           "token",
			token:                issuedToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if oldTokenResult.success {
		testingObject.Fatalf("expected old token to fail after rotate")
	}
	if oldTokenResult.errorCode != connectorAuthErrorTokenRevoked {
		testingObject.Fatalf("unexpected old token error code: got=%s want=%s", oldTokenResult.errorCode, connectorAuthErrorTokenRevoked)
	}

	newTokenResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "agent-demo",
			assignedSessionEpoch: 3,
			authMethod:           "token",
			token:                rotatedToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if !newTokenResult.success {
		testingObject.Fatalf("expected rotated token auth success, got code=%s", newTokenResult.errorCode)
	}

	currentTime = currentTime.Add(5 * time.Minute)
	if _, err := service.Revoke(rotatedRecord.TokenID); err != nil {
		testingObject.Fatalf("revoke token failed: %v", err)
	}

	revokedRecord, found, err := store.LookupByTokenID(rotatedRecord.TokenID)
	if err != nil {
		testingObject.Fatalf("lookup revoked token failed: %v", err)
	}
	if !found {
		testingObject.Fatalf("expected revoked token record to remain in store")
	}
	if revokedRecord.Status != connectorTokenStatusRevoked {
		testingObject.Fatalf("unexpected revoked token status: got=%s want=%s", revokedRecord.Status, connectorTokenStatusRevoked)
	}

	revokedTokenResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "agent-demo",
			assignedSessionEpoch: 4,
			authMethod:           "token",
			token:                rotatedToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if revokedTokenResult.success {
		testingObject.Fatalf("expected revoked token auth to fail")
	}
	if revokedTokenResult.errorCode != connectorAuthErrorTokenRevoked {
		testingObject.Fatalf(
			"unexpected revoked token error code: got=%s want=%s",
			revokedTokenResult.errorCode,
			connectorAuthErrorTokenRevoked,
		)
	}
}

func TestConnectorTokenAdminServiceWithFileStorePersistsAcrossReload(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	storeFilePath := filepath.Join(tempDir, "bridge.tokens.yaml")
	currentTime := time.Date(2026, 3, 29, 20, 0, 0, 0, time.UTC)

	store, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("create file token store failed: %v", err)
	}
	service := newTokenAdminService(tokenAdminServiceOptions{
		store: store,
		now: func() time.Time {
			return currentTime
		},
	})

	issueResult, err := service.Create(TokenCreateRequest{ConnectorID: "agent-file"})
	if err != nil {
		testingObject.Fatalf("create token on file store failed: %v", err)
	}

	reloadedStore, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("reload file token store failed: %v", err)
	}
	sessionRegistry := registry.NewSessionRegistry()
	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		tokenStore:      reloadedStore,
		now: func() time.Time {
			return currentTime
		},
	})
	authResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "agent-file",
			assignedSessionEpoch: 1,
			authMethod:           "token",
			token:                issueResult.PlaintextToken,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if !authResult.success {
		testingObject.Fatalf("expected file-backed token auth success after reload, got code=%s", authResult.errorCode)
	}
}

func TestConnectorTokenAdminServiceRotateRejectsRevokedToken(testingObject *testing.T) {
	testingObject.Parallel()

	store := newInMemoryConnectorTokenStore([]connectorTokenRecord{
		{
			TokenID:         "agent-revoked",
			ConnectorID:     "agent-revoked",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-revoked"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusRevoked,
		},
	})
	service := newTokenAdminService(tokenAdminServiceOptions{
		store: store,
		now: func() time.Time {
			return time.Date(2026, 3, 29, 20, 30, 0, 0, time.UTC)
		},
	})

	if _, err := service.Rotate(TokenRotateRequest{TokenID: "agent-revoked"}); err == nil {
		testingObject.Fatalf("expected rotate on revoked token to fail")
	}
}

func TestConnectorTokenAdminServiceCreateRejectsNilStore(testingObject *testing.T) {
	testingObject.Parallel()

	service := newTokenAdminService(tokenAdminServiceOptions{})
	if _, err := service.Create(TokenCreateRequest{ConnectorID: "agent-nil"}); err == nil {
		testingObject.Fatalf("expected create to fail when token admin store is nil")
	}
}
