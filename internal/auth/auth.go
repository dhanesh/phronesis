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

// WithStore returns a copy of m that uses store for session persistence.
// Safe to call once during server wiring; do not change after requests
// start arriving.
//
// Satisfies: RT-4.1, INT-1
func (m *Manager) WithStore(store sessions.Store) *Manager {
	clone := *m
	clone.store = store
	return &clone
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
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}

	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return "", false
	}

	if m.store != nil {
		s, err := m.store.Get(context.Background(), token)
		if err != nil {
			// ErrNotFound OR expired (the Store treats them identically).
			return "", false
		}
		return s.UserID, true
	}

	m.mu.RLock()
	session, ok := m.sessionsMap[token]
	m.mu.RUnlock()
	if !ok || time.Now().After(session.ExpiresAt) {
		if ok {
			m.Logout(token)
		}
		return "", false
	}

	return session.Username, true
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
