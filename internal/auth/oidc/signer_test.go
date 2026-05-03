package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

// @constraint RT-2 — sign + verify round-trip through the existing
// RS256Verifier. This is the structural contract that lets phronesis
// act as both an OAuth issuer and the resource server validating its
// own tokens.
func TestRS256SignerRoundTripsThroughVerifier(t *testing.T) {
	key := newTestRSAKey(t)
	signer, err := NewRS256Signer(key, "phronesis-oauth-1")
	if err != nil {
		t.Fatalf("NewRS256Signer: %v", err)
	}
	claims := map[string]any{
		"iss":   "https://example.com",
		"sub":   "user-1",
		"aud":   "phronesis",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(15 * time.Minute).Unix(),
		"scope": "read",
	}
	tok, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected compact JWT (3 parts), got %d", len(parts))
	}

	cache := NewJWKSCache("https://example.com/.well-known/jwks.json", time.Hour)
	jwk, err := signer.JWKSDocument()
	if err != nil {
		t.Fatalf("JWKSDocument: %v", err)
	}
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(jwk, &doc); err != nil {
		t.Fatalf("unmarshal JWKS doc: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("expected 1 key in JWKS doc, got %d", len(doc.Keys))
	}
	cache.Set("phronesis-oauth-1", doc.Keys[0], "RS256")

	v := &RS256Verifier{Cache: cache}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if err := v.Verify("RS256", "phronesis-oauth-1", []byte(parts[0]+"."+parts[1]), sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// @constraint RT-2 — the JWT header always carries kid + alg=RS256;
// missing either of these would break clients that key-pin or that
// validate alg before fetching JWKS.
func TestRS256SignerHeaderShapeIsCompliant(t *testing.T) {
	signer, err := NewRS256Signer(newTestRSAKey(t), "k1")
	if err != nil {
		t.Fatalf("NewRS256Signer: %v", err)
	}
	tok, err := signer.Sign(map[string]any{"sub": "x", "exp": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if hdr.Alg != "RS256" {
		t.Errorf("alg = %q; want RS256", hdr.Alg)
	}
	if hdr.Typ != "JWT" {
		t.Errorf("typ = %q; want JWT", hdr.Typ)
	}
	if hdr.Kid != "k1" {
		t.Errorf("kid = %q; want k1", hdr.Kid)
	}
}

func TestNewRS256SignerRejectsZeroValues(t *testing.T) {
	if _, err := NewRS256Signer(nil, "k1"); err == nil {
		t.Error("expected error for nil key")
	}
	if _, err := NewRS256Signer(newTestRSAKey(t), ""); err == nil {
		t.Error("expected error for empty kid")
	}
}

func TestRS256SignerNilReceiverIsSafe(t *testing.T) {
	var s *RS256Signer
	if _, err := s.Sign(map[string]any{}); err == nil {
		t.Error("nil receiver should return error, not panic")
	}
}

// @constraint RT-2 — JWKSDocument is the public-key surface; it MUST
// be parseable by the existing JWKSCache so the same verifier code
// path validates self-issued tokens.
func TestSignerJWKSDocumentIsValidJWKSet(t *testing.T) {
	signer, _ := NewRS256Signer(newTestRSAKey(t), "k1")
	doc, err := signer.JWKSDocument()
	if err != nil {
		t.Fatalf("JWKSDocument: %v", err)
	}
	var set struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(doc, &set); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(set.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(set.Keys))
	}
	for _, k := range []string{"kty", "kid", "n", "e"} {
		if set.Keys[0][k] == "" {
			t.Errorf("missing required JWK field %q", k)
		}
	}
	if set.Keys[0]["alg"] != "RS256" {
		t.Errorf("alg = %q; want RS256", set.Keys[0]["alg"])
	}
}
