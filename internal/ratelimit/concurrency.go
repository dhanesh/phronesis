package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// Semaphore tracks in-flight requests per key. Acquire returns false
// when the key is at or above max in-flight; the caller MUST NOT
// proceed in that case. Release is the matching unlock — pair it via
// defer so a panic in the inner handler still releases the slot.
//
// Concurrency: safe for concurrent use; nil receivers are safe and
// always permit (matches the disabled-limiter convention used by the
// sliding-window Limiter).
//
// Satisfies: S6 (per-key concurrency cap, default 5 in-flight).
type Semaphore struct {
	max int

	mu       sync.Mutex
	inFlight map[string]int
}

// NewSemaphore builds an empty semaphore. max <= 0 returns a permissive
// instance that never blocks — useful when the cap is disabled but
// the middleware is still mounted.
func NewSemaphore(max int) *Semaphore {
	return &Semaphore{
		max:      max,
		inFlight: make(map[string]int),
	}
}

// Acquire attempts to take one slot for key. Returns true on success.
// Callers MUST call Release exactly once per successful Acquire.
func (s *Semaphore) Acquire(key string) bool {
	if s == nil || s.max <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[key] >= s.max {
		return false
	}
	s.inFlight[key]++
	return true
}

// Release returns one slot to key. No-op when key has no in-flight
// count (defensive against double-release bugs).
func (s *Semaphore) Release(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.inFlight[key]; ok && n > 0 {
		s.inFlight[key] = n - 1
		if s.inFlight[key] == 0 {
			delete(s.inFlight, key)
		}
	}
}

// InFlight returns the current count for key. Exposed for
// observability + tests.
func (s *Semaphore) InFlight(key string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[key]
}

// PerKeyConcurrencyConfig tunes the concurrency-cap middleware.
type PerKeyConcurrencyConfig struct {
	// Semaphore is the shared in-flight tracker. Required.
	Semaphore *Semaphore

	// RetryAfter is the value emitted in the Retry-After header on
	// reject. Defaults to 1s — clients that hit the cap should
	// back off briefly, not idle for a full window.
	RetryAfter time.Duration

	// OnDeny is invoked when a request is rejected (429). Useful
	// for metrics / audit hooks. May be nil.
	OnDeny func(keyPrefix string, path string)
}

// PerKeyConcurrencyMiddleware enforces a per-bearer-key in-flight cap.
// Pairs with the sliding-window PerKeyMiddleware: the window bounds
// request *rate* over time, this bounds parallel *work* at any one
// instant. Together they close S6.
//
// Acquire happens AFTER attachPrincipal (so we have the bearer
// prefix) and BEFORE expensive handler work. Release is deferred so
// a panic inside the inner handler still returns the slot.
//
// Satisfies: S6 (per-key concurrency cap), RT-7 (the second half of
//
//	the per-key budget — sliding window + in-flight cap).
func PerKeyConcurrencyMiddleware(cfg PerKeyConcurrencyConfig, next http.Handler) http.Handler {
	if cfg.Semaphore == nil {
		panic("ratelimit: PerKeyConcurrencyMiddleware requires Config.Semaphore")
	}
	retryAfter := cfg.RetryAfter
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	retryAfterSecs := strconv.Itoa(int(retryAfter.Round(time.Second).Seconds()))
	if retryAfterSecs == "0" {
		retryAfterSecs = "1"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := principal.FromContext(r.Context())
		if err != nil || !p.IsServiceAccount() || p.ID == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !cfg.Semaphore.Acquire(p.ID) {
			if cfg.OnDeny != nil {
				cfg.OnDeny(p.ID, r.URL.Path)
			}
			w.Header().Set("Retry-After", retryAfterSecs)
			http.Error(w, "concurrency limit exceeded", http.StatusTooManyRequests)
			return
		}
		defer cfg.Semaphore.Release(p.ID)
		next.ServeHTTP(w, r)
	})
}
