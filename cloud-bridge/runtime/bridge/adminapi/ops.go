package adminapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminview"
)

var (
	// ErrAdminOperationNotSupported 表示当前部署未启用指定写操作能力。
	ErrAdminOperationNotSupported = errors.New("admin operation not supported")
	// ErrAdminResourceNotFound 表示目标资源不存在。
	ErrAdminResourceNotFound = errors.New("admin resource not found")
	// ErrAdminVersionConflict 表示配置并发版本冲突。
	ErrAdminVersionConflict = errors.New("admin config version conflict")
	// ErrAdminInvalidArgument 表示写操作参数不合法。
	ErrAdminInvalidArgument = errors.New("admin invalid argument")
)

// ReloadConfigResult 定义配置重载响应模型。
type ReloadConfigResult struct {
	ConfigVersion uint64 `json:"config_version"`
	ReloadedAtMS  uint64 `json:"reloaded_at_ms"`
	Source        string `json:"source,omitempty"`
}

// DrainResult 定义 session/connector drain 操作结果。
type DrainResult struct {
	SessionID           string `json:"session_id"`
	ConnectorID         string `json:"connector_id"`
	PreviousState       string `json:"previous_state"`
	CurrentState        string `json:"current_state"`
	UpdatedServiceCount int    `json:"updated_service_count"`
	PurgedTunnelCount   int    `json:"purged_tunnel_count"`
	Result              string `json:"result"`
}

// ConfigUpdateRequest 定义 `PUT /api/admin/config` 请求体。
type ConfigUpdateRequest struct {
	IfMatchVersion uint64         `json:"if_match_version"`
	Patch          map[string]any `json:"patch"`
}

// ConfigUpdateResult 定义配置更新响应体。
type ConfigUpdateResult struct {
	ConfigVersion   uint64         `json:"config_version"`
	Snapshot        map[string]any `json:"snapshot"`
	ApplyMode       string         `json:"apply_mode"`
	RequiresRestart bool           `json:"requires_restart"`
}

type drainOperationRequest struct {
	Reason string `json:"reason"`
}

// handleOpsConfigReload 执行配置重载命令（operator/admin）。
func (server *Server) handleOpsConfigReload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	if server == nil || server.dependencies.ReloadConfig == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	reloadResult, err := server.dependencies.ReloadConfig(server.now(), actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, "scope=config.reload")
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": reloadResult,
		"source": "bridge.adminapi.ops",
	})
}

// handleOpsSessionDrain 执行单 session 排空命令。
func (server *Server) handleOpsSessionDrain(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	sessionID, ok := parseScopedResourceID(request.URL.Path, "/api/admin/ops/session/", "/drain")
	if !ok {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "session drain path is invalid")
		return
	}
	var operationRequest drainOperationRequest
	if err := decodeOptionalJSONBody(request, &operationRequest); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if server == nil || server.dependencies.DrainSession == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	normalizedReason := strings.TrimSpace(operationRequest.Reason)
	drainResult, err := server.dependencies.DrainSession(server.now(), sessionID, normalizedReason, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("session_id=%s reason=%s", sessionID, normalizedReason))
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": drainResult,
		"source": "bridge.adminapi.ops",
	})
}

// handleOpsConnectorDrain 按 connector 当前会话执行排空命令。
func (server *Server) handleOpsConnectorDrain(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	connectorID, ok := parseScopedResourceID(request.URL.Path, "/api/admin/ops/connector/", "/drain")
	if !ok {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "connector drain path is invalid")
		return
	}
	var operationRequest drainOperationRequest
	if err := decodeOptionalJSONBody(request, &operationRequest); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if server == nil || server.dependencies.DrainConnector == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	normalizedReason := strings.TrimSpace(operationRequest.Reason)
	drainResult, err := server.dependencies.DrainConnector(server.now(), connectorID, normalizedReason, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("connector_id=%s reason=%s", connectorID, normalizedReason))
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": drainResult,
		"source": "bridge.adminapi.ops",
	})
}

// handleConfigUpdate 执行带版本校验的配置更新。
func (server *Server) handleConfigUpdate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "PUT is required")
		return
	}
	var updateRequest ConfigUpdateRequest
	if err := decodeOptionalJSONBody(request, &updateRequest); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if updateRequest.IfMatchVersion == 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "if_match_version is required")
		return
	}
	if len(updateRequest.Patch) == 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "patch is required")
		return
	}
	if server == nil || server.dependencies.UpdateConfig == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	updateResult, err := server.dependencies.UpdateConfig(server.now(), updateRequest, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(
		writer,
		fmt.Sprintf("if_match_version=%d patch_keys=%s", updateRequest.IfMatchVersion, strings.Join(collectPatchKeys(updateRequest.Patch), ",")),
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": updateResult,
		"source": "bridge.adminapi.ops",
	})
}

// handleOpsDiagnoseExport 生成诊断导出并返回短时下载链接。
func (server *Server) handleOpsDiagnoseExport(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	if server == nil || server.exportStore == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	exportPayload := server.buildDiagnoseExportPayload()
	sanitizedPayload, maskedFields := sanitizeExportPayload(exportPayload)
	entry, err := server.exportStore.create(server.now(), sanitizedPayload, maskedFields)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	downloadURL := fmt.Sprintf("/api/admin/ops/diagnose/export/%s/download?token=%s", entry.ExportID, entry.Token)
	setAuditParamSummary(writer, fmt.Sprintf("export_id=%s", entry.ExportID))
	writeJSON(writer, http.StatusOK, map[string]any{
		"export_id":     entry.ExportID,
		"download_url":  downloadURL,
		"expires_at_ms": uint64(entry.ExpireAt.UnixMilli()),
		"masked_fields": entry.MaskedFields,
		"source":        "bridge.adminapi.export",
	})
}

// handleOpsDiagnoseExportDownload 下载导出的诊断 JSON。
func (server *Server) handleOpsDiagnoseExportDownload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	exportID, ok := parseScopedResourceID(request.URL.Path, "/api/admin/ops/diagnose/export/", "/download")
	if !ok {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "diagnose export path is invalid")
		return
	}
	downloadToken := strings.TrimSpace(request.URL.Query().Get("token"))
	if downloadToken == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "token is required")
		return
	}
	if server == nil || server.exportStore == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	entry, exists := server.exportStore.get(exportID, downloadToken, server.now())
	if !exists {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "diagnose export is missing or expired")
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("export_id=%s", exportID))
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set(
		"Content-Disposition",
		fmt.Sprintf("attachment; filename=\"bridge-diagnose-%s.json\"", exportID),
	)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(entry.Payload)
}

// buildDiagnoseExportPayload 组装诊断导出的基础数据（导出前会统一脱敏）。
func (server *Server) buildDiagnoseExportPayload() map[string]any {
	now := server.now()
	sessions := safeListSessions(server.dependencies)
	routes := safeListRoutes(server.dependencies)
	services := safeListServices(server.dependencies)
	tunnelSnapshot := safeTunnelSnapshot(server.dependencies)
	payload := map[string]any{
		"generated_at_ms": uint64(now.UnixMilli()),
		"overview":        adminview.BuildBridgeOverview(now, sessions, services, routes, tunnelSnapshot),
		"routes":          adminview.BuildRouteItems(routes),
		"connectors":      adminview.BuildConnectorItems(sessions, services),
		"sessions":        adminview.BuildSessionItems(sessions),
		"tunnel_summary":  adminview.BuildTunnelSummary(now, tunnelSnapshot),
		"tunnels":         adminview.BuildTunnelItems(safeListTunnels(server.dependencies)),
		"traffic_summary": adminview.BuildTrafficSummary(now, server.dependencies.Metrics),
		"diagnose_summary": adminview.BuildDiagnoseSummary(
			now,
			sessions,
			tunnelSnapshot,
			server.dependencies.Metrics,
		),
	}
	if server.dependencies.BuildConfigSnapshot != nil {
		payload["config_snapshot"] = server.dependencies.BuildConfigSnapshot()
	}
	payload["audit_logs"] = server.auditLogs.query(
		uint64(now.Add(-maxTimeWindow).UnixMilli()),
		uint64(now.UnixMilli()),
	)
	return payload
}

// writeOperationError 把写操作错误统一映射为可预期的 HTTP 错误码。
func writeOperationError(writer http.ResponseWriter, operationError error) {
	switch {
	case errors.Is(operationError, ErrAdminInvalidArgument):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", operationError.Error())
	case errors.Is(operationError, ErrAdminResourceNotFound):
		writeError(writer, http.StatusNotFound, "NOT_FOUND", operationError.Error())
	case errors.Is(operationError, ErrAdminVersionConflict):
		writeError(writer, http.StatusConflict, "CONFLICT", operationError.Error())
	case errors.Is(operationError, ErrAdminOperationNotSupported):
		writeError(writer, http.StatusNotImplemented, "NOT_SUPPORTED", operationError.Error())
	default:
		writeError(writer, http.StatusInternalServerError, "INTERNAL", operationError.Error())
	}
}

// parseScopedResourceID 从形如 /prefix/{id}/suffix 的路径中提取资源 ID。
func parseScopedResourceID(path string, prefix string, suffix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimSuffix(trimmed, suffix)
	resourceID := strings.TrimSpace(trimmed)
	if resourceID == "" || strings.Contains(resourceID, "/") {
		return "", false
	}
	return resourceID, true
}

// decodeOptionalJSONBody 解析可选 JSON 请求体；空 body 视为零值对象。
func decodeOptionalJSONBody(request *http.Request, out any) error {
	if request == nil || request.Body == nil || out == nil {
		return nil
	}
	rawBody, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body failed: %w", err)
	}
	if len(strings.TrimSpace(string(rawBody))) == 0 {
		return nil
	}
	if err := json.Unmarshal(rawBody, out); err != nil {
		return fmt.Errorf("decode request body failed: %w", err)
	}
	return nil
}

// collectPatchKeys 返回配置 patch 的排序 key 列表，便于审计输出稳定。
func collectPatchKeys(patch map[string]any) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}
