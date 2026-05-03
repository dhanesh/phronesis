package app

import (
	"context"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oauth"
	"github.com/dhanesh/phronesis/internal/mcp"
	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/ratelimit"
)

// @constraint INT-P1/P2/P3 — the janitor's sweep drops expired
// entries across all three store types in one pass.
func TestJanitorSweepCleansAllStores(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}

	// OAuth store — mint a code, advance past TTL, expect it gone.
	oauthStore := oauth.NewStore(clock.Now)
	verifier := "v"
	code, err := oauthStore.MintCode(oauth.AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})
	if err != nil {
		t.Fatalf("MintCode: %v", err)
	}

	// MCP session store — create a session, advance past TTL.
	sessionStore := mcp.NewSessionStore(clock.Now)
	sessionID, err := sessionStore.Create(principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_oauth_x",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	})
	if err != nil {
		t.Fatalf("session Create: %v", err)
	}

	// Ratelimit limiter — record events for a key, then advance past
	// the window so Cleanup drops the empty bucket.
	limiter := ratelimit.NewLimiter(time.Minute, 60)
	limiter.Allow("phr_oauth_x", clock.Now())
	if got := limiter.Count("phr_oauth_x", clock.Now()); got != 1 {
		t.Fatalf("limiter Count = %d; want 1", got)
	}

	j := newJanitor(time.Minute, oauthStore, sessionStore, limiter)
	j.now = clock.Now

	// Advance past every TTL: oauth.CodeTTL = 10min, mcp.SessionTTL =
	// 4h, ratelimit window = 1min. 5h is comfortably past all three.
	clock.t = now.Add(5 * time.Hour)

	j.sweep()

	if _, err := oauthStore.ConsumeCode(code.Code, "c1", "/cb", verifier); err == nil {
		t.Error("oauth code should have been swept")
	}
	if _, ok := sessionStore.Get(sessionID); ok {
		t.Error("MCP session should have been swept")
	}
	if got := limiter.Count("phr_oauth_x", clock.Now()); got != 0 {
		t.Errorf("limiter Count after sweep = %d; want 0", got)
	}
}

// @constraint INT-P1/P2/P3 — janitor with all-nil stores does not
// panic. Real deployments may legitimately have OAuth disabled, MCP
// disabled, or both.
func TestJanitorTolerantesNilStores(t *testing.T) {
	j := newJanitor(time.Minute, nil, nil)
	j.sweep() // must not panic
}

// @constraint INT-P1/P2/P3 — Start + Stop lifecycle is idempotent.
// Code that calls Start in a loop or Stop in error paths should not
// panic or deadlock.
func TestJanitorStartStopLifecycle(t *testing.T) {
	j := newJanitor(10*time.Millisecond, nil, nil)
	j.Start()
	j.Start() // second Start is a no-op

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := j.Stop(ctx); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if err := j.Stop(ctx); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

// @constraint INT-P1/P2/P3 — Stop is bounded by the supplied context.
// If the goroutine somehow hangs (it can't here, but the contract
// matters), Stop must return when the context expires.
func TestJanitorStopRespectsContextDeadline(t *testing.T) {
	j := newJanitor(time.Hour, nil, nil)
	j.Start()
	t.Cleanup(func() { _ = j.Stop(context.Background()) })

	// Don't actually hang anything — the Janitor's run() loop wakes
	// on either the stop channel or the ticker (1h here), so Stop
	// returns immediately on close(j.stop). Just confirm the context
	// path doesn't panic.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := j.Stop(ctx); err != nil {
		t.Errorf("Stop with deadline: %v", err)
	}
}

// @constraint INT-P1/P2/P3 — the periodic ticker actually fires the
// sweep at the configured interval.
func TestJanitorPeriodicTickFiresSweep(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}

	oauthStore := oauth.NewStore(clock.Now)
	verifier := "v"
	code, _ := oauthStore.MintCode(oauth.AuthorizationCode{
		ClientID: "c1", RedirectURI: "/cb",
		CodeChallenge: challengeFor(verifier), CodeChallengeMethod: "S256",
	})

	// Advance past TTL BEFORE starting the janitor — the ticker
	// fires every 10ms, so the next sweep should drop the code.
	clock.t = now.Add(time.Hour)

	j := newJanitor(10*time.Millisecond, oauthStore, nil)
	j.now = clock.Now
	j.Start()
	t.Cleanup(func() { _ = j.Stop(context.Background()) })

	// Wait up to 1s for the periodic sweep to run.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := oauthStore.ConsumeCode(code.Code, "c1", "/cb", verifier); err != nil {
			return // swept — success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("expected periodic tick to sweep expired code within 1s")
}

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }
