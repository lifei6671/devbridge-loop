package validate

import (
	"strconv"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// ValidateFullSyncRequest 校验 full-sync 请求参数。
func ValidateFullSyncRequest(request pb.FullSyncRequest) error {
	// requestId 用于关联请求和快照响应，不能为空。
	if strings.TrimSpace(request.RequestID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "requestId is required")
	}
	// connectorId 用于多连接器并发场景隔离。
	if strings.TrimSpace(request.ConnectorID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connectorId is required")
	}
	// sessionId 缺失会导致去重与追踪不可用。
	if strings.TrimSpace(request.SessionID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "sessionId is required")
	}
	// sessionEpoch 必须大于 0 以保障代际隔离。
	if request.SessionEpoch == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "sessionEpoch must be greater than 0")
	}
	return nil
}

// ValidateFullSyncSnapshot 校验 full-sync 快照合法性。
func ValidateFullSyncSnapshot(snapshot pb.FullSyncSnapshot) error {
	// requestId 用于与 full-sync 请求关联。
	if strings.TrimSpace(snapshot.RequestID) == "" {
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "requestId is required")
	}
	// sessionEpoch 必须有效，防止旧会话快照污染当前状态。
	if snapshot.SessionEpoch == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidSessionEpoch, "sessionEpoch must be greater than 0")
	}
	// snapshotVersion 必须有效，供后续增量追赶使用。
	if snapshot.SnapshotVersion == 0 {
		return ltfperrors.New(ltfperrors.CodeInvalidResourceVersion, "snapshotVersion must be greater than 0")
	}

	for index, service := range snapshot.Services {
		// serviceId 与 serviceKey 都是快照定位关键字段。
		if strings.TrimSpace(service.ServiceID) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "snapshot.services["+strconv.Itoa(index)+"].serviceId is required")
		}
		if strings.TrimSpace(service.ServiceKey) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "snapshot.services["+strconv.Itoa(index)+"].serviceKey is required")
		}
	}
	for index, route := range snapshot.Routes {
		// routeId 为空会导致后续增量更新无法定位。
		if strings.TrimSpace(route.RouteID) == "" {
			return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "snapshot.routes["+strconv.Itoa(index)+"].routeId is required")
		}
	}
	return nil
}
