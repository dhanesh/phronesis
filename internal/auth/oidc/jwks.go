package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"time"
)

// jwk is a single JSON Web Key. We parse only what RS256 needs:
//
//	kty=RSA, kid, n (modulus, base64url), e (exponent, base64url),
//	optional alg=RS256.
//
// Other key types (oct, EC, OKP) are silently skipped during refresh
// — phronesis only supports RS256 for production OIDC verification.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	Kid string `json:"kid,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// jwksMaxBytes caps the JWKS response body to defend against a
// malicious or misbehaving IdP returning a multi-GB document.
const jwksMaxBytes = 256 * 1024

// jwksFetchTimeout is the per-refresh HTTP timeout. Lower than T8's
// 5s budget so the verifier returns control to its caller well within
// any request deadline.
const jwksFetchTimeout = 5 * time.Second

// Refresh fetches the JWKS URI and replaces the cache entries on
// success. Failed refreshes leave the existing entries in place so
// callers can fall back via outage-tolerant lookup (T8).
//
// On any error (network, non-2xx, parse, empty key set) the cache
// is unchanged and the error is returned. The caller is expected to
// log the error and decide whether to use stale entries.
//
// Concurrent calls are NOT serialised inside this function; callers
// that fan out should use a singleflight if they care about
// duplicate work. The cache's mutex protects the entries map but
// the HTTP fetch happens outside the lock.
//
// Satisfies: T8 (OIDC IdP timeout + cached JWKS), RT-5 (JWKS pass).
func (c *JWKSCache) Refresh(ctx context.Context, client *http.Client) error {
	if c.JWKSURI == "" {
		return errors.New("oidc: JWKSCache.JWKSURI is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.JWKSURI, nil)
	if err != nil {
		return fmt.Errorf("oidc: build JWKS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: fetch JWKS: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("oidc: JWKS returned HTTP %d: %s", res.StatusCode, body)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, jwksMaxBytes))
	if err != nil {
		return fmt.Errorf("oidc: read JWKS body: %w", err)
	}
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("oidc: parse JWKS JSON: %w", err)
	}
	fresh := make(map[string]jwksEntry, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		if k.Kid == "" {
			return errors.New("oidc: JWKS contains a key without kid")
		}
		// Validate parseability eagerly so a corrupt JWK fails the
		// whole refresh rather than poisoning a future verify.
		if _, err := parseJWKToRSA(k); err != nil {
			return fmt.Errorf("oidc: JWKS kid %q: %w", k.Kid, err)
		}
		marshalled, err := json.Marshal(k)
		if err != nil {
			return fmt.Errorf("oidc: re-marshal JWK %q: %w", k.Kid, err)
		}
		fresh[k.Kid] = jwksEntry{key: marshalled, alg: k.Alg}
	}
	if len(fresh) == 0 {
		return errors.New("oidc: JWKS contains no usable RSA keys")
	}
	c.mu.Lock()
	c.entries = fresh
	c.lastFetch = time.Now()
	c.mu.Unlock()
	return nil
}

// getRaw returns the entry for kid regardless of TTL expiry, plus the
// cache's lastFetch timestamp. Callers use this with a staleness
// policy (e.g. RS256Verifier.StaleWindow) when a fresh refresh has
// just failed and they want outage-tolerant fallback.
func (c *JWKSCache) getRaw(kid string) (entry jwksEntry, lastFetch time.Time, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, exists := c.entries[kid]
	if !exists {
		return jwksEntry{}, c.lastFetch, false
	}
	return e, c.lastFetch, true
}

// RS256Verifier verifies RS256-signed JWTs against keys held in a
// JWKSCache. On unknown kid (post-rotation), it attempts a single
// blocking refresh.
//
// Outage tolerance: when the IdP is unreachable, verification
// continues to succeed for entries already in the cache up to
// StaleWindow past the cache's last successful fetch. Past
// StaleWindow the verifier returns ErrBadSignature even if the kid
// is present — long outages must not keep stale keys live
// indefinitely.
//
// Satisfies: T8 (OIDC IdP timeout + cached JWKS for outage),
//
//	RT-5 (real RS256 verifier — was stub-only before G8),
//	GAP-16 (cache invalidation handles credential rotation).
type RS256Verifier struct {
	Cache       *JWKSCache
	Client      *http.Client  // nil = http.DefaultClient
	StaleWindow time.Duration // 0 = 24h
}

// Verify implements Verifier. Returns ErrUnsupportedAlg if
// alg != "RS256". Returns ErrBadSignature on signature mismatch,
// missing kid, or stale-past-window cache.
func (v *RS256Verifier) Verify(alg, kid string, signingInput, signature []byte) error {
	if alg != "RS256" {
		return ErrUnsupportedAlg
	}
	if v.Cache == nil {
		return errors.New("oidc: RS256Verifier.Cache is nil")
	}
	pub, err := v.publicKeyForKid(kid)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(signingInput)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
		return ErrBadSignature
	}
	return nil
}

func (v *RS256Verifier) publicKeyForKid(kid string) (*rsa.PublicKey, error) {
	staleWindow := v.StaleWindow
	if staleWindow == 0 {
		staleWindow = 24 * time.Hour
	}

	// Fast path: fresh entry (within TTL) for this kid.
	if key, _, ok := v.Cache.Get(kid); ok {
		return parseJWKBytesToRSA(key)
	}

	// Slow path: try a refresh. On success, look up again.
	ctx, cancel := context.WithTimeout(context.Background(), jwksFetchTimeout)
	defer cancel()
	if err := v.Cache.Refresh(ctx, v.Client); err != nil {
		// Outage tolerance: fall back to stale entries within window.
		entry, lastFetch, ok := v.Cache.getRaw(kid)
		if ok && time.Since(lastFetch) <= staleWindow {
			slog.Warn("JWKS refresh failed; falling back to stale cache",
				slog.String("kid", kid),
				slog.String("err", err.Error()),
				slog.Duration("staleness", time.Since(lastFetch)),
			)
			return parseJWKBytesToRSA(entry.key)
		}
		// Either no entry for kid, or staleness past window.
		slog.Error("JWKS refresh failed and no usable cache entry",
			slog.String("kid", kid),
			slog.String("err", err.Error()),
		)
		return nil, ErrBadSignature
	}

	// Refresh succeeded; entry should be present unless the IdP
	// rotated AWAY from this kid, in which case the JWT was signed
	// with a key the IdP no longer publishes — that's a real failure.
	if key, _, ok := v.Cache.Get(kid); ok {
		return parseJWKBytesToRSA(key)
	}
	return nil, ErrBadSignature
}

// parseJWKBytesToRSA decodes the marshalled-JWK bytes stored in the
// cache and constructs an *rsa.PublicKey. Wraps parseJWKToRSA for
// the bytes-stored variant.
func parseJWKBytesToRSA(b []byte) (*rsa.PublicKey, error) {
	var k jwk
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("oidc: cached JWK parse: %w", err)
	}
	return parseJWKToRSA(k)
}

func parseJWKToRSA(k jwk) (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("oidc: kid %q is not an RSA key (kty=%s)", k.Kid, k.Kty)
	}
	if k.Alg != "" && k.Alg != "RS256" {
		return nil, fmt.Errorf("oidc: kid %q advertises alg=%s; only RS256 supported", k.Kid, k.Alg)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("oidc: kid %q n base64url: %w", k.Kid, err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("oidc: kid %q e base64url: %w", k.Kid, err)
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, fmt.Errorf("oidc: kid %q has empty n or e", k.Kid)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() {
		return nil, fmt.Errorf("oidc: kid %q exponent does not fit int64", k.Kid)
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
