package ratelimit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// PerKeyConfig tunes the per-bearer-key rate-limit middleware.
//
// Satisfies: RT-7, S6 (per-key sliding window). Concurrency cap is deferred.
type PerKeyConfig struct {
	// Limiter is the shared per-key bucket store. Required.
	Limiter *Limiter

	// Now is an injectable clock for deterministic testing. Defaults to
	// time.Now when nil.
	Now func() time.Time

	// OnDeny is invoked when a request is rejected (429). Useful for
	// metrics / audit hooks. May be nil.
	OnDeny func(keyPrefix string, path string)
}

// PerKeyMiddleware applies the limiter's Allow check keyed on the bearer-key
// prefix carried by the request's Principal. Requests without a service-
// account principal pass through untouched: cookie/OIDC user sessions ride
// the existing per-IP auth-endpoint floor (RT-10) and do not need per-key
// limiting because the same human can't have many bearer prefixes flying.
//
// Denied requests receive HTTP 429 with a Retry-After hint matching the
// limiter's window.
//
// Satisfies: RT-7, S6
func PerKeyMiddleware(cfg PerKeyConfig, next http.Handler) http.Handler {
	if cfg.Limiter == nil {
		panic("ratelimit: PerKeyMiddleware requires Config.Limiter")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	retryAfter := retryAfterSeconds(cfg.Limiter.window)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := principal.FromContext(r.Context())
		if err != nil || !p.IsServiceAccount() || p.ID == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !cfg.Limiter.Allow(p.ID, cfg.Now()) {
			if cfg.OnDeny != nil {
				cfg.OnDeny(p.ID, r.URL.Path)
			}
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func retryAfterSeconds(window time.Duration) string {
	secs := int(window.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
