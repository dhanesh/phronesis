package app

import (
	"context"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oauth"
	"github.com/dhanesh/phronesis/internal/mcp"
	"github.com/dhanesh/phronesis/internal/ratelimit"
)

// JanitorInterval is the period between Cleanup sweeps. 5 minutes is
// short enough that abandoned 10-minute OAuth codes spend at most one
// extra interval in the map, and long enough that the sweep is
// effectively free at production traffic levels.
const JanitorInterval = 5 * time.Minute

// janitor drives periodic Cleanup() on stores that bound their own
// memory via TTL-on-lookup but don't otherwise reclaim entries. A
// single goroutine ticks every backing store so the production server
// doesn't need a per-store goroutine. Closes m6-integrate INT-P1
// (oauth.Store), INT-P2 (mcp.SessionStore), and INT-P3 (ratelimit
// per-key buckets) in one shared scheduler.
//
// Lifecycle:
//
//	newJanitor(...) -> Start()  -> Stop(ctx)
//
// Stop is idempotent and bounded by ctx.
type janitor struct {
	interval time.Duration
	oauth    *oauth.Store
	sessions *mcp.SessionStore
	limiters []*ratelimit.Limiter
	now      func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stop      chan struct{}
	done      chan struct{}
}

// newJanitor builds a janitor. Any nil store is silently skipped on
// each tick, so callers can pass partial configurations (e.g.
// when OAuth is disabled).
func newJanitor(interval time.Duration, oauthStore *oauth.Store, sessions *mcp.SessionStore, limiters ...*ratelimit.Limiter) *janitor {
	if interval <= 0 {
		interval = JanitorInterval
	}
	return &janitor{
		interval: interval,
		oauth:    oauthStore,
		sessions: sessions,
		limiters: limiters,
		now:      time.Now,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start spawns the ticker goroutine. Idempotent — second and later
// Start calls are no-ops, so wiring code can call Start unconditionally
// without tracking startup state.
func (j *janitor) Start() {
	j.startOnce.Do(func() {
		go j.run()
	})
}

func (j *janitor) run() {
	defer close(j.done)
	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		select {
		case <-j.stop:
			return
		case <-t.C:
			j.sweep()
		}
	}
}

// sweep runs one round of Cleanup across every configured store.
// Public-but-unexported so tests can drive a deterministic sweep
// without waiting for a tick.
func (j *janitor) sweep() {
	if j.oauth != nil {
		j.oauth.Cleanup()
	}
	if j.sessions != nil {
		j.sessions.Cleanup()
	}
	now := j.now()
	for _, l := range j.limiters {
		if l != nil {
			l.Cleanup(now)
		}
	}
}

// Stop signals the janitor goroutine to exit and waits up to ctx for
// it to do so. Idempotent. Returns ctx.Err() if the deadline elapses
// before the goroutine returns; nil otherwise.
func (j *janitor) Stop(ctx context.Context) error {
	j.stopOnce.Do(func() { close(j.stop) })
	if ctx == nil {
		<-j.done
		return nil
	}
	select {
	case <-j.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
