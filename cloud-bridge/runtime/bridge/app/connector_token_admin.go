package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
)

func listConnectorTokensForAdmin(tokenAdmin appauth.TokenAdmin) ([]adminapi.ConnectorTokenRecord, error) {
	if tokenAdmin == nil {
		return nil, adminapi.ErrAdminOperationNotSupported
	}
	records, err := tokenAdmin.List()
	if err != nil {
		return nil, mapConnectorTokenAdminError(err)
	}
	result := make([]adminapi.ConnectorTokenRecord, 0, len(records))
	for _, record := range records {
		result = append(result, buildAdminConnectorTokenRecord(record))
	}
	return result, nil
}

func getConnectorTokenForAdmin(
	tokenAdmin appauth.TokenAdmin,
	tokenID string,
) (adminapi.ConnectorTokenRecord, bool, error) {
	if tokenAdmin == nil {
		return adminapi.ConnectorTokenRecord{}, false, adminapi.ErrAdminOperationNotSupported
	}
	record, found, err := tokenAdmin.Get(tokenID)
	if err != nil {
		return adminapi.ConnectorTokenRecord{}, false, mapConnectorTokenAdminError(err)
	}
	if !found {
		return adminapi.ConnectorTokenRecord{}, false, nil
	}
	return buildAdminConnectorTokenRecord(record), true, nil
}

func createConnectorTokenForAdmin(
	tokenAdmin appauth.TokenAdmin,
	request adminapi.ConnectorTokenCreateRequest,
) (adminapi.ConnectorTokenIssueResult, error) {
	if tokenAdmin == nil {
		return adminapi.ConnectorTokenIssueResult{}, adminapi.ErrAdminOperationNotSupported
	}
	issueResult, err := tokenAdmin.Create(appauth.TokenCreateRequest{
		ConnectorID: strings.TrimSpace(request.ConnectorID),
		ExpiresAt:   timestampMillisToTime(request.ExpiresAtMS),
		Metadata:    cloneAdminConnectorTokenMetadata(request.Metadata),
	})
	if err != nil {
		return adminapi.ConnectorTokenIssueResult{}, mapConnectorTokenAdminError(err)
	}
	return buildAdminConnectorTokenIssueResult(issueResult), nil
}

func rotateConnectorTokenForAdmin(
	tokenAdmin appauth.TokenAdmin,
	tokenID string,
) (adminapi.ConnectorTokenIssueResult, error) {
	if tokenAdmin == nil {
		return adminapi.ConnectorTokenIssueResult{}, adminapi.ErrAdminOperationNotSupported
	}
	issueResult, err := tokenAdmin.Rotate(appauth.TokenRotateRequest{TokenID: strings.TrimSpace(tokenID)})
	if err != nil {
		return adminapi.ConnectorTokenIssueResult{}, mapConnectorTokenAdminError(err)
	}
	return buildAdminConnectorTokenIssueResult(issueResult), nil
}

func revokeConnectorTokenForAdmin(
	tokenAdmin appauth.TokenAdmin,
	tokenID string,
) (adminapi.ConnectorTokenRecord, error) {
	if tokenAdmin == nil {
		return adminapi.ConnectorTokenRecord{}, adminapi.ErrAdminOperationNotSupported
	}
	record, err := tokenAdmin.Revoke(strings.TrimSpace(tokenID))
	if err != nil {
		return adminapi.ConnectorTokenRecord{}, mapConnectorTokenAdminError(err)
	}
	return buildAdminConnectorTokenRecord(record), nil
}

func buildAdminConnectorTokenRecord(record appauth.TokenRecord) adminapi.ConnectorTokenRecord {
	return adminapi.ConnectorTokenRecord{
		ConnectorID: strings.TrimSpace(record.ConnectorID),
		TokenID:     strings.TrimSpace(record.TokenID),
		Status:      string(record.Status),
		IssuedAtMS:  timeToTimestampMillis(record.IssuedAt),
		ExpiresAtMS: timeToTimestampMillis(record.ExpiresAt),
		RotatedAtMS: timeToTimestampMillis(record.RotatedAt),
		Metadata:    cloneAdminConnectorTokenMetadata(record.Metadata),
	}
}

func buildAdminConnectorTokenIssueResult(issueResult appauth.TokenIssueResult) adminapi.ConnectorTokenIssueResult {
	return adminapi.ConnectorTokenIssueResult{
		Record:     buildAdminConnectorTokenRecord(issueResult.Record),
		PlainToken: strings.TrimSpace(issueResult.PlaintextToken),
	}
}

func mapConnectorTokenAdminError(operationError error) error {
	switch {
	case errors.Is(operationError, appauth.ErrTokenAdminInvalidArgument):
		return fmt.Errorf("%w: %v", adminapi.ErrAdminInvalidArgument, operationError)
	case errors.Is(operationError, appauth.ErrTokenAdminNotFound):
		return fmt.Errorf("%w: %v", adminapi.ErrAdminResourceNotFound, operationError)
	case errors.Is(operationError, appauth.ErrTokenAdminStoreUnavailable):
		return fmt.Errorf("%w: %v", adminapi.ErrAdminOperationNotSupported, operationError)
	default:
		return operationError
	}
}

func timeToTimestampMillis(value time.Time) uint64 {
	if value.IsZero() {
		return 0
	}
	return uint64(value.UTC().UnixMilli())
}

func timestampMillisToTime(value uint64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(value)).UTC()
}

func cloneAdminConnectorTokenMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clonedMetadata := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clonedMetadata[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return clonedMetadata
}
