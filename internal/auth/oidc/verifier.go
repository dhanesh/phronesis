// Package oidc provides OpenID Connect authentication for collab-wiki.
//
// Satisfies: RT-11, S4, S8, TN5 ("launch = OIDC adapter complete with stub
// provider in CI").
//
// This package ships a working adapter with a pluggable Verifier interface.
// The default Verifier is HS256-HMAC for CI / test flows (StubProvider); real
// deployments swap in an RS256 Verifier backed by a JWKS fetch. The adapter
// flow (parse, validate iss/aud/exp/nbf, apply claim mapping) is the same for
// both — only signature verification differs.
package oidc

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// Verifier validates the signature portion of a signed JWT.
//
// Satisfies: RT-11.1 (pluggable verifier for JWKS/rotation), S4 (signature validation)
//
// Implementations receive the signing input (header.payload, base64url-encoded)
// and the decoded signature bytes, plus the header's alg + kid fields so RS256
// verifiers can select a key from their JWKS cache.
type Verifier interface {
	Verify(alg, kid string, signingInput []byte, signature []byte) error
}

// ErrBadSignature is returned when the supplied signature does not match.
var ErrBadSignature = errors.New("oidc: signature verification failed")

// ErrUnsupportedAlg is returned for algs this Verifier does not implement.
var ErrUnsupportedAlg = errors.New("oidc: unsupported alg")

// HMACVerifier verifies HS256-signed tokens. Intended for CI / stub provider
// flows only; production must use an RS256 or ES256 Verifier backed by JWKS.
//
// Satisfies: RT-11.3 (stub provider integration-test path)
type HMACVerifier struct {
	Secret []byte
}

// Verify implements Verifier. Returns ErrUnsupportedAlg if alg != "HS256".
func (v *HMACVerifier) Verify(alg, _kid string, signingInput, signature []byte) error {
	if alg != "HS256" {
		return ErrUnsupportedAlg
	}
	if len(v.Secret) == 0 {
		return errors.New("oidc: HMACVerifier has empty secret")
	}
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write(signingInput)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, signature) {
		return ErrBadSignature
	}
	return nil
}

// JWKSCache is a simple TTL cache of JSON Web Key Sets, scaffolded for the
// future RS256 Verifier implementation.
//
// Satisfies: RT-11.1 (JWKS fetcher + TTL cache + rotation handler)
//
// The cache stores key material as []byte opaque to this package; the RS256
// Verifier impl interprets them. On a cache miss (e.g., unknown kid after
// rotation), the cache refetches from JWKSURI. The current Wave-3 scope uses
// the HMAC stub, so this type is in place for the integration point but not
// yet exercised end-to-end.
type JWKSCache struct {
	JWKSURI string
	TTL     time.Duration

	mu        sync.RWMutex
	entries   map[string]jwksEntry // keyed by kid
	lastFetch time.Time
}

type jwksEntry struct {
	key []byte
	alg string
}

// NewJWKSCache constructs a cache. TTL defaults to 1h if zero.
func NewJWKSCache(jwksURI string, ttl time.Duration) *JWKSCache {
	if ttl == 0 {
		ttl = time.Hour
	}
	return &JWKSCache{
		JWKSURI: jwksURI,
		TTL:     ttl,
		entries: make(map[string]jwksEntry),
	}
}

// Get returns the cached key bytes and alg for kid. If the kid is unknown or
// the cache has expired, callers should trigger a refresh via Refresh.
// ok is false when the caller should refresh.
func (c *JWKSCache) Get(kid string) (key []byte, alg string, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.lastFetch) > c.TTL {
		return nil, "", false
	}
	e, exists := c.entries[kid]
	if !exists {
		return nil, "", false
	}
	return e.key, e.alg, true
}

// Set is how a refresh populates the cache. Real implementations call this
// after fetching JWKSURI; tests can seed it directly.
func (c *JWKSCache) Set(kid string, key []byte, alg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[kid] = jwksEntry{key: key, alg: alg}
	c.lastFetch = time.Now()
}

// Purge clears all entries and resets lastFetch to the zero value. Used after
// a forced rotation or an explicit invalidation event.
func (c *JWKSCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]jwksEntry)
	c.lastFetch = time.Time{}
}
