package sessions

import (
	"context"
	"sync"
	"time"
)

// MemStore is the in-process default Store. Sessions live only in the current
// process memory; they are lost on restart. Suitable for single-binary and
// single-replica k8s deployments (T8).
//
// Satisfies: RT-4.1, T8
//
// Wave 3+ may add a PostgresStore or BoltStore for durability; neither is
// required for v1. Tests use MemStore.
type MemStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewMemStore constructs an empty in-process session store.
func NewMemStore() *MemStore {
	return &MemStore{sessions: make(map[string]Session)}
}

func (m *MemStore) Get(ctx context.Context, id string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return Session{}, ErrNotFound
	}
	if !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(time.Now()) {
		return Session{}, ErrNotFound
	}
	return s, nil
}

func (m *MemStore) Put(ctx context.Context, s Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	return nil
}

func (m *MemStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

func (m *MemStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for id, s := range m.sessions {
		if !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			delete(m.sessions, id)
			removed++
		}
	}
	return removed, nil
}
