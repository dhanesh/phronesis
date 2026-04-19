package oidc

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// helper: build a stub provider + adapter pair tied together.
func newStubAdapter(t *testing.T) (*StubProvider, *Adapter) {
	t.Helper()
	provider := NewStubProvider("https://idp.test", "collab-wiki", []byte("stub-secret-at-least-32-bytes-long!"))
	mapping := ClaimMapping{
		SchemaVersion:    CurrentSchemaVersion,
		UserIDClaim:      "sub",
		DisplayNameClaim: "name",
		GroupsClaim:      "groups",
		RoleBinding:      func(map[string]any) principal.Role { return principal.RoleEditor },
		WorkspaceBinding: func(map[string]any) string { return "ws-test" },
	}
	adapter, err := NewAdapter(Config{
		Issuer:       provider.Issuer,
		Audience:     provider.Audience,
		Verifier:     provider.Verifier(),
		ClaimMapping: mapping,
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return provider, adapter
}

// @constraint RT-11 RT-11.3 S4 TN5
// Happy-path: stub provider issues an id_token, adapter validates it, Principal comes back.
func TestStubProviderFullFlow(t *testing.T) {
	provider, adapter := newStubAdapter(t)

	tok, err := provider.Issue(Claims{Subject: "alice", Email: "alice@acme", Name: "Alice", Groups: []string{"eng"}, TTL: time.Hour})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	p, err := adapter.Authenticate(context.Background(), tok)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.Type != principal.TypeUser {
		t.Errorf("Type: got %s, want user", p.Type)
	}
	if p.ID != "alice" {
		t.Errorf("ID: got %s, want alice", p.ID)
	}
	if p.Role != principal.RoleEditor {
		t.Errorf("Role: got %s, want editor", p.Role)
	}
	if p.WorkspaceID != "ws-test" {
		t.Errorf("WorkspaceID: got %s, want ws-test", p.WorkspaceID)
	}
	if p.Claims["display_name"] != "Alice" {
		t.Errorf("display_name: got %q, want Alice", p.Claims["display_name"])
	}
	if p.Claims["oidc_schema_version"] != "1" {
		t.Errorf("schema_version claim missing or wrong")
	}
}

// @constraint RT-11 S4
// Tampered signature must be rejected.
func TestAuthenticateRejectsTamperedSignature(t *testing.T) {
	provider, adapter := newStubAdapter(t)
	tok, _ := provider.Issue(Claims{Subject: "alice", TTL: time.Hour})

	parts := strings.Split(tok, ".")
	tampered := parts[0] + "." + parts[1] + ".AAAA" // garbage signature
	_, err := adapter.Authenticate(context.Background(), tampered)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

// @constraint RT-11 S4
// Wrong issuer must be rejected.
func TestAuthenticateRejectsWrongIssuer(t *testing.T) {
	badProvider := NewStubProvider("https://other.idp", "collab-wiki", []byte("stub-secret-at-least-32-bytes-long!"))
	// Adapter expects https://idp.test; provider emits https://other.idp
	_, adapter := newStubAdapter(t)
	tok, _ := badProvider.Issue(Claims{Subject: "alice", TTL: time.Hour})
	_, err := adapter.Authenticate(context.Background(), tok)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken (iss mismatch), got %v", err)
	}
}

// @constraint RT-11 S4
// Wrong audience must be rejected.
func TestAuthenticateRejectsWrongAudience(t *testing.T) {
	mismatchedAudProvider := NewStubProvider("https://idp.test", "other-service", []byte("stub-secret-at-least-32-bytes-long!"))
	_, adapter := newStubAdapter(t)
	tok, _ := mismatchedAudProvider.Issue(Claims{Subject: "alice", TTL: time.Hour})
	// Tokens signed with a different secret would fail at signature; ensure
	// audience check fires. To isolate the aud path, we need matching signer.
	// Use the stub adapter's provider secret directly:
	// Recreate adapter with the new provider's claim (secret identical), and
	// check it falls out at aud validation.
	adapter2, err := NewAdapter(Config{
		Issuer:       "https://idp.test",
		Audience:     "collab-wiki", // adapter expects collab-wiki; provider issues other-service
		Verifier:     mismatchedAudProvider.Verifier(),
		ClaimMapping: ClaimMapping{
			SchemaVersion:    CurrentSchemaVersion,
			UserIDClaim:      "sub",
			RoleBinding:      func(map[string]any) principal.Role { return principal.RoleViewer },
			WorkspaceBinding: func(map[string]any) string { return "ws" },
		},
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	_, err = adapter2.Authenticate(context.Background(), tok)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken (aud mismatch), got %v", err)
	}
	_ = adapter // keep reference to silence unused warnings if refactored
}

// @constraint RT-11 S3 S4
// Expired token must be rejected regardless of valid signature.
func TestAuthenticateRejectsExpired(t *testing.T) {
	provider, adapter := newStubAdapter(t)
	// Token with negative TTL = already expired by issuance time.
	tok, err := provider.Issue(Claims{Subject: "alice", TTL: -1 * time.Hour})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	_, err = adapter.Authenticate(context.Background(), tok)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken (expired), got %v", err)
	}
}

// @constraint RT-11 S4
// Token with "alg: none" must be rejected regardless of other fields.
// This is a classic JWT anti-pattern we explicitly guard against.
func TestAuthenticateRejectsNoneAlg(t *testing.T) {
	_, adapter := newStubAdapter(t)
	// Manually craft a "none" alg token: header + payload + empty signature segment.
	// headerJSON = {"alg":"none","typ":"JWT"}
	header := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0"
	// payloadJSON with correct iss+aud+exp
	exp := time.Now().Add(time.Hour).Unix()
	payloadJSON := `{"iss":"https://idp.test","aud":"collab-wiki","sub":"alice","exp":` + itoa(exp) + `}`
	payload := base64RawURL([]byte(payloadJSON))
	tok := header + "." + payload + "." // empty signature

	_, err := adapter.Authenticate(context.Background(), tok)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken (alg=none), got %v", err)
	}
}

// @constraint RT-11.2 S8
// Claim-mapping schema versioning: a token validated under the wrong schema
// is rejected with ErrUnsupportedSchema, not silent user misattribution.
func TestClaimMappingRejectsWrongSchemaVersion(t *testing.T) {
	cm := ClaimMapping{
		SchemaVersion:    99, // unknown future version
		UserIDClaim:      "sub",
		RoleBinding:      func(map[string]any) principal.Role { return principal.RoleViewer },
		WorkspaceBinding: func(map[string]any) string { return "ws" },
	}
	_, err := cm.Apply(map[string]any{"sub": "alice"})
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Errorf("expected ErrUnsupportedSchema, got %v", err)
	}
}

// @constraint RT-11.2 S8
// Claim mapping must explicitly error when the configured UserID claim is
// missing from the token (rather than producing a principal with empty id).
func TestClaimMappingRejectsMissingUserIDClaim(t *testing.T) {
	cm := ClaimMapping{
		SchemaVersion:    CurrentSchemaVersion,
		UserIDClaim:      "email", // token does not include email
		RoleBinding:      func(map[string]any) principal.Role { return principal.RoleViewer },
		WorkspaceBinding: func(map[string]any) string { return "ws" },
	}
	_, err := cm.Apply(map[string]any{"sub": "alice" /* no email */})
	if !errors.Is(err, ErrMissingClaim) {
		t.Errorf("expected ErrMissingClaim, got %v", err)
	}
}

// @constraint RT-11.1 S4
// After RotateSecret, tokens signed with the OLD secret must be rejected.
// This models the JWKS rotation flow TN5 guarantees.
func TestRotateSecretRejectsOldTokens(t *testing.T) {
	provider, adapter := newStubAdapter(t)
	tok, _ := provider.Issue(Claims{Subject: "alice", TTL: time.Hour})

	// Rotate AFTER issuing.
	provider.RotateSecret([]byte("new-secret-also-at-least-32-bytes!"), "stub-2")
	// Rebuild adapter with rotated verifier (real deployments: JWKS cache
	// refreshes; here we just swap the Verifier).
	newAdapter, err := NewAdapter(Config{
		Issuer:       provider.Issuer,
		Audience:     provider.Audience,
		Verifier:     provider.Verifier(),
		ClaimMapping: adapter.cfg.ClaimMapping,
	})
	if err != nil {
		t.Fatalf("NewAdapter rotated: %v", err)
	}

	_, err = newAdapter.Authenticate(context.Background(), tok)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expected old token rejected after rotation, got %v", err)
	}

	// New token with new secret works.
	tok2, _ := provider.Issue(Claims{Subject: "alice", TTL: time.Hour})
	if _, err := newAdapter.Authenticate(context.Background(), tok2); err != nil {
		t.Errorf("new token after rotation should authenticate, got %v", err)
	}
}

// @constraint RT-11.1
// JWKSCache Set + Get round-trips; Purge resets freshness.
func TestJWKSCacheBehavior(t *testing.T) {
	c := NewJWKSCache("https://idp.test/.well-known/jwks.json", time.Minute)
	c.Set("k1", []byte("key-bytes"), "RS256")

	key, alg, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected hit after Set")
	}
	if string(key) != "key-bytes" || alg != "RS256" {
		t.Errorf("wrong value on hit")
	}

	// Unknown kid misses.
	_, _, ok = c.Get("unknown")
	if ok {
		t.Error("unknown kid should miss")
	}

	c.Purge()
	_, _, ok = c.Get("k1")
	if ok {
		t.Error("expected miss after Purge")
	}
}

// --- helpers ---

func itoa(x int64) string {
	return strconv.FormatInt(x, 10)
}

func base64RawURL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
