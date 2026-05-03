package auth

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

func samplePrincipal() principal.Principal {
	return principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_live_abcd1234efgh",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	}
}

// @constraint RT-4 — Get returns the cached principal within TTL,
// then ok=false past TTL. The 30s belt is the TN4 default; tests
// use a shorter TTL so they don't sleep for 30 seconds.
func TestCacheGetReturnsHitWithinTTL(t *testing.T) {
	c := NewCache(50 * time.Millisecond)
	p := samplePrincipal()

	c.Put(p.ID, p)

	got, ok := c.Get(p.ID)
	if !ok {
		t.Fatal("expected cache hit immediately after Put")
	}
	if got.ID != p.ID || got.Role != p.Role {
		t.Errorf("cached principal mismatch: %+v vs %+v", got, p)
	}
}

func TestCacheGetReturnsMissPastTTL(t *testing.T) {
	c := NewCache(20 * time.Millisecond)
	p := samplePrincipal()
	c.Put(p.ID, p)

	time.Sleep(30 * time.Millisecond)

	if _, ok := c.Get(p.ID); ok {
		t.Fatal("expected cache miss past TTL")
	}
}

// @constraint S5 / RT-4 — Invalidate removes the entry immediately;
// the next Get returns ok=false even if TTL hasn't elapsed.
func TestCacheInvalidateRemovesEntry(t *testing.T) {
	c := NewCache(time.Hour)
	p := samplePrincipal()
	c.Put(p.ID, p)

	c.Invalidate(p.ID)

	if _, ok := c.Get(p.ID); ok {
		t.Fatal("expected cache miss after Invalidate")
	}
}

func TestCacheInvalidateUnknownPrefixIsNoop(t *testing.T) {
	c := NewCache(time.Hour)
	c.Invalidate("phr_live_does_not_exist_in_cache")
	// Should not panic; should not affect anything else.
}

// @constraint S5 — InvalidateByUser drops every cached prefix
// owned by the suspended user.
func TestCacheInvalidateByUserDropsAllOfUserSKeys(t *testing.T) {
	c := NewCache(time.Hour)
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "cache-iu.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed two users + three keys (2 owned by user A, 1 by user B).
	res, _ := store.DB().Exec(`INSERT INTO users (oidc_sub, role, status) VALUES (?, 'user', 'active')`, "sub-A")
	uidA, _ := res.LastInsertId()
	res, _ = store.DB().Exec(`INSERT INTO users (oidc_sub, role, status) VALUES (?, 'user', 'active')`, "sub-B")
	uidB, _ := res.LastInsertId()

	for _, k := range []struct {
		uid    int64
		prefix string
	}{
		{uidA, "phr_live_a1"},
		{uidA, "phr_live_a2"},
		{uidB, "phr_live_b1"},
	} {
		_, err := store.DB().Exec(
			`INSERT INTO api_keys (user_id, workspace_slug, scope, label, key_prefix, key_hash)
			 VALUES (?, 'default', 'read', 'test', ?, ?)`,
			k.uid, k.prefix, []byte("dummy-hash"))
		if err != nil {
			t.Fatalf("seed key: %v", err)
		}
		c.Put(k.prefix, principal.Principal{
			Type: principal.TypeServiceAccount, ID: k.prefix,
		})
	}

	if c.Size() != 3 {
		t.Fatalf("expected 3 cached entries, got %d", c.Size())
	}

	// Invalidate user A — both A's keys must drop; B's must stay.
	if err := c.InvalidateByUser(context.Background(), store.DB(), uidA); err != nil {
		t.Fatalf("InvalidateByUser: %v", err)
	}

	if _, ok := c.Get("phr_live_a1"); ok {
		t.Error("a1 should be dropped")
	}
	if _, ok := c.Get("phr_live_a2"); ok {
		t.Error("a2 should be dropped")
	}
	if _, ok := c.Get("phr_live_b1"); !ok {
		t.Error("b1 should remain (different user)")
	}
}

// @constraint T4 — cached path is fast. This is a smoke check that
// 100k Get calls complete in well under 100ms (i.e. ≤1µs each
// average), so T4's 5ms cached-path budget has 4-orders-of-magnitude
// of headroom for application code on top of the cache lookup.
func TestCacheGetIsFastPath(t *testing.T) {
	c := NewCache(time.Hour)
	p := samplePrincipal()
	c.Put(p.ID, p)

	start := time.Now()
	for i := 0; i < 100_000; i++ {
		if _, ok := c.Get(p.ID); !ok {
			t.Fatal("unexpected miss")
		}
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("100k Get took %v; expected < 100ms (1µs avg)", elapsed)
	}
}

// @constraint RT-4 — Cache is safe for concurrent use; multiple
// goroutines can Get + Put + Invalidate without races.
func TestCacheConcurrentAccessIsRaceFree(t *testing.T) {
	c := NewCache(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prefix := "phr_live_" + samplePrefix(i)
			c.Put(prefix, principal.Principal{ID: prefix})
			_, _ = c.Get(prefix)
			c.Invalidate(prefix)
		}(i)
	}
	wg.Wait()
	// Run again with all 100 prefixes overlapping.
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.Put("phr_live_overlap", principal.Principal{ID: "x"})
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = c.Get("phr_live_overlap")
		}(i)
	}
	wg.Wait()
}

func samplePrefix(i int) string {
	const charset = "abcdefghijklmnop"
	out := make([]byte, 8)
	for j := 0; j < 8; j++ {
		out[j] = charset[(i+j)%len(charset)]
	}
	return string(out)
}

// @constraint RT-4 — nil Cache is safe for callers (they don't
// have to nil-check before every operation).
func TestCacheNilReceiverIsSafe(t *testing.T) {
	var c *Cache // nil
	if _, ok := c.Get("anything"); ok {
		t.Fatal("nil cache should always miss")
	}
	c.Put("anything", principal.Principal{}) // must not panic
	c.Invalidate("anything")                 // must not panic
	if c.Size() != 0 {
		t.Fatalf("nil cache Size should be 0, got %d", c.Size())
	}
}

// @constraint RT-4 — TTL=0 disables the cache (Get always misses).
func TestCacheZeroTTLDisables(t *testing.T) {
	c := NewCache(0)
	c.Put("phr_live_x", samplePrincipal())
	if _, ok := c.Get("phr_live_x"); ok {
		t.Fatal("zero-TTL cache should always miss")
	}
}
