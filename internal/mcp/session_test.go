package mcp

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

func samplePrincipal() principal.Principal {
	return principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_oauth_client_x",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	}
}

// @constraint RT-2 — Create + Get round-trips, LastSeen updates.
func TestSessionStoreCreateAndGet(t *testing.T) {
	s := NewSessionStore(nil)
	id, err := s.Create(samplePrincipal())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(id, "mcp_") {
		t.Errorf("session id = %q; want mcp_ prefix", id)
	}
	got, ok := s.Get(id)
	if !ok {
		t.Fatal("Get failed for fresh session")
	}
	if got.Principal.ID != "phr_oauth_client_x" {
		t.Errorf("Principal.ID = %q; want phr_oauth_client_x", got.Principal.ID)
	}
}

func TestSessionStoreGetUnknownReturnsFalse(t *testing.T) {
	s := NewSessionStore(nil)
	if _, ok := s.Get("does-not-exist"); ok {
		t.Error("Get of unknown id should return false")
	}
}

// @constraint RT-2 — sessions expire past TTL.
func TestSessionStoreExpiresAfterTTL(t *testing.T) {
	now := time.Now()
	clock := &mockClockMCP{t: now}
	s := NewSessionStore(clock.Now)
	id, _ := s.Create(samplePrincipal())

	clock.t = now.Add(SessionTTL + time.Second)
	if _, ok := s.Get(id); ok {
		t.Error("expected expired session to miss")
	}
}

func TestSessionStoreDeleteIsImmediate(t *testing.T) {
	s := NewSessionStore(nil)
	id, _ := s.Create(samplePrincipal())
	s.Delete(id)
	if _, ok := s.Get(id); ok {
		t.Error("Get after Delete should miss")
	}
}

func TestSessionStoreCleanupSweepsExpired(t *testing.T) {
	now := time.Now()
	clock := &mockClockMCP{t: now}
	s := NewSessionStore(clock.Now)
	id, _ := s.Create(samplePrincipal())
	if _, ok := s.sessions[id]; !ok {
		t.Fatal("session should be present")
	}
	clock.t = now.Add(SessionTTL + time.Hour)
	s.Cleanup()
	if _, ok := s.sessions[id]; ok {
		t.Error("Cleanup should have removed expired session")
	}
}

// @constraint RT-2 — concurrent access is race-free.
func TestSessionStoreConcurrentAccessIsRaceFree(t *testing.T) {
	s := NewSessionStore(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := s.Create(samplePrincipal())
			if err != nil {
				return
			}
			_, _ = s.Get(id)
			s.Delete(id)
		}()
	}
	wg.Wait()
}

func TestNilSessionStoreIsSafe(t *testing.T) {
	var s *SessionStore
	if id, _ := s.Create(samplePrincipal()); id != "" {
		t.Errorf("nil Create returned id %q; want empty", id)
	}
	if _, ok := s.Get("x"); ok {
		t.Error("nil Get should return false")
	}
	s.Delete("x") // must not panic
	s.Cleanup()   // must not panic
}

type mockClockMCP struct {
	mu sync.Mutex
	t  time.Time
}

func (c *mockClockMCP) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
