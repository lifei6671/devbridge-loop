package auth

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
)

// connectorAuthAuditRecord 描述认证审计日志所需的稳定字段集合。
type connectorAuthAuditRecord struct {
	ConnectorID  string
	TokenID      string
	SessionID    string
	SessionEpoch uint64
	SourceIP     string
	ErrorCode    string
}

// emitConnectorAuthAuditLog 输出认证成功/失败的统一审计日志。
func emitConnectorAuthAuditLog(success bool, record connectorAuthAuditRecord) {
	normalizedRecord := normalizeConnectorAuthAuditRecord(record)
	result := "success"
	level := slog.LevelInfo
	if !success {
		// 拒绝路径使用 warn，便于安全事件筛选和告警规则复用。
		result = "rejected"
		level = slog.LevelWarn
	}
	slog.Log(
		context.Background(),
		level,
		"connector auth audit",
		"event", "connector_auth",
		"result", result,
		obs.LogFieldConnectorID, normalizedRecord.ConnectorID,
		obs.LogFieldTokenID, normalizedRecord.TokenID,
		obs.LogFieldSessionID, normalizedRecord.SessionID,
		obs.LogFieldSessionEpoch, normalizedRecord.SessionEpoch,
		obs.LogFieldSourceIP, normalizedRecord.SourceIP,
		obs.LogFieldErrorCode, normalizedRecord.ErrorCode,
	)
}

// normalizeConnectorAuthAuditRecord 统一审计字段格式并执行脱敏。
func normalizeConnectorAuthAuditRecord(record connectorAuthAuditRecord) connectorAuthAuditRecord {
	return connectorAuthAuditRecord{
		ConnectorID:  strings.TrimSpace(record.ConnectorID),
		TokenID:      maskConnectorTokenID(record.TokenID),
		SessionID:    strings.TrimSpace(record.SessionID),
		SessionEpoch: record.SessionEpoch,
		SourceIP:     normalizeConnectorAuthSourceIP(record.SourceIP),
		ErrorCode:    strings.TrimSpace(record.ErrorCode),
	}
}

// extractConnectorTokenIDForAudit 从 token 中提取 token_id，供审计日志脱敏输出。
func extractConnectorTokenIDForAudit(rawToken string) string {
	tokenID, _, ok := parseConnectorToken(rawToken)
	if ok {
		return tokenID
	}
	return ""
}

// maskConnectorTokenID 对 token_id 做最小可识别脱敏，避免原值直接暴露到日志。
func maskConnectorTokenID(rawTokenID string) string {
	normalizedTokenID := strings.TrimSpace(rawTokenID)
	if normalizedTokenID == "" {
		return ""
	}
	if len(normalizedTokenID) <= 4 {
		return "****"
	}
	return "****" + normalizedTokenID[len(normalizedTokenID)-4:]
}

// normalizeConnectorAuthSourceIP 从远端地址中提取稳定的 source_ip 字段。
func normalizeConnectorAuthSourceIP(rawPeerAddr string) string {
	normalizedPeerAddr := strings.TrimSpace(rawPeerAddr)
	if normalizedPeerAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(normalizedPeerAddr)
	if err != nil {
		// bufconn / pipe 等测试场景可能不是 host:port，保留原始标签便于排查。
		return strings.Trim(normalizedPeerAddr, "[]")
	}
	return strings.Trim(strings.TrimSpace(host), "[]")
}
