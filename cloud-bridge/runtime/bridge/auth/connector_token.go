package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	// connectorTokenHashAlgorithmArgon2ID 标识当前默认 token 密码哈希算法。
	connectorTokenHashAlgorithmArgon2ID = "argon2id"
	// connectorTokenHashVersionV1 标识当前 token 哈希参数版本。
	connectorTokenHashVersionV1 = "v1"

	// connectorTokenArgon2IDMemoryKB 定义 argon2id 的 memory cost。
	connectorTokenArgon2IDMemoryKB uint32 = 64 * 1024
	// connectorTokenArgon2IDIterations 定义 argon2id 的 time cost。
	connectorTokenArgon2IDIterations uint32 = 3
	// connectorTokenArgon2IDParallelism 定义 argon2id 的并行度。
	connectorTokenArgon2IDParallelism uint8 = 2
	// connectorTokenArgon2IDKeyLength 定义导出 hash 长度。
	connectorTokenArgon2IDKeyLength uint32 = 32
	// connectorTokenArgon2IDSaltLength 定义随机 salt 长度。
	connectorTokenArgon2IDSaltLength = 16
)

var (
	defaultConnectorTokenRecordsOnce sync.Once
	defaultConnectorTokenRecordsData []connectorTokenRecord
)

// connectorTokenStatus 定义 token 在认证阶段的状态。
type connectorTokenStatus string

const (
	connectorTokenStatusActive  connectorTokenStatus = "active"
	connectorTokenStatusGrace   connectorTokenStatus = "grace"
	connectorTokenStatusRevoked connectorTokenStatus = "revoked"
	connectorTokenStatusExpired connectorTokenStatus = "expired"
)

// connectorTokenRecord 保存 Connector token 领域模型的核心字段。
type connectorTokenRecord struct {
	TokenID         string
	ConnectorID     string
	TokenSecretHash string
	HashAlgorithm   string
	HashVersion     string
	Status          connectorTokenStatus
	IssuedAt        time.Time
	ExpiresAt       time.Time
	RotatedAt       time.Time
	Metadata        map[string]string
}

// connectorTokenStore 定义 token 索引查询接口。
type connectorTokenStore interface {
	LookupByTokenID(tokenID string) (connectorTokenRecord, bool, error)
}

// connectorManagedTokenStore 定义 token 管理读写能力。
type connectorManagedTokenStore interface {
	connectorTokenStore
	List() ([]connectorTokenRecord, error)
	Get(tokenID string) (connectorTokenRecord, bool, error)
	Upsert(record connectorTokenRecord) error
	Delete(tokenID string) error
	ReplaceAll(records []connectorTokenRecord) error
	Save() error
	Reload() error
}

// inMemoryConnectorTokenStore 提供开发期可用的内存 token 索引实现。
type inMemoryConnectorTokenStore struct {
	mu        sync.RWMutex
	byTokenID map[string]connectorTokenRecord
}

// newInMemoryConnectorTokenStore 根据 token 记录构建内存索引。
func newInMemoryConnectorTokenStore(records []connectorTokenRecord) *inMemoryConnectorTokenStore {
	normalizedRecords, err := normalizeConnectorTokenRecordSet(records)
	if err != nil {
		panic(fmt.Errorf("new in-memory connector token store: %w", err))
	}
	store := &inMemoryConnectorTokenStore{
		byTokenID: connectorTokenRecordSliceToMap(normalizedRecords),
	}
	return store
}

// LookupByTokenID 通过 token_id 查询 token 记录。
func (store *inMemoryConnectorTokenStore) LookupByTokenID(tokenID string) (connectorTokenRecord, bool, error) {
	if store == nil {
		return connectorTokenRecord{}, false, fmt.Errorf("connector token store is nil")
	}
	normalizedTokenID := strings.TrimSpace(tokenID)
	if normalizedTokenID == "" {
		return connectorTokenRecord{}, false, nil
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, exists := store.byTokenID[normalizedTokenID]
	if !exists {
		return connectorTokenRecord{}, false, nil
	}
	return cloneConnectorTokenRecord(record), true, nil
}

// List 返回当前内存 token 记录快照。
func (store *inMemoryConnectorTokenStore) List() ([]connectorTokenRecord, error) {
	if store == nil {
		return nil, fmt.Errorf("connector token store is nil")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return sortedConnectorTokenRecordsFromMap(store.byTokenID), nil
}

// Get 通过 token_id 获取 token 记录。
func (store *inMemoryConnectorTokenStore) Get(tokenID string) (connectorTokenRecord, bool, error) {
	return store.LookupByTokenID(tokenID)
}

// Upsert 写入或覆盖一条 token 记录。
func (store *inMemoryConnectorTokenStore) Upsert(record connectorTokenRecord) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	normalizedRecord, ok := normalizeConnectorTokenRecord(record)
	if !ok {
		return fmt.Errorf("upsert connector token record: invalid token record")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byTokenID[normalizedRecord.TokenID] = normalizedRecord
	return nil
}

// Delete 删除一条 token 记录。
func (store *inMemoryConnectorTokenStore) Delete(tokenID string) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	normalizedTokenID := strings.TrimSpace(tokenID)
	if normalizedTokenID == "" {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.byTokenID, normalizedTokenID)
	return nil
}

// ReplaceAll 使用给定快照替换全部 token 记录。
func (store *inMemoryConnectorTokenStore) ReplaceAll(records []connectorTokenRecord) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	replacedRecords, err := normalizeConnectorTokenRecordSet(records)
	if err != nil {
		return fmt.Errorf("replace all connector token records: %w", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byTokenID = connectorTokenRecordSliceToMap(replacedRecords)
	return nil
}

// Save 对纯内存 store 为 no-op。
func (store *inMemoryConnectorTokenStore) Save() error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	return nil
}

// Reload 对纯内存 store 为 no-op。
func (store *inMemoryConnectorTokenStore) Reload() error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	return nil
}

// normalizeConnectorTokenRecord 归一化 token 记录，避免脏数据进入索引。
func normalizeConnectorTokenRecord(record connectorTokenRecord) (connectorTokenRecord, bool) {
	normalizedTokenID := strings.TrimSpace(record.TokenID)
	if normalizedTokenID == "" || !isValidConnectorTokenID(normalizedTokenID) {
		return connectorTokenRecord{}, false
	}
	normalizedHash := strings.TrimSpace(record.TokenSecretHash)
	if normalizedHash == "" {
		return connectorTokenRecord{}, false
	}
	normalizedRecord := connectorTokenRecord{
		TokenID:         normalizedTokenID,
		ConnectorID:     strings.TrimSpace(record.ConnectorID),
		TokenSecretHash: normalizedHash,
		HashAlgorithm:   strings.TrimSpace(record.HashAlgorithm),
		HashVersion:     strings.TrimSpace(record.HashVersion),
		Status:          normalizeConnectorTokenStatus(record.Status),
		IssuedAt:        record.IssuedAt.UTC(),
		ExpiresAt:       record.ExpiresAt.UTC(),
		RotatedAt:       record.RotatedAt.UTC(),
	}
	if len(record.Metadata) != 0 {
		normalizedRecord.Metadata = make(map[string]string, len(record.Metadata))
		for key, value := range record.Metadata {
			normalizedKey := strings.TrimSpace(key)
			if normalizedKey == "" {
				continue
			}
			normalizedRecord.Metadata[normalizedKey] = strings.TrimSpace(value)
		}
	}
	return normalizedRecord, true
}

// normalizeConnectorTokenStatus 归一化 token 状态文本。
func normalizeConnectorTokenStatus(status connectorTokenStatus) connectorTokenStatus {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case string(connectorTokenStatusActive):
		return connectorTokenStatusActive
	case string(connectorTokenStatusGrace):
		return connectorTokenStatusGrace
	case string(connectorTokenStatusRevoked):
		return connectorTokenStatusRevoked
	case string(connectorTokenStatusExpired):
		return connectorTokenStatusExpired
	default:
		return ""
	}
}

func cloneConnectorTokenRecord(record connectorTokenRecord) connectorTokenRecord {
	clonedRecord := record
	clonedRecord.Metadata = cloneConnectorTokenMetadata(record.Metadata)
	return clonedRecord
}

func cloneConnectorTokenMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clonedMetadata := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clonedMetadata[key] = value
	}
	return clonedMetadata
}

func normalizeConnectorTokenRecordSet(records []connectorTokenRecord) ([]connectorTokenRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}
	normalizedRecords := make([]connectorTokenRecord, 0, len(records))
	seenTokenIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		normalizedRecord, ok := normalizeConnectorTokenRecord(record)
		if !ok {
			return nil, fmt.Errorf("invalid connector token record token_id=%q", strings.TrimSpace(record.TokenID))
		}
		if _, exists := seenTokenIDs[normalizedRecord.TokenID]; exists {
			return nil, fmt.Errorf("duplicate connector token record token_id=%q", normalizedRecord.TokenID)
		}
		seenTokenIDs[normalizedRecord.TokenID] = struct{}{}
		normalizedRecords = append(normalizedRecords, normalizedRecord)
	}
	sort.Slice(normalizedRecords, func(leftIndex int, rightIndex int) bool {
		return normalizedRecords[leftIndex].TokenID < normalizedRecords[rightIndex].TokenID
	})
	return normalizedRecords, nil
}

func connectorTokenRecordSliceToMap(records []connectorTokenRecord) map[string]connectorTokenRecord {
	if len(records) == 0 {
		return make(map[string]connectorTokenRecord)
	}
	byTokenID := make(map[string]connectorTokenRecord, len(records))
	for _, record := range records {
		byTokenID[record.TokenID] = cloneConnectorTokenRecord(record)
	}
	return byTokenID
}

func sortedConnectorTokenRecordsFromMap(byTokenID map[string]connectorTokenRecord) []connectorTokenRecord {
	if len(byTokenID) == 0 {
		return nil
	}
	records := make([]connectorTokenRecord, 0, len(byTokenID))
	for _, record := range byTokenID {
		records = append(records, cloneConnectorTokenRecord(record))
	}
	sort.Slice(records, func(leftIndex int, rightIndex int) bool {
		return records[leftIndex].TokenID < records[rightIndex].TokenID
	})
	return records
}

// parseConnectorToken 解析 dbt_<token_id>.<token_secret> 结构。
func parseConnectorToken(rawToken string) (string, string, bool) {
	normalizedToken := strings.TrimSpace(rawToken)
	if !strings.HasPrefix(normalizedToken, "dbt_") {
		return "", "", false
	}
	withoutPrefix := strings.TrimPrefix(normalizedToken, "dbt_")
	tokenID, tokenSecret, found := strings.Cut(withoutPrefix, ".")
	if !found {
		return "", "", false
	}
	tokenID = strings.TrimSpace(tokenID)
	tokenSecret = strings.TrimSpace(tokenSecret)
	if tokenID == "" || tokenSecret == "" {
		return "", "", false
	}
	if !isValidConnectorTokenID(tokenID) {
		return "", "", false
	}
	return tokenID, tokenSecret, true
}

// isValidConnectorTokenID 校验 token_id 是否满足 [A-Za-z0-9_-]+。
func isValidConnectorTokenID(tokenID string) bool {
	if strings.TrimSpace(tokenID) == "" {
		return false
	}
	for _, char := range tokenID {
		isDigit := char >= '0' && char <= '9'
		isLowerAlpha := char >= 'a' && char <= 'z'
		isUpperAlpha := char >= 'A' && char <= 'Z'
		if isDigit || isLowerAlpha || isUpperAlpha || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

// hashConnectorTokenSecretArgon2ID 使用 argon2id 计算 token_secret_hash。
func hashConnectorTokenSecretArgon2ID(tokenSecret string) (string, error) {
	normalizedSecret := strings.TrimSpace(tokenSecret)
	if normalizedSecret == "" {
		return "", fmt.Errorf("hash connector token secret: empty token secret")
	}
	salt := make([]byte, connectorTokenArgon2IDSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash connector token secret: generate salt: %w", err)
	}
	derivedKey := argon2.IDKey(
		[]byte(normalizedSecret),
		salt,
		connectorTokenArgon2IDIterations,
		connectorTokenArgon2IDMemoryKB,
		connectorTokenArgon2IDParallelism,
		connectorTokenArgon2IDKeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		connectorTokenArgon2IDMemoryKB,
		connectorTokenArgon2IDIterations,
		connectorTokenArgon2IDParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derivedKey),
	), nil
}

// verifyConnectorTokenSecret 校验 token secret 与 argon2id hash 是否匹配。
func verifyConnectorTokenSecret(tokenSecret string, encodedHash string) (bool, error) {
	normalizedSecret := strings.TrimSpace(tokenSecret)
	normalizedHash := strings.TrimSpace(encodedHash)
	if normalizedSecret == "" || normalizedHash == "" {
		return false, nil
	}
	hashParameters, salt, expectedHash, err := parseArgon2IDHash(normalizedHash)
	if err != nil {
		return false, err
	}
	actualHash := argon2.IDKey(
		[]byte(normalizedSecret),
		salt,
		hashParameters.iterations,
		hashParameters.memoryKB,
		hashParameters.parallelism,
		uint32(len(expectedHash)),
	)
	return subtle.ConstantTimeCompare(actualHash, expectedHash) == 1, nil
}

// mustHashConnectorTokenSecretArgon2ID 用于构造默认开发 token 记录。
func mustHashConnectorTokenSecretArgon2ID(tokenSecret string) string {
	encodedHash, err := hashConnectorTokenSecretArgon2ID(tokenSecret)
	if err != nil {
		panic(err)
	}
	return encodedHash
}

// argon2IDHashParameters 保存 hash 校验所需的参数快照。
type argon2IDHashParameters struct {
	memoryKB    uint32
	iterations  uint32
	parallelism uint8
}

// parseArgon2IDHash 解析 argon2id 编码串。
func parseArgon2IDHash(encodedHash string) (argon2IDHashParameters, []byte, []byte, error) {
	parts := strings.Split(strings.TrimSpace(encodedHash), "$")
	if len(parts) != 6 {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: invalid part count")
	}
	if parts[1] != "argon2id" {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: unsupported algorithm=%s", parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: decode version: %w", err)
	}
	if version != argon2.Version {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: unsupported version=%d", version)
	}
	parameters := argon2IDHashParameters{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parameters.memoryKB, &parameters.iterations, &parameters.parallelism); err != nil {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: decode parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: decode salt: %w", err)
	}
	derivedKey, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: decode hash: %w", err)
	}
	if len(salt) == 0 || len(derivedKey) == 0 {
		return argon2IDHashParameters{}, nil, nil, fmt.Errorf("parse argon2id hash: empty salt or hash")
	}
	return parameters, salt, derivedKey, nil
}

// defaultConnectorTokenRecords 返回开发环境默认 token 记录。
func defaultConnectorTokenRecords() []connectorTokenRecord {
	defaultConnectorTokenRecordsOnce.Do(func() {
		// 默认开发 token 的 argon2id 哈希只需生成一次，避免高并发测试下重复消耗 CPU/内存。
		defaultConnectorTokenRecordsData = []connectorTokenRecord{
			{
				TokenID:         "agent-local",
				ConnectorID:     "agent-local",
				TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("agent-dev-secret"),
				HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
				HashVersion:     connectorTokenHashVersionV1,
				Status:          connectorTokenStatusActive,
			},
		}
	})
	copiedRecords := make([]connectorTokenRecord, len(defaultConnectorTokenRecordsData))
	copy(copiedRecords, defaultConnectorTokenRecordsData)
	return copiedRecords
}
