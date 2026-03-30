package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	connectorTokenIDRandomBytes      = 9
	connectorTokenSecretRandomBytes  = 24
	connectorTokenGenerateMaxRetries = 16
)

var (
	// ErrTokenAdminInvalidArgument 表示 token 管理请求参数不合法。
	ErrTokenAdminInvalidArgument = errors.New("token admin invalid argument")
	// ErrTokenAdminNotFound 表示指定 token 记录不存在。
	ErrTokenAdminNotFound = errors.New("token admin not found")
	// ErrTokenAdminStoreUnavailable 表示 token 管理存储未就绪。
	ErrTokenAdminStoreUnavailable = errors.New("token admin store unavailable")
)

// TokenCreateRequest 定义 token 创建参数。
type TokenCreateRequest struct {
	ConnectorID string
	ExpiresAt   time.Time
	Metadata    map[string]string
}

// TokenRotateRequest 定义 token 轮换参数。
type TokenRotateRequest struct {
	TokenID string
}

// TokenIssueResult 表示一次 create / rotate 的返回值。
type TokenIssueResult struct {
	Record         connectorTokenRecord
	PlaintextToken string
}

type tokenAdminServiceOptions struct {
	store               connectorManagedTokenStore
	now                 func() time.Time
	generateTokenID     func() (string, error)
	generateTokenSecret func() (string, error)
}

type tokenAdminService struct {
	mu                  sync.Mutex
	store               connectorManagedTokenStore
	now                 func() time.Time
	generateTokenID     func() (string, error)
	generateTokenSecret func() (string, error)
}

func newTokenAdminService(options tokenAdminServiceOptions) *tokenAdminService {
	store := options.store
	nowFunc := options.now
	if nowFunc == nil {
		nowFunc = func() time.Time {
			return time.Now().UTC()
		}
	}
	tokenIDGenerator := options.generateTokenID
	if tokenIDGenerator == nil {
		tokenIDGenerator = defaultConnectorTokenIDGenerator
	}
	tokenSecretGenerator := options.generateTokenSecret
	if tokenSecretGenerator == nil {
		tokenSecretGenerator = defaultConnectorTokenSecretGenerator
	}
	return &tokenAdminService{
		store:               store,
		now:                 nowFunc,
		generateTokenID:     tokenIDGenerator,
		generateTokenSecret: tokenSecretGenerator,
	}
}

func (service *tokenAdminService) requireStore(operation string) error {
	if service == nil {
		return fmt.Errorf("%s: %w", operation, ErrTokenAdminStoreUnavailable)
	}
	if service.store == nil {
		return fmt.Errorf("%s: %w", operation, ErrTokenAdminStoreUnavailable)
	}
	return nil
}

func (service *tokenAdminService) List() ([]connectorTokenRecord, error) {
	if err := service.requireStore("list token"); err != nil {
		return nil, err
	}
	return service.store.List()
}

func (service *tokenAdminService) Get(tokenID string) (connectorTokenRecord, bool, error) {
	if err := service.requireStore("get token"); err != nil {
		return connectorTokenRecord{}, false, err
	}
	return service.store.Get(tokenID)
}

func (service *tokenAdminService) Create(request TokenCreateRequest) (TokenIssueResult, error) {
	if err := service.requireStore("create token"); err != nil {
		return TokenIssueResult{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	normalizedConnectorID := strings.TrimSpace(request.ConnectorID)
	if normalizedConnectorID == "" {
		return TokenIssueResult{}, fmt.Errorf("create token: %w: connector_id is required", ErrTokenAdminInvalidArgument)
	}
	now := service.now().UTC()
	if !request.ExpiresAt.IsZero() && !request.ExpiresAt.After(now) {
		return TokenIssueResult{}, fmt.Errorf("create token: %w: expires_at must be after now", ErrTokenAdminInvalidArgument)
	}

	tokenID, tokenSecret, err := service.generateUniqueTokenMaterial()
	if err != nil {
		return TokenIssueResult{}, err
	}
	tokenSecretHash, err := hashConnectorTokenSecretArgon2ID(tokenSecret)
	if err != nil {
		return TokenIssueResult{}, fmt.Errorf("create token: hash token secret: %w", err)
	}

	record := connectorTokenRecord{
		TokenID:         tokenID,
		ConnectorID:     normalizedConnectorID,
		TokenSecretHash: tokenSecretHash,
		HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
		HashVersion:     connectorTokenHashVersionV1,
		Status:          connectorTokenStatusActive,
		IssuedAt:        now,
		ExpiresAt:       request.ExpiresAt.UTC(),
		Metadata:        cloneConnectorTokenMetadata(request.Metadata),
	}
	if err := service.store.Upsert(record); err != nil {
		return TokenIssueResult{}, fmt.Errorf("create token: persist token record: %w", err)
	}
	return TokenIssueResult{
		Record:         cloneConnectorTokenRecord(record),
		PlaintextToken: buildConnectorPlaintextToken(tokenID, tokenSecret),
	}, nil
}

func (service *tokenAdminService) Rotate(request TokenRotateRequest) (TokenIssueResult, error) {
	if err := service.requireStore("rotate token"); err != nil {
		return TokenIssueResult{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	normalizedTokenID := strings.TrimSpace(request.TokenID)
	if normalizedTokenID == "" {
		return TokenIssueResult{}, fmt.Errorf("rotate token: %w: token_id is required", ErrTokenAdminInvalidArgument)
	}
	existingRecord, found, err := service.store.Get(normalizedTokenID)
	if err != nil {
		return TokenIssueResult{}, fmt.Errorf("rotate token: lookup existing token: %w", err)
	}
	if !found {
		return TokenIssueResult{}, fmt.Errorf("rotate token: %w: token_id=%s", ErrTokenAdminNotFound, normalizedTokenID)
	}

	now := service.now().UTC()
	if existingRecord.Status == connectorTokenStatusRevoked {
		return TokenIssueResult{}, fmt.Errorf(
			"rotate token: %w: token_id=%s is revoked",
			ErrTokenAdminInvalidArgument,
			normalizedTokenID,
		)
	}
	if _, _, ok := validateConnectorTokenState(existingRecord, now); !ok {
		return TokenIssueResult{}, fmt.Errorf(
			"rotate token: %w: token_id=%s is not rotatable",
			ErrTokenAdminInvalidArgument,
			normalizedTokenID,
		)
	}

	nextRecords, err := service.store.List()
	if err != nil {
		return TokenIssueResult{}, fmt.Errorf("rotate token: list token records: %w", err)
	}
	tokenID, tokenSecret, err := service.generateUniqueTokenMaterial()
	if err != nil {
		return TokenIssueResult{}, err
	}
	tokenSecretHash, err := hashConnectorTokenSecretArgon2ID(tokenSecret)
	if err != nil {
		return TokenIssueResult{}, fmt.Errorf("rotate token: hash token secret: %w", err)
	}

	replacedExisting := false
	for index, record := range nextRecords {
		if record.TokenID != normalizedTokenID {
			continue
		}
		record.Status = connectorTokenStatusRevoked
		record.RotatedAt = now
		nextRecords[index] = record
		replacedExisting = true
		break
	}
	if !replacedExisting {
		return TokenIssueResult{}, fmt.Errorf(
			"rotate token: %w: token_id=%s disappeared during rotation",
			ErrTokenAdminNotFound,
			normalizedTokenID,
		)
	}

	newRecord := connectorTokenRecord{
		TokenID:         tokenID,
		ConnectorID:     existingRecord.ConnectorID,
		TokenSecretHash: tokenSecretHash,
		HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
		HashVersion:     connectorTokenHashVersionV1,
		Status:          connectorTokenStatusActive,
		IssuedAt:        now,
		ExpiresAt:       existingRecord.ExpiresAt,
		Metadata:        cloneConnectorTokenMetadata(existingRecord.Metadata),
	}
	nextRecords = append(nextRecords, newRecord)
	if err := service.store.ReplaceAll(nextRecords); err != nil {
		return TokenIssueResult{}, fmt.Errorf("rotate token: persist rotated token set: %w", err)
	}
	return TokenIssueResult{
		Record:         cloneConnectorTokenRecord(newRecord),
		PlaintextToken: buildConnectorPlaintextToken(tokenID, tokenSecret),
	}, nil
}

func (service *tokenAdminService) Revoke(tokenID string) (connectorTokenRecord, error) {
	if err := service.requireStore("revoke token"); err != nil {
		return connectorTokenRecord{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	normalizedTokenID := strings.TrimSpace(tokenID)
	if normalizedTokenID == "" {
		return connectorTokenRecord{}, fmt.Errorf("revoke token: %w: token_id is required", ErrTokenAdminInvalidArgument)
	}
	record, found, err := service.store.Get(normalizedTokenID)
	if err != nil {
		return connectorTokenRecord{}, fmt.Errorf("revoke token: lookup token: %w", err)
	}
	if !found {
		return connectorTokenRecord{}, fmt.Errorf("revoke token: %w: token_id=%s", ErrTokenAdminNotFound, normalizedTokenID)
	}
	record.Status = connectorTokenStatusRevoked
	if err := service.store.Upsert(record); err != nil {
		return connectorTokenRecord{}, fmt.Errorf("revoke token: persist token record: %w", err)
	}
	return cloneConnectorTokenRecord(record), nil
}

func (service *tokenAdminService) Reload() error {
	if err := service.requireStore("reload token"); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.store.Reload()
}

func (service *tokenAdminService) generateUniqueTokenMaterial() (string, string, error) {
	for attempt := 0; attempt < connectorTokenGenerateMaxRetries; attempt++ {
		tokenID, err := service.generateTokenID()
		if err != nil {
			return "", "", fmt.Errorf("generate token material: token_id: %w", err)
		}
		normalizedTokenID := strings.TrimSpace(tokenID)
		if !isValidConnectorTokenID(normalizedTokenID) {
			continue
		}
		if _, found, err := service.store.Get(normalizedTokenID); err != nil {
			return "", "", fmt.Errorf("generate token material: check token collision: %w", err)
		} else if found {
			continue
		}
		tokenSecret, err := service.generateTokenSecret()
		if err != nil {
			return "", "", fmt.Errorf("generate token material: token_secret: %w", err)
		}
		normalizedTokenSecret := strings.TrimSpace(tokenSecret)
		if normalizedTokenSecret == "" || strings.Contains(normalizedTokenSecret, ".") {
			continue
		}
		return normalizedTokenID, normalizedTokenSecret, nil
	}
	return "", "", fmt.Errorf("generate token material: exhausted retries")
}

func buildConnectorPlaintextToken(tokenID string, tokenSecret string) string {
	return fmt.Sprintf("dbt_%s.%s", tokenID, tokenSecret)
}

func defaultConnectorTokenIDGenerator() (string, error) {
	return generateConnectorTokenRandomText(connectorTokenIDRandomBytes)
}

func defaultConnectorTokenSecretGenerator() (string, error) {
	return generateConnectorTokenRandomText(connectorTokenSecretRandomBytes)
}

func generateConnectorTokenRandomText(byteLength int) (string, error) {
	randomBytes := make([]byte, byteLength)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate connector token text: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
