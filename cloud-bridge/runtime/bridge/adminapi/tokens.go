package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ConnectorTokenRecord 定义面向管理面的 connector token 脱敏元数据。
type ConnectorTokenRecord struct {
	TokenID     string            `json:"token_id"`
	ConnectorID string            `json:"connector_id"`
	Status      string            `json:"status"`
	IssuedAtMS  uint64            `json:"issued_at_ms"`
	ExpiresAtMS uint64            `json:"expires_at_ms,omitempty"`
	RotatedAtMS uint64            `json:"rotated_at_ms,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ConnectorTokenCreateRequest 定义创建 connector token 的请求体。
type ConnectorTokenCreateRequest struct {
	ConnectorID string            `json:"connector_id"`
	ExpiresAtMS uint64            `json:"expires_at_ms,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ConnectorTokenIssueResult 表示创建/轮换接口的一次性返回值。
type ConnectorTokenIssueResult struct {
	Record     ConnectorTokenRecord `json:"record"`
	PlainToken string               `json:"plain_token"`
}

func (server *Server) handleConnectorTokens(writer http.ResponseWriter, request *http.Request) {
	if request == nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request is required")
		return
	}
	if request.URL.Path == "/api/admin/connector-tokens" {
		switch request.Method {
		case http.MethodGet:
			server.handleConnectorTokensList(writer, request)
		case http.MethodPost:
			if !authorizeRequestRole(writer, request, RoleAdmin) {
				return
			}
			server.handleConnectorTokensCreate(writer, request)
		default:
			writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET or POST is required")
		}
		return
	}
	if !strings.HasPrefix(request.URL.Path, "/api/admin/connector-tokens/") {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "connector token path is invalid")
		return
	}

	tokenID, action, ok := parseConnectorTokenAction(request.URL.Path)
	if !ok {
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "connector token path is invalid")
		return
	}
	switch {
	case action == "" && request.Method == http.MethodGet:
		server.handleConnectorTokenGet(writer, request, tokenID)
	case action == "rotate" && request.Method == http.MethodPost:
		if !authorizeRequestRole(writer, request, RoleAdmin) {
			return
		}
		server.handleConnectorTokenRotate(writer, request, tokenID)
	case action == "revoke" && request.Method == http.MethodPost:
		if !authorizeRequestRole(writer, request, RoleAdmin) {
			return
		}
		server.handleConnectorTokenRevoke(writer, request, tokenID)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "unsupported connector token operation")
	}
}

func (server *Server) handleConnectorTokensList(writer http.ResponseWriter, request *http.Request) {
	if server == nil || server.dependencies.ListConnectorTokens == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	items, err := server.dependencies.ListConnectorTokens()
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(items),
		"source":      "bridge.adminapi.connector_tokens",
	})
}

func (server *Server) handleConnectorTokensCreate(writer http.ResponseWriter, request *http.Request) {
	if server == nil || server.dependencies.CreateConnectorToken == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	var createRequest ConnectorTokenCreateRequest
	if err := decodeConnectorTokenCreateBody(request, &createRequest); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if strings.TrimSpace(createRequest.ConnectorID) == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "connector_id is required")
		return
	}
	actor := principalFromRequest(request)
	result, err := server.dependencies.CreateConnectorToken(server.now(), createRequest, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("connector_id=%s", strings.TrimSpace(createRequest.ConnectorID)))
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": result,
		"source": "bridge.adminapi.connector_tokens",
	})
}

func (server *Server) handleConnectorTokenGet(writer http.ResponseWriter, request *http.Request, tokenID string) {
	if server == nil || server.dependencies.GetConnectorToken == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	record, found, err := server.dependencies.GetConnectorToken(tokenID)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	if !found {
		writeOperationError(writer, ErrAdminResourceNotFound)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": record,
		"source": "bridge.adminapi.connector_tokens",
	})
}

func (server *Server) handleConnectorTokenRotate(writer http.ResponseWriter, request *http.Request, tokenID string) {
	if server == nil || server.dependencies.RotateConnectorToken == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	result, err := server.dependencies.RotateConnectorToken(server.now(), tokenID, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("token_id=%s", tokenID))
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": result,
		"source": "bridge.adminapi.connector_tokens",
	})
}

func (server *Server) handleConnectorTokenRevoke(writer http.ResponseWriter, request *http.Request, tokenID string) {
	if server == nil || server.dependencies.RevokeConnectorToken == nil {
		writeOperationError(writer, ErrAdminOperationNotSupported)
		return
	}
	actor := principalFromRequest(request)
	record, err := server.dependencies.RevokeConnectorToken(server.now(), tokenID, actor.name)
	if err != nil {
		writeOperationError(writer, err)
		return
	}
	setAuditParamSummary(writer, fmt.Sprintf("token_id=%s", tokenID))
	writeJSON(writer, http.StatusOK, map[string]any{
		"result": record,
		"source": "bridge.adminapi.connector_tokens",
	})
}

func decodeConnectorTokenCreateBody(request *http.Request, target *ConnectorTokenCreateRequest) error {
	if request == nil || request.Body == nil {
		return fmt.Errorf("connector token request body is required")
	}
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		return fmt.Errorf("decode connector token request: %w", err)
	}
	return nil
}

func parseConnectorTokenAction(path string) (tokenID string, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/api/admin/connector-tokens/")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "", "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), "", strings.TrimSpace(parts[0]) != ""
	}
	if len(parts) == 2 {
		normalizedTokenID := strings.TrimSpace(parts[0])
		normalizedAction := strings.TrimSpace(parts[1])
		return normalizedTokenID, normalizedAction, normalizedTokenID != "" && normalizedAction != ""
	}
	return "", "", false
}

func authorizeRequestRole(writer http.ResponseWriter, request *http.Request, requiredRole Role) bool {
	principal := principalFromRequest(request)
	if !roleCanAccess(principal.role, requiredRole) {
		writeError(
			writer,
			http.StatusForbidden,
			"FORBIDDEN",
			fmt.Sprintf("role=%s cannot access role=%s operation", principal.role, requiredRole),
		)
		return false
	}
	return true
}
