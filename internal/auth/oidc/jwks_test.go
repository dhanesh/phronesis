package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rsaFixture builds an RSA-2048 key pair plus its base64url-encoded
// JWK fields. Tests use the private key to sign tokens and the JWK
// fields to populate a fixture JWKS endpoint.
type rsaFixture struct {
	priv *rsa.PrivateKey
	kid  string
	jwkN string // base64url-encoded modulus
	jwkE string // base64url-encoded exponent
}

func newRSAFixture(t *testing.T, kid string) rsaFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	n := base64.RawURLEncoding.EncodeToString(priv.N.Bytes())
	// exponent: e=65537 = 0x010001 → 3 bytes
	eBytes := []byte{0x01, 0x00, 0x01}
	if priv.E != 65537 {
		t.Fatalf("expected e=65537, got %d", priv.E)
	}
	return rsaFixture{
		priv: priv,
		kid:  kid,
		jwkN: n,
		jwkE: base64.RawURLEncoding.EncodeToString(eBytes),
	}
}

// signRS256 returns a JWT in the form `<headerB64>.<payloadB64>.<sigB64>`
// signed with the fixture's private key.
func (f *rsaFixture) signRS256(t *testing.T, payload string) []byte {
	t.Helper()
	header := `{"alg":"RS256","typ":"JWT","kid":"` + f.kid + `"}`
	hb := base64.RawURLEncoding.EncodeToString([]byte(header))
	pb := base64.RawURLEncoding.EncodeToString([]byte(payload))
	signingInput := hb + "." + pb
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.priv, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sb := base64.RawURLEncoding.EncodeToString(sig)
	return []byte(signingInput + "." + sb)
}

// serveJWKS spins up an httptest server returning a JWKS containing
// the given fixtures. behavior controls failure modes.
type jwksServerOpts struct {
	fail5xx     int32 // when set, every request returns 503 until cleared
	requestHits *int32
}

func serveJWKS(t *testing.T, fixtures []rsaFixture, opts *jwksServerOpts) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts != nil && opts.requestHits != nil {
			atomic.AddInt32(opts.requestHits, 1)
		}
		if opts != nil && atomic.LoadInt32(&opts.fail5xx) > 0 {
			http.Error(w, "simulated IdP outage", http.StatusServiceUnavailable)
			return
		}
		keys := make([]jwk, 0, len(fixtures))
		for _, f := range fixtures {
			keys = append(keys, jwk{
				Kty: "RSA",
				Use: "sig",
				Alg: "RS256",
				Kid: f.kid,
				N:   f.jwkN,
				E:   f.jwkE,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwkSet{Keys: keys})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// @constraint T8 — cached JWKS verify works on the happy path.
// Satisfies RT-5 (JWKS pass — closes G8).
func TestRS256VerifierHappyPath(t *testing.T) {
	fix := newRSAFixture(t, "key-1")
	srv := serveJWKS(t, []rsaFixture{fix}, nil)

	cache := NewJWKSCache(srv.URL, time.Hour)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	v := &RS256Verifier{Cache: cache}
	token := fix.signRS256(t, `{"sub":"alice","aud":"phronesis","iss":"`+srv.URL+`"}`)
	parts := strings.SplitN(string(token), ".", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if err := v.Verify("RS256", "key-1", signingInput, sig); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

// @constraint T8 — IdP outage tolerance: cached entries continue to
// verify when the JWKS endpoint returns 5xx, up to StaleWindow.
func TestRS256VerifierOutageToleranceWithinStaleWindow(t *testing.T) {
	fix := newRSAFixture(t, "key-1")
	hits := int32(0)
	opts := &jwksServerOpts{requestHits: &hits}
	srv := serveJWKS(t, []rsaFixture{fix}, opts)

	cache := NewJWKSCache(srv.URL, 10*time.Millisecond) // short TTL forces a refresh attempt
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}

	// Wait past TTL so subsequent verify triggers Refresh.
	time.Sleep(20 * time.Millisecond)

	// Now simulate IdP outage. Cache.Get will miss (TTL expired);
	// Refresh will fail; verifier should fall back to stale entries.
	atomic.StoreInt32(&opts.fail5xx, 1)

	v := &RS256Verifier{Cache: cache, StaleWindow: 24 * time.Hour}
	token := fix.signRS256(t, `{"sub":"alice"}`)
	parts := strings.SplitN(string(token), ".", 3)
	signingInput := []byte(parts[0] + "." + parts[1])
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])

	if err := v.Verify("RS256", "key-1", signingInput, sig); err != nil {
		t.Fatalf("expected verify to succeed via stale fallback, got: %v", err)
	}
}

// @constraint T8 — past StaleWindow, verifier hard-fails even with
// the kid present. Long outages must not silently authorise forever.
func TestRS256VerifierStaleWindowExceededHardFails(t *testing.T) {
	fix := newRSAFixture(t, "key-1")
	opts := &jwksServerOpts{}
	srv := serveJWKS(t, []rsaFixture{fix}, opts)

	cache := NewJWKSCache(srv.URL, time.Millisecond)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	atomic.StoreInt32(&opts.fail5xx, 1)
	// StaleWindow is 1ms; sleep past it so even cached entry is past
	// the window when the verifier tries to fall back.
	time.Sleep(50 * time.Millisecond)

	v := &RS256Verifier{Cache: cache, StaleWindow: time.Millisecond}
	token := fix.signRS256(t, `{"sub":"alice"}`)
	parts := strings.SplitN(string(token), ".", 3)
	signingInput := []byte(parts[0] + "." + parts[1])
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])

	err := v.Verify("RS256", "key-1", signingInput, sig)
	if err == nil {
		t.Fatal("expected ErrBadSignature past stale window, got nil")
	}
}

// @constraint GAP-16 — credential rotation: new kid triggers a
// refresh and verification succeeds against the rotated key set.
func TestRS256VerifierHandlesKeyRotation(t *testing.T) {
	old := newRSAFixture(t, "old")
	rotated := newRSAFixture(t, "new")

	// Server serves only the OLD key initially; we'll mutate fixtures
	// via a closure-controlled handler.
	current := []rsaFixture{old}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		keys := make([]jwk, 0, len(current))
		for _, f := range current {
			keys = append(keys, jwk{Kty: "RSA", Alg: "RS256", Kid: f.kid, N: f.jwkN, E: f.jwkE})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwkSet{Keys: keys})
	}))
	t.Cleanup(srv.Close)

	cache := NewJWKSCache(srv.URL, time.Hour)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	v := &RS256Verifier{Cache: cache}

	// Verify against OLD works.
	tok := old.signRS256(t, `{"sub":"alice"}`)
	parts := strings.SplitN(string(tok), ".", 3)
	signingInput := []byte(parts[0] + "." + parts[1])
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if err := v.Verify("RS256", "old", signingInput, sig); err != nil {
		t.Fatalf("verify old: %v", err)
	}

	// IdP rotates: now serves the NEW key. Without a refresh, the
	// verifier doesn't know the new kid yet — it should refresh and
	// then verify successfully.
	current = []rsaFixture{rotated}
	tok = rotated.signRS256(t, `{"sub":"alice"}`)
	parts = strings.SplitN(string(tok), ".", 3)
	signingInput = []byte(parts[0] + "." + parts[1])
	sig, _ = base64.RawURLEncoding.DecodeString(parts[2])
	if err := v.Verify("RS256", "new", signingInput, sig); err != nil {
		t.Fatalf("verify new (post-rotation): %v", err)
	}
}

// @constraint S3 — alg confusion attack: a token with alg=HS256 must
// NOT be accepted by the RS256 verifier.
func TestRS256VerifierRejectsAlgConfusion(t *testing.T) {
	fix := newRSAFixture(t, "key-1")
	srv := serveJWKS(t, []rsaFixture{fix}, nil)
	cache := NewJWKSCache(srv.URL, time.Hour)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	v := &RS256Verifier{Cache: cache}
	if err := v.Verify("HS256", "key-1", []byte("x"), []byte("y")); err != ErrUnsupportedAlg {
		t.Fatalf("expected ErrUnsupportedAlg, got %v", err)
	}
}

func TestRS256VerifierUnknownKidWithFreshCacheFailsClosed(t *testing.T) {
	fix := newRSAFixture(t, "key-1")
	srv := serveJWKS(t, []rsaFixture{fix}, nil)
	cache := NewJWKSCache(srv.URL, time.Hour)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	v := &RS256Verifier{Cache: cache}
	err := v.Verify("RS256", "no-such-kid", []byte("x"), []byte("y"))
	if err == nil {
		t.Fatal("expected error for unknown kid")
	}
}

func TestJWKSRefreshRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	cache := NewJWKSCache(srv.URL, time.Hour)
	err := cache.Refresh(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
}

func TestJWKSRefreshRejectsCorruptJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not-json"))
	}))
	t.Cleanup(srv.Close)
	cache := NewJWKSCache(srv.URL, time.Hour)
	err := cache.Refresh(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on bad JSON")
	}
}

func TestJWKSRefreshRejectsKeysetWithoutKid(t *testing.T) {
	fix := newRSAFixture(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwkSet{Keys: []jwk{{
			Kty: "RSA", Alg: "RS256", N: fix.jwkN, E: fix.jwkE,
		}}})
	}))
	t.Cleanup(srv.Close)
	cache := NewJWKSCache(srv.URL, time.Hour)
	err := cache.Refresh(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on missing kid")
	}
}

func TestJWKSRefreshSkipsNonRSAKeys(t *testing.T) {
	fix := newRSAFixture(t, "rsa-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwkSet{Keys: []jwk{
			{Kty: "EC", Kid: "ec-1"},    // skipped
			{Kty: "oct", Kid: "hmac-1"}, // skipped
			{Kty: "RSA", Alg: "RS256", Kid: "rsa-1", N: fix.jwkN, E: fix.jwkE},
		}})
	}))
	t.Cleanup(srv.Close)
	cache := NewJWKSCache(srv.URL, time.Hour)
	if err := cache.Refresh(context.Background(), nil); err != nil {
		t.Fatalf("Refresh failed despite valid RSA key present: %v", err)
	}
	if _, _, ok := cache.Get("rsa-1"); !ok {
		t.Fatal("rsa-1 should be cached")
	}
	if _, _, ok := cache.Get("ec-1"); ok {
		t.Fatal("ec-1 should NOT be cached")
	}
}

func TestJWKSRefreshRejectsEmptyKeySet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwkSet{Keys: []jwk{}})
	}))
	t.Cleanup(srv.Close)
	cache := NewJWKSCache(srv.URL, time.Hour)
	err := cache.Refresh(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on empty key set")
	}
}
