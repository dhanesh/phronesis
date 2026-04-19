package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// @constraint RT-10.1
// Basic window behavior: N events in window are allowed; N+1th is denied.
func TestAllowEnforcesMax(t *testing.T) {
	l := NewLimiter(time.Minute, 3)
	now := time.Unix(1_700_000_000, 0)

	for i := 0; i < 3; i++ {
		if !l.Allow("ip-1", now) {
			t.Errorf("event %d denied, want allowed", i+1)
		}
	}
	if l.Allow("ip-1", now) {
		t.Error("4th event allowed, want denied")
	}
}

// @constraint RT-10.1
// Sliding window: events that fall outside the window no longer count.
func TestAllowSlidingWindow(t *testing.T) {
	l := NewLimiter(time.Second, 2)
	start := time.Unix(1_700_000_000, 0)

	// 2 events at t=0 fill the window.
	if !l.Allow("ip-1", start) {
		t.Fatal("event 1")
	}
	if !l.Allow("ip-1", start) {
		t.Fatal("event 2")
	}
	if l.Allow("ip-1", start) {
		t.Fatal("event 3 at t=0 should be denied")
	}

	// 2 seconds later, the original 2 events are outside the 1s window.
	later := start.Add(2 * time.Second)
	if !l.Allow("ip-1", later) {
		t.Error("post-slide event denied")
	}
}

// @constraint RT-10.1
// Keys are independent: one key hitting its cap does not affect another.
func TestAllowPerKeyIndependent(t *testing.T) {
	l := NewLimiter(time.Minute, 2)
	now := time.Unix(1_700_000_000, 0)

	_ = l.Allow("ip-1", now)
	_ = l.Allow("ip-1", now)
	if l.Allow("ip-1", now) {
		t.Error("ip-1 should be rate-limited")
	}
	if !l.Allow("ip-2", now) {
		t.Error("ip-2 should not be affected by ip-1")
	}
}

// @constraint RT-10.1
// A permissive limiter (window=0 or max<=0) always allows. Useful when the
// feature flag is off but the middleware is still mounted.
func TestPermissiveLimiter(t *testing.T) {
	now := time.Unix(1, 0)

	zero := NewLimiter(0, 10)
	if !zero.Allow("x", now) {
		t.Error("window=0 should always allow")
	}

	neg := NewLimiter(time.Minute, 0)
	if !neg.Allow("x", now) {
		t.Error("max=0 should always allow")
	}
}

// @constraint RT-10.1
// Cleanup removes buckets whose events have all aged out of the window.
func TestCleanupRemovesStaleBuckets(t *testing.T) {
	l := NewLimiter(time.Second, 10)
	t0 := time.Unix(1_700_000_000, 0)

	_ = l.Allow("ip-a", t0)
	_ = l.Allow("ip-b", t0)
	_ = l.Allow("ip-c", t0)

	// Cleanup with a "now" far after the window -> all stale.
	removed := l.Cleanup(t0.Add(time.Hour))
	if removed != 3 {
		t.Errorf("removed: got %d, want 3", removed)
	}
}

// @constraint RT-10.1
// Count reports current bucket size within the window.
func TestCount(t *testing.T) {
	l := NewLimiter(time.Minute, 5)
	now := time.Unix(1_700_000_000, 0)

	for i := 0; i < 4; i++ {
		_ = l.Allow("x", now)
	}
	if n := l.Count("x", now); n != 4 {
		t.Errorf("Count: got %d, want 4", n)
	}
	if n := l.Count("unknown", now); n != 0 {
		t.Errorf("Count unknown: got %d, want 0", n)
	}
}

// @constraint RT-10.1
// Concurrent Allow calls must be safe.
func TestAllowConcurrent(t *testing.T) {
	l := NewLimiter(time.Minute, 1_000_000) // effectively unlimited
	now := time.Unix(1_700_000_000, 0)

	const workers = 50
	const perWorker = 100

	var wg sync.WaitGroup
	var allowed atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				if l.Allow("shared", now) {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != workers*perWorker {
		t.Errorf("allowed count: got %d, want %d", got, workers*perWorker)
	}
}
