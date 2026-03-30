package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/internal/fileutil"
	"gopkg.in/yaml.v3"
)

const connectorTokenStoreFileFormatVersion = 1

var connectorTokenStoreRenameFile = fileutil.ReplaceFile

type persistedConnectorTokenStoreFile struct {
	Version int                             `yaml:"version"`
	Tokens  []persistedConnectorTokenRecord `yaml:"tokens"`
}

type persistedConnectorTokenRecord struct {
	TokenID         string            `yaml:"token_id"`
	ConnectorID     string            `yaml:"connector_id"`
	TokenSecretHash string            `yaml:"token_secret_hash"`
	HashAlgorithm   string            `yaml:"hash_algorithm"`
	HashVersion     string            `yaml:"hash_version"`
	Status          string            `yaml:"status"`
	IssuedAt        time.Time         `yaml:"issued_at,omitempty"`
	ExpiresAt       time.Time         `yaml:"expires_at,omitempty"`
	RotatedAt       time.Time         `yaml:"rotated_at,omitempty"`
	Metadata        map[string]string `yaml:"metadata,omitempty"`
}

type fileConnectorTokenStore struct {
	path string

	mu        sync.RWMutex
	byTokenID map[string]connectorTokenRecord
}

func newFileConnectorTokenStore(path string) (*fileConnectorTokenStore, error) {
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath == "" {
		return nil, fmt.Errorf("new file connector token store: empty file path")
	}
	absolutePath, err := filepath.Abs(normalizedPath)
	if err != nil {
		return nil, fmt.Errorf("new file connector token store: resolve absolute path: %w", err)
	}
	byTokenID, err := loadConnectorTokenRecordMapFromFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("new file connector token store: %w", err)
	}
	return &fileConnectorTokenStore{
		path:      absolutePath,
		byTokenID: byTokenID,
	}, nil
}

func (store *fileConnectorTokenStore) LookupByTokenID(tokenID string) (connectorTokenRecord, bool, error) {
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

func (store *fileConnectorTokenStore) List() ([]connectorTokenRecord, error) {
	if store == nil {
		return nil, fmt.Errorf("connector token store is nil")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return sortedConnectorTokenRecordsFromMap(store.byTokenID), nil
}

func (store *fileConnectorTokenStore) Get(tokenID string) (connectorTokenRecord, bool, error) {
	return store.LookupByTokenID(tokenID)
}

func (store *fileConnectorTokenStore) Upsert(record connectorTokenRecord) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	normalizedRecord, ok := normalizeConnectorTokenRecord(record)
	if !ok {
		return fmt.Errorf("upsert connector token record: invalid token record")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	nextRecords := sortedConnectorTokenRecordsFromMap(store.byTokenID)
	replaced := false
	for index, existingRecord := range nextRecords {
		if existingRecord.TokenID != normalizedRecord.TokenID {
			continue
		}
		nextRecords[index] = normalizedRecord
		replaced = true
		break
	}
	if !replaced {
		nextRecords = append(nextRecords, normalizedRecord)
	}
	if err := persistConnectorTokenRecordsToFile(store.path, nextRecords); err != nil {
		return err
	}
	store.byTokenID = connectorTokenRecordSliceToMap(nextRecords)
	return nil
}

func (store *fileConnectorTokenStore) Delete(tokenID string) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	normalizedTokenID := strings.TrimSpace(tokenID)
	if normalizedTokenID == "" {
		return nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	nextRecords := sortedConnectorTokenRecordsFromMap(store.byTokenID)
	filteredRecords := make([]connectorTokenRecord, 0, len(nextRecords))
	for _, record := range nextRecords {
		if record.TokenID == normalizedTokenID {
			continue
		}
		filteredRecords = append(filteredRecords, record)
	}
	if err := persistConnectorTokenRecordsToFile(store.path, filteredRecords); err != nil {
		return err
	}
	store.byTokenID = connectorTokenRecordSliceToMap(filteredRecords)
	return nil
}

func (store *fileConnectorTokenStore) ReplaceAll(records []connectorTokenRecord) error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	normalizedRecords, err := normalizeConnectorTokenRecordSet(records)
	if err != nil {
		return fmt.Errorf("replace all connector token records: %w", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := persistConnectorTokenRecordsToFile(store.path, normalizedRecords); err != nil {
		return err
	}
	store.byTokenID = connectorTokenRecordSliceToMap(normalizedRecords)
	return nil
}

func (store *fileConnectorTokenStore) Save() error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	records := sortedConnectorTokenRecordsFromMap(store.byTokenID)
	return persistConnectorTokenRecordsToFile(store.path, records)
}

func (store *fileConnectorTokenStore) Reload() error {
	if store == nil {
		return fmt.Errorf("connector token store is nil")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	reloadedRecords, err := loadConnectorTokenRecordMapFromFile(store.path)
	if err != nil {
		return err
	}
	store.byTokenID = reloadedRecords
	return nil
}

func loadConnectorTokenRecordMapFromFile(filePath string) (map[string]connectorTokenRecord, error) {
	absolutePath, err := filepath.Abs(strings.TrimSpace(filePath))
	if err != nil {
		return nil, fmt.Errorf("load connector token store file: resolve absolute path: %w", err)
	}
	encodedContent, err := os.ReadFile(absolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]connectorTokenRecord), nil
		}
		return nil, fmt.Errorf("load connector token store file: read file: %w", err)
	}
	if len(strings.TrimSpace(string(encodedContent))) == 0 {
		return make(map[string]connectorTokenRecord), nil
	}

	persisted := persistedConnectorTokenStoreFile{}
	if err := yaml.Unmarshal(encodedContent, &persisted); err != nil {
		return nil, fmt.Errorf("load connector token store file: decode yaml: %w", err)
	}
	if persisted.Version != 0 && persisted.Version != connectorTokenStoreFileFormatVersion {
		return nil, fmt.Errorf(
			"load connector token store file: unsupported version=%d",
			persisted.Version,
		)
	}

	records := make([]connectorTokenRecord, 0, len(persisted.Tokens))
	seenTokenIDs := make(map[string]struct{}, len(persisted.Tokens))
	for _, persistedRecord := range persisted.Tokens {
		record := connectorTokenRecord{
			TokenID:         persistedRecord.TokenID,
			ConnectorID:     persistedRecord.ConnectorID,
			TokenSecretHash: persistedRecord.TokenSecretHash,
			HashAlgorithm:   persistedRecord.HashAlgorithm,
			HashVersion:     persistedRecord.HashVersion,
			Status:          connectorTokenStatus(persistedRecord.Status),
			IssuedAt:        persistedRecord.IssuedAt,
			ExpiresAt:       persistedRecord.ExpiresAt,
			RotatedAt:       persistedRecord.RotatedAt,
			Metadata:        persistedRecord.Metadata,
		}
		normalizedRecord, ok := normalizeConnectorTokenRecord(record)
		if !ok {
			return nil, fmt.Errorf("load connector token store file: invalid token record token_id=%q", persistedRecord.TokenID)
		}
		if _, exists := seenTokenIDs[normalizedRecord.TokenID]; exists {
			return nil, fmt.Errorf("load connector token store file: duplicated token_id=%q", normalizedRecord.TokenID)
		}
		seenTokenIDs[normalizedRecord.TokenID] = struct{}{}
		records = append(records, normalizedRecord)
	}
	return connectorTokenRecordSliceToMap(records), nil
}

func persistConnectorTokenRecordsToFile(filePath string, records []connectorTokenRecord) error {
	absolutePath, err := filepath.Abs(strings.TrimSpace(filePath))
	if err != nil {
		return fmt.Errorf("persist connector token store file: resolve absolute path: %w", err)
	}
	normalizedRecords, err := normalizeConnectorTokenRecordSet(records)
	if err != nil {
		return fmt.Errorf("persist connector token store file: %w", err)
	}
	persistedRecords := make([]persistedConnectorTokenRecord, 0, len(records))
	for _, record := range normalizedRecords {
		persistedRecords = append(persistedRecords, persistedConnectorTokenRecord{
			TokenID:         record.TokenID,
			ConnectorID:     record.ConnectorID,
			TokenSecretHash: record.TokenSecretHash,
			HashAlgorithm:   record.HashAlgorithm,
			HashVersion:     record.HashVersion,
			Status:          string(record.Status),
			IssuedAt:        record.IssuedAt,
			ExpiresAt:       record.ExpiresAt,
			RotatedAt:       record.RotatedAt,
			Metadata:        cloneConnectorTokenMetadata(record.Metadata),
		})
	}
	encodedContent, err := yaml.Marshal(&persistedConnectorTokenStoreFile{
		Version: connectorTokenStoreFileFormatVersion,
		Tokens:  persistedRecords,
	})
	if err != nil {
		return fmt.Errorf("persist connector token store file: encode yaml: %w", err)
	}

	configFileDirectory := filepath.Dir(absolutePath)
	if err := os.MkdirAll(configFileDirectory, 0o755); err != nil {
		return fmt.Errorf("persist connector token store file: ensure directory: %w", err)
	}

	fileMode := os.FileMode(0o600)
	if stat, err := os.Stat(absolutePath); err == nil {
		fileMode = stat.Mode().Perm()
	}
	tempFile, err := os.CreateTemp(configFileDirectory, ".bridge-token-store-*.tmp")
	if err != nil {
		return fmt.Errorf("persist connector token store file: create temp file: %w", err)
	}
	tempFilePath := tempFile.Name()
	cleanupTempFile := func() {
		_ = os.Remove(tempFilePath)
	}
	defer cleanupTempFile()
	if err := tempFile.Chmod(fileMode); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("persist connector token store file: chmod temp file: %w", err)
	}
	if _, err := tempFile.Write(encodedContent); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("persist connector token store file: write temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("persist connector token store file: close temp file: %w", err)
	}
	if err := connectorTokenStoreRenameFile(tempFilePath, absolutePath); err != nil {
		return fmt.Errorf("persist connector token store file: replace target file: %w", err)
	}
	return nil
}
