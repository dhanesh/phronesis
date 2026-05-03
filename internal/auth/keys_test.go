package auth

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// newKeysTestStore opens a fresh SQLite store with the
// user-mgmt-mcp schema applied AND seeds a single user owning the
// keys minted by the test. Returns the store + user id.
func newKeysTestStore(t *testing.T) (*sqlite.Store, int64) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	res, err := store.DB().Exec(
		`INSERT INTO users (oidc_sub, email, display_name, role, status) VALUES (?, ?, ?, ?, ?)`,
		"sub-keys", "k@example.com", "Keys Owner", "user", "active")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	uid, _ := res.LastInsertId()
	return store, uid
}

// @constraint S1 — plaintext shown ONCE; only Argon2id hash persists.
// Satisfies RT-3 + TN7 (key minting end-to-end).
func TestMintKeyEmitsPlaintextOnceAndStoresOnlyHash(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	row, err := MintKey(ctx, store.DB(), uid, "default", "write", "claude-code", nil)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if row.Plaintext == "" {
		t.Fatal("Plaintext must be returned at creation time")
	}
	if !strings.HasPrefix(row.Plaintext, "phr_live_") {
		t.Errorf("Plaintext should have phr_live_ prefix: %q", row.Plaintext)
	}
	if !strings.HasPrefix(row.Plaintext, row.Prefix+"_") {
		t.Errorf("Plaintext (%q) must start with Prefix+_ (%q)", row.Plaintext, row.Prefix+"_")
	}

	// Read back: key_hash must be present, but plaintext should not
	// be recoverable from the row.
	var hash []byte
	var prefix, scope string
	err = store.DB().QueryRow(
		`SELECT key_prefix, scope, key_hash FROM api_keys WHERE id = ?`, row.ID,
	).Scan(&prefix, &scope, &hash)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if prefix != row.Prefix {
		t.Errorf("prefix mismatch: %q vs %q", prefix, row.Prefix)
	}
	if scope != "write" {
		t.Errorf("scope mismatch: %q", scope)
	}
	if len(hash) == 0 {
		t.Fatal("key_hash is empty in DB")
	}
	// Defence-in-depth: the stored hash MUST NOT contain the
	// plaintext substring. (Argon2id is one-way, so this should
	// never happen, but it catches an accidental "store plaintext"
	// regression at trivial cost.)
	if strings.Contains(string(hash), row.Plaintext) {
		t.Fatal("stored hash contains plaintext substring — S1 violated")
	}
}

// @constraint RT-3 — bearer key resolves to a workspace-pinned
// service-account principal with the right scope tier.
func TestResolveBearerKeyReturnsCorrectPrincipal(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	for _, tc := range []struct {
		scope string
		role  principal.Role
	}{
		{"read", principal.RoleViewer},
		{"write", principal.RoleEditor},
		{"admin", principal.RoleAdmin},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			row, err := MintKey(ctx, store.DB(), uid, "default", tc.scope, "test-"+tc.scope, nil)
			if err != nil {
				t.Fatalf("MintKey: %v", err)
			}
			p, err := ResolveBearerKey(ctx, store.DB(), row.Plaintext)
			if err != nil {
				t.Fatalf("ResolveBearerKey: %v", err)
			}
			if p.Type != principal.TypeServiceAccount {
				t.Errorf("expected service_account type, got %q", p.Type)
			}
			if p.WorkspaceID != "default" {
				t.Errorf("expected workspace=default, got %q", p.WorkspaceID)
			}
			if p.Role != tc.role {
				t.Errorf("expected role=%q, got %q", tc.role, p.Role)
			}
			if p.ID != row.Prefix {
				t.Errorf("expected ID=prefix=%q, got %q", row.Prefix, p.ID)
			}
			if p.Claims["auth_method"] != "api_key" {
				t.Errorf("auth_method missing in claims: %v", p.Claims)
			}
		})
	}
}

// @constraint S1 — wrong plaintext for the same prefix MUST fail.
// Defence against an attacker who stole only the prefix (which is
// non-secret, displayed in the admin UI).
func TestResolveBearerKeyRejectsWrongPlaintextForKnownPrefix(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	row, err := MintKey(ctx, store.DB(), uid, "default", "read", "test", nil)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	// Preserve the prefix structure (phr_live_<12>_) so parsing
	// succeeds; replace ONLY the suffix portion. This simulates an
	// attacker who learned the public-facing prefix from the admin
	// UI and tries random suffixes.
	sepIdx := strings.LastIndexByte(row.Plaintext, '_')
	if sepIdx < 0 {
		t.Fatalf("unexpected plaintext shape: %q", row.Plaintext)
	}
	tampered := row.Plaintext[:sepIdx+1] + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	_, err = ResolveBearerKey(ctx, store.DB(), tampered)
	if err != ErrInvalidKey {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

// @constraint S5 — revoked key resolves with ErrKeyRevoked.
func TestResolveBearerKeyRejectsRevoked(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	row, err := MintKey(ctx, store.DB(), uid, "default", "read", "test", nil)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if err := RevokeKey(ctx, store.DB(), row.ID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	_, err = ResolveBearerKey(ctx, store.DB(), row.Plaintext)
	if err != ErrKeyRevoked {
		t.Fatalf("expected ErrKeyRevoked, got %v", err)
	}
}

// @constraint S5 — owner-side suspension propagates to derived keys.
func TestResolveBearerKeyRejectsKeysOfSuspendedUser(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	row, err := MintKey(ctx, store.DB(), uid, "default", "read", "test", nil)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	// Admin suspends the owner.
	if _, err := store.DB().Exec(`UPDATE users SET status='suspended' WHERE id=?`, uid); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	_, err = ResolveBearerKey(ctx, store.DB(), row.Plaintext)
	if err != ErrKeyRevoked {
		t.Fatalf("expected ErrKeyRevoked when owner suspended, got %v", err)
	}
}

func TestResolveBearerKeyRejectsExpiredKey(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	past := time.Now().Add(-time.Hour)
	// MintKey rejects past expiresAt; insert directly to set up the
	// scenario where a previously-valid key has now expired.
	row, err := MintKey(ctx, store.DB(), uid, "default", "read", "test", nil)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if _, err := store.DB().Exec(
		`UPDATE api_keys SET expires_at=? WHERE id=?`,
		past.UTC().Format(time.RFC3339Nano), row.ID,
	); err != nil {
		t.Fatalf("backdate expiry: %v", err)
	}

	_, err = ResolveBearerKey(ctx, store.DB(), row.Plaintext)
	if err != ErrKeyExpired {
		t.Fatalf("expected ErrKeyExpired, got %v", err)
	}
}

func TestResolveBearerKeyRejectsUnknownPrefix(t *testing.T) {
	store, _ := newKeysTestStore(t)
	ctx := context.Background()

	_, err := ResolveBearerKey(ctx, store.DB(),
		"phr_live_aaaaaaaaaaaa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestResolveBearerKeyRejectsMalformed(t *testing.T) {
	store, _ := newKeysTestStore(t)
	ctx := context.Background()

	for _, bad := range []string{
		"",
		"not-a-bearer-token",
		"phr_live_",                   // missing prefix entropy
		"phr_live_aaaaaaaaaaaa",       // missing suffix
		"phr_live_aaaaaaaaaaaa_short", // suffix too short
		"phr_test_aaaaaaaaaaaa_bbbbbbbbbbbbbbbbbb", // wrong env (Stage 2 only emits live)
	} {
		_, err := ResolveBearerKey(ctx, store.DB(), bad)
		if err != ErrKeyMalformed && err != ErrKeyNotFound {
			t.Errorf("input %q: expected ErrKeyMalformed or ErrKeyNotFound, got %v", bad, err)
		}
	}
}

func TestMintKeyRejectsBadInputs(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name             string
		uid              int64
		ws, scope, label string
		expiresAt        *time.Time
	}{
		{"zero-uid", 0, "default", "read", "x", nil},
		{"empty-ws", uid, "", "read", "x", nil},
		{"bad-scope", uid, "default", "superadmin", "x", nil},
		{"empty-label", uid, "default", "read", "", nil},
	}
	past := time.Now().Add(-time.Hour)
	cases = append(cases, struct {
		name             string
		uid              int64
		ws, scope, label string
		expiresAt        *time.Time
	}{"past-expiry", uid, "default", "read", "x", &past})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MintKey(ctx, store.DB(), tc.uid, tc.ws, tc.scope, tc.label, tc.expiresAt)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRevokeKeyOnUnknownIDReturnsNotFound(t *testing.T) {
	store, _ := newKeysTestStore(t)
	if err := RevokeKey(context.Background(), store.DB(), 9999); err != ErrKeyNotFound {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestRevokeKeyTwiceIsIdempotent(t *testing.T) {
	store, uid := newKeysTestStore(t)
	ctx := context.Background()
	row, _ := MintKey(ctx, store.DB(), uid, "default", "read", "x", nil)
	if err := RevokeKey(ctx, store.DB(), row.ID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	// Second call: row exists but already revoked → no error.
	if err := RevokeKey(ctx, store.DB(), row.ID); err != nil {
		t.Fatalf("second revoke (idempotent): %v", err)
	}
}
