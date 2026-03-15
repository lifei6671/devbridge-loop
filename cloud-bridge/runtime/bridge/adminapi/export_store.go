package adminapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// defaultDiagnoseExportLimit 定义导出缓存条目上限。
	defaultDiagnoseExportLimit = 32
	// defaultDiagnoseExportTTL 定义导出下载链接默认有效期（10 分钟）。
	defaultDiagnoseExportTTL = 10 * time.Minute
)

var (
	// errDiagnoseExportNotFound 表示导出条目不存在、已过期或已被消费。
	errDiagnoseExportNotFound = errors.New("diagnose export is missing or expired")
	// errDiagnoseExportActorMismatch 表示下载人和导出发起人不一致。
	errDiagnoseExportActorMismatch = errors.New("diagnose export actor mismatch")
)

// diagnoseExportEntry 表示一条导出记录。
type diagnoseExportEntry struct {
	ExportID     string
	Token        string
	ExpireAt     time.Time
	Payload      []byte
	MaskedFields []string
	CreatedAt    time.Time
	IssuedTo     string
}

// diagnoseExportStore 是导出文件的内存短期缓存。
type diagnoseExportStore struct {
	mutex sync.Mutex
	limit int
	ttl   time.Duration

	sequence uint64
	order    []string
	byID     map[string]diagnoseExportEntry
}

// newDiagnoseExportStore 初始化导出缓存。
func newDiagnoseExportStore(limit int, ttl time.Duration) *diagnoseExportStore {
	normalizedLimit := limit
	if normalizedLimit <= 0 {
		normalizedLimit = defaultDiagnoseExportLimit
	}
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		normalizedTTL = defaultDiagnoseExportTTL
	}
	return &diagnoseExportStore{
		limit: normalizedLimit,
		ttl:   normalizedTTL,
		order: make([]string, 0, normalizedLimit),
		byID:  make(map[string]diagnoseExportEntry, normalizedLimit),
	}
}

// create 写入导出内容并生成下载令牌。
func (store *diagnoseExportStore) create(
	now time.Time,
	payload any,
	maskedFields []string,
	issuedTo string,
) (diagnoseExportEntry, error) {
	if store == nil {
		return diagnoseExportEntry{}, ErrAdminOperationNotSupported
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return diagnoseExportEntry{}, fmt.Errorf("marshal diagnose export payload: %w", err)
	}
	token, err := generateExportToken()
	if err != nil {
		return diagnoseExportEntry{}, fmt.Errorf("generate export token: %w", err)
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.purgeExpiredLocked(normalizedNow)

	store.sequence++
	exportID := fmt.Sprintf("exp-%d-%d", normalizedNow.UnixMilli(), store.sequence)
	normalizedMaskedFields := append([]string(nil), maskedFields...)
	sort.Strings(normalizedMaskedFields)
	entry := diagnoseExportEntry{
		ExportID:     exportID,
		Token:        token,
		CreatedAt:    normalizedNow,
		ExpireAt:     normalizedNow.Add(store.ttl),
		Payload:      rawPayload,
		MaskedFields: normalizedMaskedFields,
		IssuedTo:     strings.TrimSpace(issuedTo),
	}
	store.byID[exportID] = entry
	store.order = append(store.order, exportID)
	store.trimOverflowLocked()
	return entry, nil
}

// get 读取并校验导出条目（id + token + 发起人 + 过期时间），并按一次性链接语义立即消费。
func (store *diagnoseExportStore) get(
	exportID string,
	token string,
	actor string,
	now time.Time,
) (diagnoseExportEntry, error) {
	if store == nil {
		return diagnoseExportEntry{}, errDiagnoseExportNotFound
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.purgeExpiredLocked(normalizedNow)
	entry, exists := store.byID[exportID]
	if !exists {
		return diagnoseExportEntry{}, errDiagnoseExportNotFound
	}
	if entry.Token != token {
		return diagnoseExportEntry{}, errDiagnoseExportNotFound
	}
	normalizedActor := strings.TrimSpace(actor)
	if strings.TrimSpace(entry.IssuedTo) != "" && normalizedActor != strings.TrimSpace(entry.IssuedTo) {
		return diagnoseExportEntry{}, errDiagnoseExportActorMismatch
	}
	// 一次性消费策略：下载成功后立刻删除，避免链接被重放。
	delete(store.byID, exportID)
	store.removeFromOrderLocked(exportID)
	return entry, nil
}

// purgeExpiredLocked 清理过期导出条目（调用方需持有互斥锁）。
func (store *diagnoseExportStore) purgeExpiredLocked(now time.Time) {
	if len(store.byID) == 0 {
		return
	}
	filteredOrder := store.order[:0]
	for _, exportID := range store.order {
		entry, exists := store.byID[exportID]
		if !exists {
			continue
		}
		if !entry.ExpireAt.After(now) {
			delete(store.byID, exportID)
			continue
		}
		filteredOrder = append(filteredOrder, exportID)
	}
	store.order = filteredOrder
}

// trimOverflowLocked 按 FIFO 淘汰超限导出条目（调用方需持有互斥锁）。
func (store *diagnoseExportStore) trimOverflowLocked() {
	for len(store.order) > store.limit {
		oldestExportID := store.order[0]
		store.order = store.order[1:]
		delete(store.byID, oldestExportID)
	}
}

// removeFromOrderLocked 从 FIFO 顺序队列中删除指定导出条目（调用方需持有互斥锁）。
func (store *diagnoseExportStore) removeFromOrderLocked(exportID string) {
	if len(store.order) == 0 {
		return
	}
	filteredOrder := store.order[:0]
	for _, currentExportID := range store.order {
		if currentExportID == exportID {
			continue
		}
		filteredOrder = append(filteredOrder, currentExportID)
	}
	store.order = filteredOrder
}

// generateExportToken 生成随机下载令牌，避免可预测 ID 被枚举下载。
func generateExportToken() (string, error) {
	rawToken := make([]byte, 12)
	if _, err := rand.Read(rawToken); err != nil {
		return "", err
	}
	return hex.EncodeToString(rawToken), nil
}
