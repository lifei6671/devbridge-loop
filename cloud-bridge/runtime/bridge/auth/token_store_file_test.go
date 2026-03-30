package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestFileConnectorTokenStoreSaveAndReload(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	storeFilePath := filepath.Join(tempDir, "bridge.tokens.yaml")
	store, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("create file token store failed: %v", err)
	}

	now := time.Date(2026, 3, 29, 19, 0, 0, 0, time.UTC)
	records := []connectorTokenRecord{
		{
			TokenID:         "agent-local",
			ConnectorID:     "agent-local",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-local"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
			IssuedAt:        now,
			Metadata: map[string]string{
				"owner": "qa",
			},
		},
	}
	if err := store.ReplaceAll(records); err != nil {
		testingObject.Fatalf("replace records failed: %v", err)
	}

	reloadedStore, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("reload file token store failed: %v", err)
	}
	reloadedRecord, found, err := reloadedStore.LookupByTokenID("agent-local")
	if err != nil {
		testingObject.Fatalf("lookup reloaded token failed: %v", err)
	}
	if !found {
		testingObject.Fatalf("expected token after reload")
	}
	if reloadedRecord.ConnectorID != "agent-local" {
		testingObject.Fatalf("unexpected reloaded connector id: got=%s want=agent-local", reloadedRecord.ConnectorID)
	}

	listedRecords, err := reloadedStore.List()
	if err != nil {
		testingObject.Fatalf("list reloaded records failed: %v", err)
	}
	if len(listedRecords) != 1 {
		testingObject.Fatalf("unexpected reloaded record count: got=%d want=1", len(listedRecords))
	}

	rawPersistedData, err := os.ReadFile(storeFilePath)
	if err != nil {
		testingObject.Fatalf("read persisted file failed: %v", err)
	}
	if strings.Contains(string(rawPersistedData), "secret-local") {
		testingObject.Fatalf("expected persisted file to omit raw token secret")
	}
}

func TestFileConnectorTokenStoreSaveRecordsKeepsPreviousFileWhenRenameFails(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	storeFilePath := filepath.Join(tempDir, "bridge.tokens.yaml")
	store, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("create file token store failed: %v", err)
	}

	initialRecords := []connectorTokenRecord{
		{
			TokenID:         "agent-initial",
			ConnectorID:     "agent-initial",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-initial"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	}
	if err := store.ReplaceAll(initialRecords); err != nil {
		testingObject.Fatalf("replace initial records failed: %v", err)
	}
	originalFileData, err := os.ReadFile(storeFilePath)
	if err != nil {
		testingObject.Fatalf("read original file failed: %v", err)
	}

	originalRenameFile := connectorTokenStoreRenameFile
	connectorTokenStoreRenameFile = func(oldPath string, newPath string) error {
		return errors.New("rename failed")
	}
	defer func() {
		connectorTokenStoreRenameFile = originalRenameFile
	}()
	updatedRecords := []connectorTokenRecord{
		{
			TokenID:         "agent-updated",
			ConnectorID:     "agent-updated",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-updated"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	}
	if err := store.ReplaceAll(updatedRecords); err == nil {
		testingObject.Fatalf("expected replace records to fail when rename fails")
	}

	currentFileData, err := os.ReadFile(storeFilePath)
	if err != nil {
		testingObject.Fatalf("read current file failed: %v", err)
	}
	if string(currentFileData) != string(originalFileData) {
		testingObject.Fatalf("expected persisted file to remain unchanged after rename failure")
	}

	record, found, err := store.LookupByTokenID("agent-initial")
	if err != nil {
		testingObject.Fatalf("lookup initial record after failure failed: %v", err)
	}
	if !found {
		testingObject.Fatalf("expected initial record to remain after failed save")
	}
	if record.ConnectorID != "agent-initial" {
		testingObject.Fatalf("unexpected connector id after failed save: got=%s want=agent-initial", record.ConnectorID)
	}
}

func TestFileConnectorTokenStoreReplaceAllRejectsDuplicateTokenIDs(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	storeFilePath := filepath.Join(tempDir, "bridge.tokens.yaml")
	store, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("create file token store failed: %v", err)
	}

	err = store.ReplaceAll([]connectorTokenRecord{
		{
			TokenID:         "agent-dup",
			ConnectorID:     "agent-a",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-a"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
		{
			TokenID:         "agent-dup",
			ConnectorID:     "agent-b",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-b"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	})
	if err == nil {
		testingObject.Fatalf("expected duplicate token ids to be rejected")
	}
}

func TestInMemoryConnectorTokenStoreReplaceAllRejectsDuplicateTokenIDs(testingObject *testing.T) {
	testingObject.Parallel()

	store := newInMemoryConnectorTokenStore(nil)
	err := store.ReplaceAll([]connectorTokenRecord{
		{
			TokenID:         "agent-dup",
			ConnectorID:     "agent-a",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-a"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
		{
			TokenID:         "agent-dup",
			ConnectorID:     "agent-b",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-b"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	})
	if err == nil {
		testingObject.Fatalf("expected in-memory store to reject duplicate token ids")
	}
}

func TestFileConnectorTokenStoreReplaceAllMaintainsReadableSnapshots(testingObject *testing.T) {
	tempDir := testingObject.TempDir()
	storeFilePath := filepath.Join(tempDir, "bridge.tokens.yaml")
	store, err := newFileConnectorTokenStore(storeFilePath)
	if err != nil {
		testingObject.Fatalf("create file token store failed: %v", err)
	}

	if err := store.ReplaceAll([]connectorTokenRecord{
		{
			TokenID:         "agent-initial",
			ConnectorID:     "agent-initial",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-initial"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	}); err != nil {
		testingObject.Fatalf("seed file token store failed: %v", err)
	}
	initialSnapshotBytes, err := os.ReadFile(storeFilePath)
	if err != nil {
		testingObject.Fatalf("read initial snapshot failed: %v", err)
	}
	updatedRecords := []connectorTokenRecord{
		{
			TokenID:         "agent-a",
			ConnectorID:     "agent-a",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-a"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
		{
			TokenID:         "agent-b",
			ConnectorID:     "agent-b",
			TokenSecretHash: mustHashConnectorTokenSecretArgon2ID("secret-b"),
			HashAlgorithm:   connectorTokenHashAlgorithmArgon2ID,
			HashVersion:     connectorTokenHashVersionV1,
			Status:          connectorTokenStatusActive,
		},
	}
	updatedSnapshotBytes, err := marshalConnectorTokenRecordsForTest(updatedRecords)
	if err != nil {
		testingObject.Fatalf("marshal updated snapshot failed: %v", err)
	}

	stopChannel := make(chan struct{})
	errorChannel := make(chan error, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for {
			select {
			case <-stopChannel:
				return
			default:
			}
			rawFileData, err := os.ReadFile(storeFilePath)
			if err != nil {
				errorChannel <- err
				return
			}
			if len(bytes.TrimSpace(rawFileData)) == 0 {
				errorChannel <- errors.New("empty token store snapshot")
				return
			}
			if !bytes.Equal(rawFileData, initialSnapshotBytes) && !bytes.Equal(rawFileData, updatedSnapshotBytes) {
				errorChannel <- errors.New("reader observed partial token store snapshot")
				return
			}
		}
	}()

	for index := 0; index < 16; index++ {
		if err := store.ReplaceAll(updatedRecords); err != nil {
			close(stopChannel)
			waitGroup.Wait()
			testingObject.Fatalf("replace all failed during concurrent snapshot read: %v", err)
		}
	}
	close(stopChannel)
	waitGroup.Wait()

	select {
	case err := <-errorChannel:
		testingObject.Fatalf("expected concurrent readers to observe only complete snapshots: %v", err)
	default:
	}
}

func marshalConnectorTokenRecordsForTest(records []connectorTokenRecord) ([]byte, error) {
	normalizedRecords, err := normalizeConnectorTokenRecordSet(records)
	if err != nil {
		return nil, err
	}
	persistedRecords := make([]persistedConnectorTokenRecord, 0, len(normalizedRecords))
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
	return yaml.Marshal(&persistedConnectorTokenStoreFile{
		Version: connectorTokenStoreFileFormatVersion,
		Tokens:  persistedRecords,
	})
}
