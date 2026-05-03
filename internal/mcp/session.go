package mcp

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// SessionTTL is how long an MCP session stays alive without activity.
// 4 hours is the pragmatic floor for AI-agent clients that may issue
// requests in bursts separated by long idle periods. Sessions are
// in-memory; a server restart drops them and clients re-handshake.
const SessionTTL = 4 * time.Hour

// Session is the per-client state created by `initialize` and
// referenced via the `Mcp-Session-Id` header on subsequent requests.
//
// Holds the resolved Principal so each MCP request doesn't re-derive
// it (small win) AND so the session anchors auth state across the
// JSON-RPC frames within one logical conversation.
type Session struct {
	ID        string
	Principal principal.Principal
	CreatedAt time.Time
	LastSeen  time.Time
}

// SessionStore is an in-memory map of session id -> Session.
//
// Concurrency: all methods are safe for concurrent use.
//
// Nil receivers are safe — every method short-circuits to a zero
// result.
//
// Satisfies: RT-2 (Mcp-Session-Id header semantics).
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	now      func() time.Time
}

// NewSessionStore builds an empty store. now defaults to time.Now
// when nil.
func NewSessionStore(now func() time.Time) *SessionStore {
	if now == nil {
		now = time.Now
	}
	return &SessionStore{
		sessions: make(map[string]Session),
		now:      now,
	}
}

// Create assigns a fresh session id and persists the session. Returns
// the new id.
func (s *SessionStore) Create(p principal.Principal) (string, error) {
	if s == nil {
		return "", nil
	}
	id, err := randomSessionID()
	if err != nil {
		return "", err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = Session{
		ID:        id,
		Principal: p,
		CreatedAt: now,
		LastSeen:  now,
	}
	return id, nil
}

// Get returns the session for id and bumps LastSeen. Returns
// (Session{}, false) for unknown or expired sessions.
func (s *SessionStore) Get(id string) (Session, bool) {
	if s == nil || id == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	now := s.now()
	if now.Sub(sess.LastSeen) > SessionTTL {
		delete(s.sessions, id)
		return Session{}, false
	}
	sess.LastSeen = now
	s.sessions[id] = sess
	return sess, true
}

// Delete removes the session for id. No-op when id is unknown.
func (s *SessionStore) Delete(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Cleanup drops sessions whose LastSeen has aged past SessionTTL.
// Intended for periodic invocation.
func (s *SessionStore) Cleanup() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, sess := range s.sessions {
		if now.Sub(sess.LastSeen) > SessionTTL {
			delete(s.sessions, id)
		}
	}
}

func randomSessionID() (string, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return "mcp_" + strings.ToLower(enc), nil
}
