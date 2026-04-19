// Package ratelimit provides a per-key sliding-window rate limiter and an
// HTTP middleware that enforces it on configured path prefixes. It is the
// server-side backstop behind TN7's auth-endpoint rate-limit floor (RT-10):
// reverse proxy WAFs remain the primary defense, but the server refuses to
// let brute-force /login requests pass even when the reverse proxy is
// misconfigured or absent.
//
// Satisfies: RT-10, RT-10.1, S5 (server-side input defense), B1 (auth stays
// available under credential-stuffing pressure)
package ratelimit

import (
	"sync"
	"time"
)

// Limiter enforces at most Max events per Window per key, via a sliding
// window of timestamps.
//
// Satisfies: RT-10.1
//
// Concurrency: all methods are safe for concurrent use.
type Limiter struct {
	window time.Duration
	max    int

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	// times is the set of event timestamps currently inside the window,
	// sorted ascending. Trimmed lazily during Allow.
	times []time.Time
}

// NewLimiter builds a limiter with the given sliding-window size and per-key
// max events. window=0 or max<=0 returns a permissive limiter that never
// denies — useful when the feature is disabled but the middleware is still
// mounted.
func NewLimiter(window time.Duration, max int) *Limiter {
	return &Limiter{
		window:  window,
		max:     max,
		buckets: make(map[string]*bucket),
	}
}

// Allow records an event for key at time now and returns true iff the number
// of events for key within the window is ≤ Max.
//
// The last-event timestamp is always appended (so repeated calls eventually
// push older ones out of the window). Callers passing an allow=false result
// as "reject" should NOT double-count: this method has side effects.
//
// Satisfies: RT-10.1 (sliding window semantics)
func (l *Limiter) Allow(key string, now time.Time) bool {
	if l.window == 0 || l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		b = &bucket{}
		l.buckets[key] = b
	}
	cutoff := now.Add(-l.window)

	// Drop entries outside the window. Since entries are appended in
	// order, the prefix to drop is contiguous at the start.
	drop := 0
	for drop < len(b.times) && b.times[drop].Before(cutoff) {
		drop++
	}
	b.times = b.times[drop:]

	b.times = append(b.times, now)
	return len(b.times) <= l.max
}

// Cleanup removes buckets that have been empty (no events within the window)
// relative to olderThan. Use from a periodic goroutine to prevent unbounded
// growth when many unique keys appear briefly (e.g., bot sweeps).
func (l *Limiter) Cleanup(olderThan time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	removed := 0
	cutoff := olderThan.Add(-l.window)
	for k, b := range l.buckets {
		// Trim before checking.
		drop := 0
		for drop < len(b.times) && b.times[drop].Before(cutoff) {
			drop++
		}
		b.times = b.times[drop:]
		if len(b.times) == 0 {
			delete(l.buckets, k)
			removed++
		}
	}
	return removed
}

// Count returns the current bucket size for key (events within the window
// as of now). Exposed for observability / metrics.
func (l *Limiter) Count(key string, now time.Time) int {
	if l.window == 0 {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		return 0
	}
	cutoff := now.Add(-l.window)
	n := 0
	for _, t := range b.times {
		if !t.Before(cutoff) {
			n++
		}
	}
	return n
}
