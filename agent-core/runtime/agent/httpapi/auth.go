package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultSessionTTL = 12 * time.Hour

type session struct {
	token     string
	username  string
	expiresAt time.Time
}

type sessionStore struct {
	mutex sync.Mutex
	items map[string]session
	ttl   time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		normalizedTTL = defaultSessionTTL
	}
	return &sessionStore{
		items: make(map[string]session),
		ttl:   normalizedTTL,
	}
}

func (store *sessionStore) create(now time.Time, username string) (session, error) {
	if store == nil {
		return session{}, fmt.Errorf("session store is nil")
	}
	token, err := generateSecureToken(32)
	if err != nil {
		return session{}, err
	}
	savedSession := session{
		token:     token,
		username:  username,
		expiresAt: now.Add(store.ttl),
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.deleteExpiredLocked(now)
	store.items[token] = savedSession
	return savedSession, nil
}

func (store *sessionStore) get(now time.Time, token string) (session, bool) {
	if store == nil {
		return session{}, false
	}
	normalizedToken := strings.TrimSpace(token)
	if normalizedToken == "" {
		return session{}, false
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.deleteExpiredLocked(now)
	savedSession, exists := store.items[normalizedToken]
	return savedSession, exists
}

func (store *sessionStore) delete(token string) {
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

func (store *sessionStore) deleteExpiredLocked(now time.Time) {
	for token, savedSession := range store.items {
		if !savedSession.expiresAt.After(now) {
			delete(store.items, token)
		}
	}
}

func validateCredentials(expectedUsername string, expectedPassword string, username string, password string) bool {
	normalizedExpectedUsername := strings.TrimSpace(expectedUsername)
	normalizedExpectedPassword := strings.TrimSpace(expectedPassword)
	normalizedUsername := strings.TrimSpace(username)
	if subtle.ConstantTimeCompare([]byte(normalizedExpectedUsername), []byte(normalizedUsername)) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(normalizedExpectedPassword), []byte(password)) == 1
}

func buildSessionCookie(name string, token string, path string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	}
}

func buildExpiredSessionCookie(name string, path string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
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
