package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Config tunes the HTTP middleware.
//
// Satisfies: RT-10, RT-10.2 (trusted-proxy allow-list)
type Config struct {
	// Limiter is the shared per-key bucket store. Required.
	Limiter *Limiter

	// PathPrefixes is the set of request paths this middleware gates. A
	// request whose URL.Path does not start with any configured prefix is
	// passed through untouched. Example: ["/api/login", "/api/auth/"].
	// Empty slice = apply to every request.
	PathPrefixes []string

	// TrustedProxies is a list of CIDR prefixes for reverse proxies whose
	// X-Forwarded-For headers we trust. When the direct peer is in this
	// set, the first entry of X-Forwarded-For becomes the rate-limit key.
	// When empty or the peer is not trusted, the direct peer address is
	// used. This prevents clients from spoofing their rate-limit key via
	// an arbitrary X-Forwarded-For header.
	//
	// Satisfies: RT-10.2
	TrustedProxies []netip.Prefix

	// Now is an injectable clock for deterministic testing. Defaults to
	// time.Now when nil.
	Now func() time.Time

	// OnDeny is invoked when a request is rejected (429). Useful for
	// metrics / audit hooks. May be nil.
	OnDeny func(key string, path string)
}

// Middleware returns an http.Handler that applies the limiter's Allow check
// before delegating to next. Denied requests receive HTTP 429.
//
// Satisfies: RT-10, RT-10.1, TN7 resolution
func Middleware(cfg Config, next http.Handler) http.Handler {
	if cfg.Limiter == nil {
		panic("ratelimit: Middleware requires Config.Limiter")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldGate(cfg.PathPrefixes, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		key := ResolveKey(r, cfg.TrustedProxies)
		if !cfg.Limiter.Allow(key, cfg.Now()) {
			if cfg.OnDeny != nil {
				cfg.OnDeny(key, r.URL.Path)
			}
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func shouldGate(prefixes []string, path string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// ResolveKey returns the rate-limit key (IP address as string) for r.
//
// When the direct peer is in trustedProxies, the leftmost entry of the
// X-Forwarded-For header is used; otherwise, the direct peer address. This
// prevents untrusted callers from spoofing a rate-limit key.
//
// If parsing fails for any reason, the raw RemoteAddr is used as a fallback
// so we never silently drop all rate-limiting.
//
// Satisfies: RT-10.2
func ResolveKey(r *http.Request, trustedProxies []netip.Prefix) string {
	peerIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peerIP = r.RemoteAddr
	}
	if len(trustedProxies) > 0 {
		if addr, err := netip.ParseAddr(peerIP); err == nil && peerInTrusted(addr, trustedProxies) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
				if first != "" {
					return first
				}
			}
		}
	}
	return peerIP
}

func peerInTrusted(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// MustParsePrefixes is a convenience helper for test code and static config.
// Panics on invalid CIDR input.
func MustParsePrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			panic("ratelimit: invalid CIDR " + c + ": " + err.Error())
		}
		out = append(out, p)
	}
	return out
}
