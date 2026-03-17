package app

import (
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

// TestConnectorAuthCoordinatorConcurrentCommitSuperseded 验证同 connector 并发提交仅一个成功。
func TestConnectorAuthCoordinatorConcurrentCommitSuperseded(testingObject *testing.T) {
	testingObject.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
			{
				TokenID:     "connector-a",
				ConnectorID: "connector-a",
				TokenSecret: "secret-a",
				Status:      connectorTokenStatusActive,
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
				func(sessionID string, sessionEpoch uint64) error {
					// 认证提交成功后把会话写入权威注册表。
					sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
						SessionID:     sessionID,
						ConnectorID:   "connector-a",
						Epoch:         sessionEpoch,
						State:         registry.SessionActive,
						LastHeartbeat: time.Now().UTC(),
					})
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
	coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
		sessionRegistry: sessionRegistry,
		tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
			{
				TokenID:     "connector-rate",
				ConnectorID: "connector-rate",
				TokenSecret: "secret-rate",
				Status:      connectorTokenStatusActive,
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
			func(sessionID string, sessionEpoch uint64) error {
				// 每次成功提交都更新当前 connector 的权威 session。
				sessionRegistry.Upsert(currentTime, registry.SessionRuntime{
					SessionID:     sessionID,
					ConnectorID:   "connector-rate",
					Epoch:         sessionEpoch,
					State:         registry.SessionActive,
					LastHeartbeat: currentTime,
				})
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
		func(sessionID string, sessionEpoch uint64) error {
			sessionRegistry.Upsert(currentTime, registry.SessionRuntime{
				SessionID:     sessionID,
				ConnectorID:   "connector-rate",
				Epoch:         sessionEpoch,
				State:         registry.SessionActive,
				LastHeartbeat: currentTime,
			})
			return nil
		},
	)
	if limitedResult.success {
		testingObject.Fatalf("expected supersede rate limit reject")
	}
	if limitedResult.errorCode != connectorAuthErrorRateLimited {
		testingObject.Fatalf("unexpected rate-limit error code: got=%s want=%s", limitedResult.errorCode, connectorAuthErrorRateLimited)
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
				TokenID:     "connector-code",
				ConnectorID: "connector-code",
				TokenSecret: "secret-code",
				Status:      connectorTokenStatusActive,
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
				TokenID:     "connector-code",
				ConnectorID: "connector-code",
				TokenSecret: "secret-code",
				Status:      connectorTokenStatusActive,
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
				TokenID:     "connector-code",
				ConnectorID: "connector-other",
				TokenSecret: "secret-code",
				Status:      connectorTokenStatusActive,
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
				TokenID:     "connector-code",
				ConnectorID: "connector-code",
				TokenSecret: "secret-code",
				Status:      connectorTokenStatusRevoked,
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
				TokenID:     "connector-code",
				ConnectorID: "connector-code",
				TokenSecret: "secret-code",
				Status:      connectorTokenStatusActive,
				ExpiresAt:   now.Add(-time.Second),
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
			coordinator := newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
				sessionRegistry: registry.NewSessionRegistry(),
				tokenStore: newInMemoryConnectorTokenStore([]connectorTokenRecord{
					testCase.record,
				}),
				now: func() time.Time {
					return now
				},
			})
			result := coordinator.AuthenticateAndCommit(
				testCase.request,
				func(sessionID string, sessionEpoch uint64) error {
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
		})
	}
}
