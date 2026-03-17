package app

import (
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

// TestConnectorAuthCoordinatorConcurrentCommitSuperseded 验证同 connector 并发提交仅一个成功。
func TestConnectorAuthCoordinatorConcurrentCommitSuperseded(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	metrics := obs.NewMetrics()
	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		metrics:         metrics,
		tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
			{
				TokenID:         "connector-a",
				ConnectorID:     "connector-a",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-a"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
		}),
	})
	request := connectorAuthRequest{
		connectorID:          "connector-a",
		assignedSessionEpoch: 1,
		authMethod:           "token",
		token:                "dbt_connector-a.secret-a",
	}

	startChannel := make(chan struct{})
	results := make([]connectorAuthResult, 2)
	var waitGroup sync.WaitGroup
	for index := 0; index < 2; index++ {
		waitGroup.Add(1)
		go func(resultIndex int) {
			defer waitGroup.Done()
			<-startChannel
			results[resultIndex] = coordinator.AuthenticateAndCommit(
				request,
				func(now time.Time, sessionRuntime registry.SessionRuntime) error {
					// 测试里沿用权威提交路径，确保并发条件与生产代码一致。
					sessionRegistry.CommitAuthoritative(now, sessionRuntime)
					return nil
				},
			)
		}(index)
	}
	close(startChannel)
	waitGroup.Wait()

	successCount := 0
	supersededCount := 0
	for _, result := range results {
		if result.success {
			successCount++
			continue
		}
		if result.errorCode == connectorAuthErrorSessionSuperseded {
			supersededCount++
		}
	}
	if successCount != 1 || supersededCount != 1 {
		testingObject.Fatalf("unexpected concurrent auth results: success=%d superseded=%d", successCount, supersededCount)
	}
	if metrics.BridgeAuthSuccessTotal() != 1 {
		testingObject.Fatalf("unexpected auth success metric: got=%d want=1", metrics.BridgeAuthSuccessTotal())
	}
	if metrics.BridgeAuthFailureTotal() != 1 {
		testingObject.Fatalf("unexpected auth failure metric: got=%d want=1", metrics.BridgeAuthFailureTotal())
	}
	if metrics.BridgeAuthErrorCodeTotal(connectorAuthErrorSessionSuperseded) != 1 {
		testingObject.Fatalf(
			"unexpected superseded error metric: got=%d want=1",
			metrics.BridgeAuthErrorCodeTotal(connectorAuthErrorSessionSuperseded),
		)
	}
	if metrics.BridgeAuthSupersedeTotal() != 0 {
		testingObject.Fatalf("unexpected auth supersede metric: got=%d want=0", metrics.BridgeAuthSupersedeTotal())
	}
}

// TestConnectorAuthCoordinatorSupersedeRateLimit 验证成功抢占超过阈值后会被限流拒绝。
func TestConnectorAuthCoordinatorSupersedeRateLimit(testingObject *testing.T) {
	testingObject.Parallel()

	currentTime := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(currentTime, registry.SessionRuntime{
		SessionID:     "session-0",
		ConnectorID:   "connector-rate",
		Epoch:         1,
		State:         registry.SessionActive,
		LastHeartbeat: currentTime,
	})
	metrics := obs.NewMetrics()
	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		metrics:         metrics,
		tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
			{
				TokenID:         "connector-rate",
				ConnectorID:     "connector-rate",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-rate"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
		}),
		now: func() time.Time {
			return currentTime
		},
		supersedeRateWindow: 60 * time.Second,
		supersedeRateLimit:  3,
	})

	for _, epoch := range []uint64{2, 3, 4} {
		result := coordinator.AuthenticateAndCommit(
			connectorAuthRequest{
				connectorID:          "connector-rate",
				assignedSessionEpoch: epoch,
				authMethod:           "token",
				token:                "dbt_connector-rate.secret-rate",
			},
			func(now time.Time, sessionRuntime registry.SessionRuntime) error {
				// 每次成功提交都通过统一权威提交入口更新 connector 当前会话。
				sessionRegistry.CommitAuthoritative(now, sessionRuntime)
				return nil
			},
		)
		if !result.success {
			testingObject.Fatalf("expected supersede success for epoch=%d, got code=%s", epoch, result.errorCode)
		}
		currentTime = currentTime.Add(10 * time.Second)
	}

	limitedResult := coordinator.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          "connector-rate",
			assignedSessionEpoch: 5,
			authMethod:           "token",
			token:                "dbt_connector-rate.secret-rate",
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			sessionRegistry.CommitAuthoritative(now, sessionRuntime)
			return nil
		},
	)
	if limitedResult.success {
		testingObject.Fatalf("expected supersede rate limit reject")
	}
	if limitedResult.errorCode != connectorAuthErrorRateLimited {
		testingObject.Fatalf("unexpected rate-limit error code: got=%s want=%s", limitedResult.errorCode, connectorAuthErrorRateLimited)
	}
	if metrics.BridgeAuthSuccessTotal() != 3 {
		testingObject.Fatalf("unexpected auth success metric: got=%d want=3", metrics.BridgeAuthSuccessTotal())
	}
	if metrics.BridgeAuthSupersedeTotal() != 3 {
		testingObject.Fatalf("unexpected auth supersede metric: got=%d want=3", metrics.BridgeAuthSupersedeTotal())
	}
	if metrics.BridgeAuthFailureTotal() != 1 {
		testingObject.Fatalf("unexpected auth failure metric: got=%d want=1", metrics.BridgeAuthFailureTotal())
	}
	if metrics.BridgeAuthRateLimitTotal() != 1 {
		testingObject.Fatalf("unexpected auth rate-limit metric: got=%d want=1", metrics.BridgeAuthRateLimitTotal())
	}
	if metrics.BridgeAuthErrorCodeTotal(connectorAuthErrorRateLimited) != 1 {
		testingObject.Fatalf(
			"unexpected rate-limit error metric: got=%d want=1",
			metrics.BridgeAuthErrorCodeTotal(connectorAuthErrorRateLimited),
		)
	}
}

// TestConnectorAuthCoordinatorErrorCodeMapping 验证关键失败场景返回标准错误码。
func TestConnectorAuthCoordinatorErrorCodeMapping(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Date(2026, 3, 17, 11, 0, 0, 0, time.UTC)
	testCases := []struct {
		name       string
		record     connectorTokenRecord
		request    connectorAuthRequest
		wantCode   string
		wantCommit bool
	}{
		{
			name: "invalid method",
			record: connectorTokenRecord{
				TokenID:         "connector-code",
				ConnectorID:     "connector-code",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-code"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
			request: connectorAuthRequest{
				connectorID:          "connector-code",
				assignedSessionEpoch: 1,
				authMethod:           "hmac",
				token:                "dbt_connector-code.secret-code",
			},
			wantCode: connectorAuthErrorInvalidMethod,
		},
		{
			name: "unknown token",
			record: connectorTokenRecord{
				TokenID:         "connector-code",
				ConnectorID:     "connector-code",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-code"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
			request: connectorAuthRequest{
				connectorID:          "connector-code",
				assignedSessionEpoch: 1,
				authMethod:           "token",
				token:                "dbt_missing.secret-code",
			},
			wantCode: connectorAuthErrorInvalidToken,
		},
		{
			name: "connector mismatch",
			record: connectorTokenRecord{
				TokenID:         "connector-code",
				ConnectorID:     "connector-other",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-code"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
			request: connectorAuthRequest{
				connectorID:          "connector-code",
				assignedSessionEpoch: 1,
				authMethod:           "token",
				token:                "dbt_connector-code.secret-code",
			},
			wantCode: connectorAuthErrorConnectorMismatch,
		},
		{
			name: "token revoked",
			record: connectorTokenRecord{
				TokenID:         "connector-code",
				ConnectorID:     "connector-code",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-code"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusRevoked,
			},
			request: connectorAuthRequest{
				connectorID:          "connector-code",
				assignedSessionEpoch: 1,
				authMethod:           "token",
				token:                "dbt_connector-code.secret-code",
			},
			wantCode: connectorAuthErrorTokenRevoked,
		},
		{
			name: "token expired by timestamp",
			record: connectorTokenRecord{
				TokenID:         "connector-code",
				ConnectorID:     "connector-code",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-code"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
				ExpiresAt:       now.Add(-time.Second),
			},
			request: connectorAuthRequest{
				connectorID:          "connector-code",
				assignedSessionEpoch: 1,
				authMethod:           "token",
				token:                "dbt_connector-code.secret-code",
			},
			wantCode: connectorAuthErrorTokenExpired,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()

			commitCalled := false
			metrics := obs.NewMetrics()
			coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
				sessionRegistry: registry.NewSessionRegistry(),
				metrics:         metrics,
				tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
					testCase.record,
				}),
				now: func() time.Time {
					return now
				},
			})
			result := coordinator.AuthenticateAndCommit(
				testCase.request,
				func(now time.Time, sessionRuntime registry.SessionRuntime) error {
					_ = now
					_ = sessionRuntime
					commitCalled = true
					return nil
				},
			)
			if result.success {
				testingObject.Fatalf("expected auth failure, got success")
			}
			if result.errorCode != testCase.wantCode {
				testingObject.Fatalf("unexpected auth error code: got=%s want=%s", result.errorCode, testCase.wantCode)
			}
			if commitCalled != testCase.wantCommit {
				testingObject.Fatalf("unexpected commit callback state: got=%t want=%t", commitCalled, testCase.wantCommit)
			}
			if metrics.BridgeAuthFailureTotal() != 1 {
				testingObject.Fatalf("unexpected auth failure metric: got=%d want=1", metrics.BridgeAuthFailureTotal())
			}
			if metrics.BridgeAuthErrorCodeTotal(testCase.wantCode) != 1 {
				testingObject.Fatalf(
					"unexpected error code metric: got=%d want=1 code=%s",
					metrics.BridgeAuthErrorCodeTotal(testCase.wantCode),
					testCase.wantCode,
				)
			}
		})
	}
}

// TestVerifyConnectorTokenSecretArgon2ID 验证 token secret 会按 argon2id 哈希校验。
func TestVerifyConnectorTokenSecretArgon2ID(testingObject *testing.T) {
	testingObject.Parallel()

	encodedHash := mustHashConnectorTokenSecretArgon2ID("secret-hash")
	matched, err := verifyConnectorTokenSecret("secret-hash", encodedHash)
	if err != nil {
		testingObject.Fatalf("verify connector token secret failed: %v", err)
	}
	if !matched {
		testingObject.Fatalf("expected token secret hash matched")
	}

	mismatched, err := verifyConnectorTokenSecret("secret-other", encodedHash)
	if err != nil {
		testingObject.Fatalf("verify mismatched connector token secret failed: %v", err)
	}
	if mismatched {
		testingObject.Fatalf("expected token secret hash mismatch")
	}
}

// TestParseConnectorTokenUsesFirstDot 验证 token 按第一个点切分 token_id 与 token_secret。
func TestParseConnectorTokenUsesFirstDot(testingObject *testing.T) {
	testingObject.Parallel()

	tokenID, tokenSecret, ok := parseConnectorToken("dbt_connector-id.secret.with.dot")
	if !ok {
		testingObject.Fatalf("expected connector token parsed")
	}
	if tokenID != "connector-id" {
		testingObject.Fatalf("unexpected token id: got=%s want=%s", tokenID, "connector-id")
	}
	if tokenSecret != "secret.with.dot" {
		testingObject.Fatalf("unexpected token secret: got=%s want=%s", tokenSecret, "secret.with.dot")
	}
}
