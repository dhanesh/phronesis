package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/sessions"
)

const CookieName = "phronesis_session"

// Manager owns password credentials and session lifecycle. Sessions are
// persisted via one of two backing stores:
//   - In-memory map (default) — lost on restart; suitable for single-binary
//     and single-admin deployments.
//   - Optional sessions.Store (INT-1 Wave-2 integration) — externalizable
//     for multi-replica future; when set, the in-memory map is not used.
//
// Satisfies: RT-4.1 (when Store is wired), S3 (secure session via HMAC token
// + hard expiry).
type Manager struct {
	username string
	password [32]byte

	// When store is non-nil, Manager persists sessions via the Store
	// interface. The in-memory map is only used as fallback.
	store sessions.Store

	mu          sync.RWMutex
	sessionsMap map[string]Session
}

type Session struct {
	Username  string
	ExpiresAt time.Time
}

// NewManager builds a Manager with an in-memory session store.
func NewManager(username, password string) *Manager {
	return &Manager{
		username:    username,
		password:    sha256.Sum256([]byte(password)),
		sessionsMap: make(map[string]Session),
	}
}

// WithStore configures m to persist sessions via the supplied Store and
// returns m for chaining. MUST be called before any requests are served;
// switching stores at runtime is not supported.
//
// When a store is set, the in-memory sessionsMap is no longer used; its
// backing memory is released to let the GC reclaim it.
//
// Satisfies: RT-4.1, INT-1
func (m *Manager) WithStore(store sessions.Store) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.sessionsMap = nil
	return m
}

func (m *Manager) Login(username, password string) (string, error) {
	if username != m.username || sha256.Sum256([]byte(password)) != m.password {
		return "", errors.New("invalid credentials")
	}

	token, err := randomToken()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(24 * time.Hour).UTC()

	if m.store != nil {
		// INT-1: persist to sessions.Store instead of the in-memory map.
		// PrincipalType = "user"; WorkspaceID is the default ("default")
		// since v1 is single-workspace (see Server.defaultWorkspaceID).
		err := m.store.Put(context.Background(), sessions.Session{
			ID:            token,
			UserID:        username,
			WorkspaceID:   "default",
			PrincipalType: "user",
			CreatedAt:     time.Now().UTC(),
			ExpiresAt:     expiresAt,
		})
		if err != nil {
			return "", err
		}
		return token, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsMap[token] = Session{
		Username:  username,
		ExpiresAt: expiresAt,
	}
	return token, nil
}

func (m *Manager) Logout(token string) {
	if m.store != nil {
		_ = m.store.Delete(context.Background(), token)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessionsMap, token)
}

func (m *Manager) Username(r *http.Request) (string, bool) {
	resolved, ok := m.Resolve(r)
	if !ok {
		return "", false
	}
	if resolved.UserID != "" {
		return resolved.UserID, true
	}
	return resolved.Username, true
}

// Resolved captures everything Manager knows about the current request's
// session. When the sessions.Store path is active, all Session fields
// (PrincipalType, WorkspaceID, Metadata) are populated. On the legacy
// in-memory path, only Username is set and callers fall back to defaults.
//
// Satisfies: INT-1, I1 review fix (OIDC sessions retain auth_method + claims
// across requests so principalFromRequest can build a correctly-typed
// principal instead of stamping everything as auth_method=password).
type Resolved struct {
	// UserID is populated on the store-backed path (matches sessions.Session.UserID).
	UserID string
	// Username is populated on the in-memory path (legacy Session.Username).
	Username string
	// PrincipalType is "user" or "service_account"; empty on the in-memory path.
	PrincipalType string
	// WorkspaceID the session is scoped to; empty on the in-memory path.
	WorkspaceID string
	// Metadata passes provider-specific extras (e.g., auth_method=oidc).
	Metadata map[string]string
}

// Resolve returns the full session context for r, or ok=false when the
// request is unauthenticated or expired.
func (m *Manager) Resolve(r *http.Request) (Resolved, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return Resolved{}, false
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return Resolved{}, false
	}

	if m.store != nil {
		s, err := m.store.Get(context.Background(), token)
		if err != nil {
			return Resolved{}, false
		}
		return Resolved{
			UserID:        s.UserID,
			PrincipalType: s.PrincipalType,
			WorkspaceID:   s.WorkspaceID,
			Metadata:      s.Metadata,
		}, true
	}

	m.mu.RLock()
	session, ok := m.sessionsMap[token]
	m.mu.RUnlock()
	if !ok || time.Now().After(session.ExpiresAt) {
		if ok {
			m.Logout(token)
		}
		return Resolved{}, false
	}
	return Resolved{Username: session.Username}, true
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
