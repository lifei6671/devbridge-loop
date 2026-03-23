package adminapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultAdminSessionCookieName = "devbridge_admin_session"
	defaultAdminSessionTTL        = 12 * time.Hour

	authProviderTypePassword = "password"
	authProviderFlowPassword = "password_form"
)

// AuthProviderConfig 定义 adminapi 消费的认证 provider 配置。
type AuthProviderConfig struct {
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Label    string                 `json:"label"`
	Enabled  bool                   `json:"enabled"`
	Password PasswordProviderConfig `json:"password"`
}

// PasswordProviderConfig 定义本地用户名密码登录 provider 配置。
type PasswordProviderConfig struct {
	Accounts []PasswordAccountConfig `json:"accounts"`
}

// PasswordAccountConfig 定义一个本地账号。
type PasswordAccountConfig struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        Role   `json:"role"`
}

type authProviderDescriptor struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	LoginFlow string `json:"login_flow"`
}

type authLoginRequest struct {
	Provider string `json:"provider"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type authProvider interface {
	Descriptor() authProviderDescriptor
	Authenticate(ctx context.Context, request authLoginRequest) (principal, error)
}

type passwordProvider struct {
	descriptor authProviderDescriptor
	accounts   map[string]passwordAccount
}

type passwordAccount struct {
	username    string
	password    string
	displayName string
	role        Role
}

func (provider *passwordProvider) Descriptor() authProviderDescriptor {
	if provider == nil {
		return authProviderDescriptor{}
	}
	return provider.descriptor
}

func (provider *passwordProvider) Authenticate(_ context.Context, request authLoginRequest) (principal, error) {
	if provider == nil {
		return principal{}, fmt.Errorf("password auth provider unavailable")
	}
	username := strings.TrimSpace(request.Username)
	if username == "" {
		return principal{}, fmt.Errorf("missing username")
	}
	account, exists := provider.accounts[username]
	if !exists {
		return principal{}, fmt.Errorf("invalid username or password")
	}
	if subtle.ConstantTimeCompare([]byte(account.password), []byte(request.Password)) != 1 {
		return principal{}, fmt.Errorf("invalid username or password")
	}
	displayName := strings.TrimSpace(account.displayName)
	if displayName == "" {
		displayName = account.username
	}
	return principal{
		name:        account.username,
		displayName: displayName,
		role:        account.role,
		provider:    provider.descriptor.Name,
	}, nil
}

type authSession struct {
	token     string
	csrfToken string
	principal principal
	expiresAt time.Time
}

type authSessionStore struct {
	mutex sync.Mutex
	items map[string]authSession
	ttl   time.Duration
}

func newAuthSessionStore(ttl time.Duration) *authSessionStore {
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		normalizedTTL = defaultAdminSessionTTL
	}
	return &authSessionStore{
		items: make(map[string]authSession),
		ttl:   normalizedTTL,
	}
}

func (store *authSessionStore) create(now time.Time, actor principal) (authSession, error) {
	if store == nil {
		return authSession{}, fmt.Errorf("auth session store unavailable")
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	token, err := generateSecureToken(32)
	if err != nil {
		return authSession{}, err
	}
	csrfToken, err := generateSecureToken(24)
	if err != nil {
		return authSession{}, err
	}
	session := authSession{
		token:     token,
		csrfToken: csrfToken,
		principal: actor,
		expiresAt: normalizedNow.Add(store.ttl),
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.deleteExpiredLocked(normalizedNow)
	store.items[token] = session
	return session, nil
}

func (store *authSessionStore) get(now time.Time, token string) (authSession, bool) {
	if store == nil {
		return authSession{}, false
	}
	normalizedToken := strings.TrimSpace(token)
	if normalizedToken == "" {
		return authSession{}, false
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.deleteExpiredLocked(normalizedNow)
	session, exists := store.items[normalizedToken]
	if !exists {
		return authSession{}, false
	}
	return session, true
}

func (store *authSessionStore) delete(token string) {
	if store == nil {
		return
	}
	normalizedToken := strings.TrimSpace(token)
	if normalizedToken == "" {
		return
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.items, normalizedToken)
}

func (store *authSessionStore) deleteExpiredLocked(now time.Time) {
	for token, session := range store.items {
		if !session.expiresAt.After(now) {
			delete(store.items, token)
		}
	}
}

func generateSecureToken(size int) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("invalid secure token size=%d", size)
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func buildAuthProviders(configs []AuthProviderConfig) (map[string]authProvider, []authProviderDescriptor, error) {
	providers := make(map[string]authProvider, len(configs))
	descriptors := make([]authProviderDescriptor, 0, len(configs))
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		name := strings.TrimSpace(config.Name)
		if name == "" {
			return nil, nil, fmt.Errorf("build auth providers: empty provider name")
		}
		if _, exists := providers[name]; exists {
			return nil, nil, fmt.Errorf("build auth providers: duplicated provider name=%s", name)
		}
		providerType := strings.ToLower(strings.TrimSpace(config.Type))
		switch providerType {
		case authProviderTypePassword:
			accounts := make(map[string]passwordAccount, len(config.Password.Accounts))
			for _, accountConfig := range config.Password.Accounts {
				username := strings.TrimSpace(accountConfig.Username)
				if username == "" {
					return nil, nil, fmt.Errorf("build auth providers: empty username for provider=%s", name)
				}
				if _, exists := accounts[username]; exists {
					return nil, nil, fmt.Errorf("build auth providers: duplicated username=%s for provider=%s", username, name)
				}
				accounts[username] = passwordAccount{
					username:    username,
					password:    accountConfig.Password,
					displayName: strings.TrimSpace(accountConfig.DisplayName),
					role:        accountConfig.Role,
				}
			}
			provider := &passwordProvider{
				descriptor: authProviderDescriptor{
					Name:      name,
					Type:      authProviderTypePassword,
					Label:     defaultProviderLabel(config.Label, "本地账号"),
					LoginFlow: authProviderFlowPassword,
				},
				accounts: accounts,
			}
			providers[name] = provider
			descriptors = append(descriptors, provider.descriptor)
		default:
			return nil, nil, fmt.Errorf("build auth providers: unsupported provider type=%s", config.Type)
		}
	}
	if len(providers) == 0 {
		return nil, nil, fmt.Errorf("build auth providers: empty enabled providers")
	}
	sort.Slice(descriptors, func(indexA int, indexB int) bool {
		return descriptors[indexA].Name < descriptors[indexB].Name
	})
	return providers, descriptors, nil
}

func defaultProviderLabel(rawLabel string, fallback string) string {
	normalizedLabel := strings.TrimSpace(rawLabel)
	if normalizedLabel != "" {
		return normalizedLabel
	}
	return fallback
}

func (server *Server) authenticateLoginRequest(request *http.Request) (principal, error) {
	if server == nil {
		return principal{}, fmt.Errorf("admin api server unavailable")
	}
	if request == nil {
		return principal{}, fmt.Errorf("missing request")
	}
	var loginRequest authLoginRequest
	if err := json.NewDecoder(request.Body).Decode(&loginRequest); err != nil {
		return principal{}, fmt.Errorf("invalid login payload")
	}
	providerName := strings.TrimSpace(loginRequest.Provider)
	if providerName == "" && len(server.providerDescriptors) == 1 {
		providerName = server.providerDescriptors[0].Name
		loginRequest.Provider = providerName
	}
	if providerName == "" {
		return principal{}, fmt.Errorf("missing auth provider")
	}
	provider, exists := server.authProviders[providerName]
	if !exists {
		return principal{}, fmt.Errorf("unsupported auth provider")
	}
	return provider.Authenticate(request.Context(), loginRequest)
}

func (server *Server) handleAuthProviders(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"providers": server.providerDescriptors,
	})
}

func (server *Server) handleAuthSession(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	session, exists := server.resolveSession(request)
	if !exists {
		server.clearAuthCookies(writer, request)
		server.writeAnonymousAuthResponse(writer)
		return
	}
	server.writeAuthSessionResponse(writer, session)
}

func (server *Server) handleAuthLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	startAt := server.now()
	if securityErr := server.enforcePublicOriginSecurity(request); securityErr != nil {
		writeError(writer, http.StatusForbidden, "FORBIDDEN", securityErr.Error())
		server.appendAuditRecord(startAt, AuditRecord{
			Method:    request.Method,
			Path:      request.URL.Path,
			Scope:     "auth",
			Action:    "login",
			Status:    http.StatusForbidden,
			Result:    "rejected",
			TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
			ErrorCode: "FORBIDDEN",
		})
		return
	}
	actor, authErr := server.authenticateLoginRequest(request)
	if authErr != nil {
		writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED", authErr.Error())
		server.appendAuditRecord(startAt, AuditRecord{
			Method:    request.Method,
			Path:      request.URL.Path,
			Scope:     "auth",
			Action:    "login",
			Status:    http.StatusUnauthorized,
			Result:    "rejected",
			TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
			ErrorCode: "UNAUTHORIZED",
		})
		return
	}
	session, err := server.sessionStore.create(startAt, actor)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "create session failed")
		server.appendAuditRecord(startAt, AuditRecord{
			Actor:     actor.name,
			Role:      string(actor.role),
			Method:    request.Method,
			Path:      request.URL.Path,
			Scope:     "auth",
			Action:    "login",
			Status:    http.StatusInternalServerError,
			Result:    "failed",
			TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
			ErrorCode: "INTERNAL",
		})
		return
	}
	server.setAuthCookies(writer, request, session)
	server.writeAuthSessionResponse(writer, session)
	server.appendAuditRecord(startAt, AuditRecord{
		Actor:     actor.name,
		Role:      string(actor.role),
		Method:    request.Method,
		Path:      request.URL.Path,
		Scope:     "auth",
		Action:    "login",
		Status:    http.StatusOK,
		Result:    "success",
		TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
		ErrorCode: "",
	})
}

func (server *Server) handleAuthLogout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST is required")
		return
	}
	startAt := server.now()
	if securityErr := server.enforceWriteRequestSecurity(request); securityErr != nil {
		writeError(writer, http.StatusForbidden, "FORBIDDEN", securityErr.Error())
		server.appendAuditRecord(startAt, AuditRecord{
			Method:    request.Method,
			Path:      request.URL.Path,
			Scope:     "auth",
			Action:    "logout",
			Status:    http.StatusForbidden,
			Result:    "rejected",
			TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
			ErrorCode: "FORBIDDEN",
		})
		return
	}
	session, exists := server.resolveSession(request)
	if exists {
		server.sessionStore.delete(session.token)
	}
	server.clearAuthCookies(writer, request)
	server.writeAnonymousAuthResponse(writer)
	actorName := ""
	actorRole := ""
	if exists {
		actorName = session.principal.name
		actorRole = string(session.principal.role)
	}
	server.appendAuditRecord(startAt, AuditRecord{
		Actor:     actorName,
		Role:      actorRole,
		Method:    request.Method,
		Path:      request.URL.Path,
		Scope:     "auth",
		Action:    "logout",
		Status:    http.StatusOK,
		Result:    "success",
		TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
		ErrorCode: "",
	})
}

func (server *Server) resolveSession(request *http.Request) (authSession, bool) {
	if server == nil || request == nil {
		return authSession{}, false
	}
	sessionToken := extractTokenFromCookie(request, server.sessionCookieName)
	if sessionToken == "" {
		return authSession{}, false
	}
	return server.sessionStore.get(server.now(), sessionToken)
}

func (server *Server) writeAuthSessionResponse(writer http.ResponseWriter, session authSession) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"authenticated":    true,
		"providers":        server.providerDescriptors,
		"csrf_header_name": server.csrfHeaderNameOrDefault(),
		"session": map[string]any{
			"username":      session.principal.name,
			"display_name":  session.principal.displayName,
			"role":          string(session.principal.role),
			"provider":      session.principal.provider,
			"csrf_token":    session.csrfToken,
			"expires_at_ms": uint64(session.expiresAt.UTC().UnixMilli()),
		},
	})
}

func (server *Server) writeAnonymousAuthResponse(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"authenticated":    false,
		"providers":        server.providerDescriptors,
		"csrf_header_name": server.csrfHeaderNameOrDefault(),
	})
}

func (server *Server) csrfHeaderNameOrDefault() string {
	if server == nil {
		return defaultCSRFHeaderName
	}
	normalizedHeaderName := strings.TrimSpace(server.csrfHeaderName)
	if normalizedHeaderName == "" {
		return defaultCSRFHeaderName
	}
	return normalizedHeaderName
}

func (server *Server) setAuthCookies(writer http.ResponseWriter, request *http.Request, session authSession) {
	if server == nil {
		return
	}
	secure := isSecureRequest(request)
	http.SetCookie(writer, &http.Cookie{
		Name:     server.sessionCookieName,
		Value:    session.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  session.expiresAt,
		MaxAge:   int(time.Until(session.expiresAt).Seconds()),
	})
	http.SetCookie(writer, &http.Cookie{
		Name:     server.csrfCookieName,
		Value:    session.csrfToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  session.expiresAt,
		MaxAge:   int(time.Until(session.expiresAt).Seconds()),
	})
}

func (server *Server) clearAuthCookies(writer http.ResponseWriter, request *http.Request) {
	if server == nil {
		return
	}
	secure := isSecureRequest(request)
	http.SetCookie(writer, &http.Cookie{
		Name:     server.sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
	http.SetCookie(writer, &http.Cookie{
		Name:     server.csrfCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func isSecureRequest(request *http.Request) bool {
	if request == nil {
		return false
	}
	if request.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https")
}
