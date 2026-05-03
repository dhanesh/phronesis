package auth

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// Cache caches resolved Principals keyed by bearer-key prefix
// (phr_live_<12base32>). It's the "fast path" for auth — turning
// a per-request Argon2id verify (~43ms on M1) into a sync.RWMutex
// map lookup (~µs).
//
// Satisfies: RT-4 (event-driven cache invalidation + 30s TTL belt),
//
//	S5 (60s revocation propagation — Invalidate calls trigger
//	    sub-30s in practice; TTL belt covers any missed signal),
//	T4 (cached-path budget ≤5ms — single map read + TTL check),
//	TN4 (P10 prior action: invalidate proactively on revoke;
//	     P11 cushioning: TTL belt for missed signals).
//
// Concurrency: sync.RWMutex; Get is RLock, Put / Invalidate are
// Lock. At B3 scale (≤1000 keys), all entries fit in a single map
// with negligible memory cost (~200B per principal × 1000 = ~200KB).
//
// Lifecycle: no background goroutine. Expired entries stay in the
// map until overwritten by a fresh Put — Get's TTL check filters
// them out so callers never see a stale principal. Memory growth
// is bounded by B3.
type Cache struct {
	ttl time.Duration

	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	principal principal.Principal
	expiresAt time.Time
}

// NewCache constructs a Cache with the given TTL. Pass 30 * time.Second
// to match TN4's belt. Zero ttl → Get always returns ok=false (cache
// disabled).
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

// Get returns the cached Principal for prefix. Returns ok=false
// when the entry is missing OR has passed its TTL.
//
// The fast-path cost is one RLock + one map lookup + one time.Now()
// comparison — well under T4's 5ms cached-path budget.
func (c *Cache) Get(prefix string) (principal.Principal, bool) {
	if c == nil || c.ttl == 0 {
		return principal.Principal{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[prefix]
	if !ok {
		return principal.Principal{}, false
	}
	if time.Now().After(e.expiresAt) {
		return principal.Principal{}, false
	}
	return e.principal, true
}

// Put records a successful resolution. Subsequent Get calls within
// TTL return the principal without re-hitting SQLite + Argon2id.
func (c *Cache) Put(prefix string, p principal.Principal) {
	if c == nil || c.ttl == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[prefix] = cacheEntry{
		principal: p,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes the entry for prefix. Called by the revoke
// handler so the next request with that key fails closed (slow-
// path resolution sees revoked_at IS NOT NULL → ErrKeyRevoked).
//
// Safe on prefixes not in the cache — idempotent.
func (c *Cache) Invalidate(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, prefix)
}

// InvalidateByUser drops every cached prefix owned by userID. Used
// when an admin suspends a user — all their keys must stop
// resolving without waiting for the 30s TTL.
//
// Implementation: query api_keys for the user's prefixes, then
// Invalidate each. Cheap at B3 scale (typical user has 1-3 keys).
// SQL failure leaves the cache untouched and returns the error;
// the TTL belt still bounds propagation to ≤30s.
func (c *Cache) InvalidateByUser(ctx context.Context, db *sql.DB, userID int64) error {
	if c == nil || db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT key_prefix FROM api_keys WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var prefix string
		if err := rows.Scan(&prefix); err != nil {
			return err
		}
		c.Invalidate(prefix)
	}
	return rows.Err()
}

// Size returns the current number of cached entries (including any
// past their TTL — Get filters those, but they remain in the map
// until overwritten). Useful for /metrics + test assertions.
func (c *Cache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
