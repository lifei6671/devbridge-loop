package consistency

import (
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// VersionRelation 表示 incoming 与 current 的版本关系。
type VersionRelation int

const (
	// VersionRelationOlder 表示 incoming 版本小于 current。
	VersionRelationOlder VersionRelation = -1
	// VersionRelationEqual 表示 incoming 与 current 版本一致。
	VersionRelationEqual VersionRelation = 0
	// VersionRelationNewer 表示 incoming 版本大于 current。
	VersionRelationNewer VersionRelation = 1
)

// BuildDedupKey 构造事件去重键。
func BuildDedupKey(sessionID string, sessionEpoch uint64, eventID string) (string, error) {
	normalizedSessionID := strings.TrimSpace(sessionID)
	normalizedEventID := strings.TrimSpace(eventID)
	// sessionId 为空会导致不同会话事件互相污染。
	if normalizedSessionID == "" {
		return "", ltfperrors.New(ltfperrors.CodeMissingRequiredField, "sessionId is required")
	}
	// sessionEpoch 必须为正数以保证代际隔离。
	if sessionEpoch == 0 {
		return "", ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "sessionEpoch must be greater than 0")
	}
	// eventId 为空会破坏幂等语义。
	if normalizedEventID == "" {
		return "", ltfperrors.New(ltfperrors.CodeInvalidEventID, "eventId is required")
	}
	// key 由 session+epoch+event 三元组构成，满足资源级幂等要求。
	return fmt.Sprintf("%s:%d:%s", normalizedSessionID, sessionEpoch, normalizedEventID), nil
}

// CompareResourceVersion 比较资源版本关系。
func CompareResourceVersion(currentVersion uint64, incomingVersion uint64) VersionRelation {
	// incoming 小于 current 时视为版本回退。
	if incomingVersion < currentVersion {
		return VersionRelationOlder
	}
	// incoming 大于 current 时视为新版本。
	if incomingVersion > currentVersion {
		return VersionRelationNewer
	}
	// incoming 等于 current 时视为重复版本。
	return VersionRelationEqual
}

// ValidateResourceVersionAdvance 校验资源版本是否允许推进。
func ValidateResourceVersionAdvance(currentVersion uint64, incomingVersion uint64) error {
	relation := CompareResourceVersion(currentVersion, incomingVersion)
	// 旧版本事件必须拒绝，避免状态被回退。
	if relation == VersionRelationOlder {
		return ltfperrors.New(ltfperrors.CodeVersionRollback, "incoming resourceVersion is older than current resourceVersion")
	}
	return nil
}
