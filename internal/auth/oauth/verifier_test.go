package oauth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/principal"
)

// signTestToken produces a JWT signed with the given signer carrying
// the supplied claims. Used by every verifier test below.
func signTestToken(t *testing.T, signer *oidc.RS256Signer, claims map[string]any) string {
	t.Helper()
	tok, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

// newTestVerifierKit produces a (signer, verifier) pair sharing a
// JWKSCache so signed tokens round-trip through the verifier.
func newTestVerifierKit(t *testing.T) (*oidc.RS256Signer, *Verifier) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	signer, err := oidc.NewRS256Signer(key, "test-key-1")
	if err != nil {
		t.Fatalf("NewRS256Signer: %v", err)
	}
	cache := oidc.NewJWKSCache("test", time.Hour)
	doc, err := signer.JWKSDocument()
	if err != nil {
		t.Fatalf("JWKSDocument: %v", err)
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(doc, &set); err != nil {
		t.Fatalf("unmarshal JWKS: %v", err)
	}
	cache.Set("test-key-1", set.Keys[0], "RS256")

	v := &Verifier{
		Cache:    cache,
		Inner:    &oidc.RS256Verifier{Cache: cache},
		Issuer:   "https://phronesis.test",
		Audience: "https://phronesis.test",
		Now:      time.Now,
	}
	return signer, v
}

func validClaims() map[string]any {
	return map[string]any{
		"iss":       "https://phronesis.test",
		"aud":       "https://phronesis.test",
		"sub":       "user-1",
		"client_id": "phr_oauth_client_x",
		"workspace": "default",
		"scope":     "read",
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(time.Hour).Unix(),
	}
}

// @constraint RT-2 — signed-with-our-key + valid claims = SA principal.
func TestVerifyAccessTokenGoldenPath(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	tok := signTestToken(t, signer, validClaims())

	p, err := v.VerifyAccessToken(tok)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if p.Type != principal.TypeServiceAccount {
		t.Errorf("Type = %v; want service_account", p.Type)
	}
	if p.ID != "phr_oauth_client_x" {
		t.Errorf("ID = %q; want client_id from claims", p.ID)
	}
	if p.WorkspaceID != "default" {
		t.Errorf("WorkspaceID = %q; want default", p.WorkspaceID)
	}
	if p.Role != principal.RoleViewer {
		t.Errorf("Role = %v; want viewer (scope=read)", p.Role)
	}
	if p.Claims["auth_method"] != "oauth_jwt" {
		t.Errorf("auth_method = %q; want oauth_jwt", p.Claims["auth_method"])
	}
}

// @constraint RT-2 — scope=write promotes to editor; scope=admin to
// admin. Anything else collapses to viewer.
func TestVerifyAccessTokenScopeToRole(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	for _, tc := range []struct {
		scope string
		want  principal.Role
	}{
		{"read", principal.RoleViewer},
		{"write", principal.RoleEditor},
		{"read write", principal.RoleEditor},
		{"admin", principal.RoleAdmin},
		{"read admin", principal.RoleAdmin},
		{"", principal.RoleViewer},
	} {
		claims := validClaims()
		claims["scope"] = tc.scope
		tok := signTestToken(t, signer, claims)
		p, err := v.VerifyAccessToken(tok)
		if err != nil {
			t.Fatalf("scope=%q: VerifyAccessToken: %v", tc.scope, err)
		}
		if p.Role != tc.want {
			t.Errorf("scope=%q: Role = %v; want %v", tc.scope, p.Role, tc.want)
		}
	}
}

// @constraint RT-2 — wrong issuer = ErrInvalidAccessToken (no detail
// leak).
func TestVerifyAccessTokenRejectsWrongIssuer(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	claims := validClaims()
	claims["iss"] = "https://attacker.example"
	tok := signTestToken(t, signer, claims)

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken", err)
	}
}

func TestVerifyAccessTokenRejectsWrongAudience(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	claims := validClaims()
	claims["aud"] = "https://other.example"
	tok := signTestToken(t, signer, claims)

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken", err)
	}
}

// @constraint RT-2 — exp < now MUST fail closed.
func TestVerifyAccessTokenRejectsExpired(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	claims := validClaims()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	tok := signTestToken(t, signer, claims)

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken", err)
	}
}

// @constraint RT-2 — alg=none MUST be rejected.
func TestVerifyAccessTokenRejectsAlgNone(t *testing.T) {
	_, v := newTestVerifierKit(t)
	// Hand-construct a JWT with alg=none (no real signer would emit one).
	// Tiny token: header={"alg":"none"}.payload={}.<empty sig>
	hdr := "eyJhbGciOiJub25lIn0"
	pld := "e30"
	tok := hdr + "." + pld + "."

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken", err)
	}
}

// @constraint RT-2 — missing client_id claim MUST be rejected (not
// silently mapped to ""; that would create an authenticated principal
// with no identity).
func TestVerifyAccessTokenRejectsMissingClientID(t *testing.T) {
	signer, v := newTestVerifierKit(t)
	claims := validClaims()
	delete(claims, "client_id")
	tok := signTestToken(t, signer, claims)

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken", err)
	}
}

func TestVerifyAccessTokenRejectsMalformed(t *testing.T) {
	_, v := newTestVerifierKit(t)
	for _, tok := range []string{
		"",
		"abc",
		"a.b",
		strings.Repeat("a", 1000),
	} {
		if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
			t.Errorf("token=%q: err = %v; want ErrInvalidAccessToken", tok, err)
		}
	}
}

// @constraint RT-2 — token signed with a DIFFERENT key MUST fail
// signature verification (defense-in-depth: even if a forged token
// reaches us, the public-key check rejects it).
func TestVerifyAccessTokenRejectsForeignSignature(t *testing.T) {
	_, v := newTestVerifierKit(t)
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherSigner, _ := oidc.NewRS256Signer(otherKey, "test-key-1") // same kid; different key
	tok := signTestToken(t, otherSigner, validClaims())

	if _, err := v.VerifyAccessToken(tok); !errors.Is(err, ErrInvalidAccessToken) {
		t.Errorf("err = %v; want ErrInvalidAccessToken (forged sig)", err)
	}
}

func TestVerifyAccessTokenNilReceiver(t *testing.T) {
	var v *Verifier
	if _, err := v.VerifyAccessToken("any.thing.here"); err == nil {
		t.Error("nil verifier should error, not panic")
	}
}
