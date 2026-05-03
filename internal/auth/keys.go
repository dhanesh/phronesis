// Workspace API key minting + verification.
//
// Satisfies: RT-3 (workspace-scoped principal model — keys resolve to
//
//	a workspace-pinned service-account principal),
//	S1 (Argon2id hash; plaintext shown once at creation),
//	S3 (scope tier enforced at the boundary),
//	T4 (production Argon2id params per G3 closure: m=64MiB,
//	    t=3, p=4, validated by internal/auth/keyverify_bench_test.go),
//	TN7 (admin approves -> key minted; plaintext returned once).
//
// Naming:
//   - phr_live_<prefix>_<suffix> is the public-facing token format.
//     <prefix> is 12 chars of base32 entropy used for index lookup;
//     <suffix> is 32 chars of base32 used for the actual secret.
//     Example: phr_live_abcd1234efgh_ijklmnopqrstuvwxyz0123456789ab
//   - The full plaintext is shown ONCE at creation. Only the
//     Argon2id hash + the prefix are persisted.
//   - Listing/revocation work by prefix (the non-secret display id).

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/dhanesh/phronesis/internal/principal"
)

// Production Argon2id parameters per OWASP-2023 + G3 microbench.
// Centralised here so MintKey + ResolveBearerKey hash with identical
// costs (any divergence breaks verification).
const (
	prodArgon2Memory      uint32 = 64 * 1024
	prodArgon2Iterations  uint32 = 3
	prodArgon2Parallelism uint8  = 4
	prodArgon2KeyLen      uint32 = 32
	prodArgon2SaltBytes          = 16
)

// Key prefix structure: "phr_live_" + 12 chars base32 prefix.
const (
	keyTokenPrefix      = "phr_live_"
	keyPrefixEntropyLen = 12 // base32 chars after keyTokenPrefix
	keySuffixEntropyLen = 32 // base32 chars after the second underscore
)

// Errors surfaced by ResolveBearerKey. Callers map these to HTTP
// 401 (no valid key) without leaking which leaf failed — defence
// against probing by an attacker.
var (
	ErrKeyNotFound  = errors.New("auth: key not found")
	ErrKeyRevoked   = errors.New("auth: key revoked")
	ErrKeyExpired   = errors.New("auth: key expired")
	ErrInvalidKey   = errors.New("auth: invalid key")
	ErrKeyMalformed = errors.New("auth: malformed key token")
)

// KeyRow is the public-facing representation returned by MintKey.
// The Plaintext field is populated only at the moment of creation;
// it is NEVER recoverable from the database afterwards (S1).
type KeyRow struct {
	ID            int64
	UserID        int64
	WorkspaceSlug string
	Scope         string // "read" | "write" | "admin"
	Label         string
	Prefix        string // phr_live_<12char>
	Plaintext     string // full token; shown ONCE
	CreatedAt     time.Time
	ExpiresAt     *time.Time
}

// MintKey issues a new API key for the given user + workspace + scope
// + label. The full plaintext is generated, hashed with Argon2id at
// production parameters, and returned on the KeyRow exactly once.
// The caller (admin approve handler) is responsible for delivering
// the plaintext to the requesting user via a one-time channel
// (in-app notification + optional email) and discarding it from
// memory afterwards.
//
// expiresAt may be nil for long-lived keys; otherwise it MUST be
// in the future (MintKey returns an error if it isn't).
func MintKey(ctx context.Context, db *sql.DB, userID int64, workspace, scope, label string, expiresAt *time.Time) (*KeyRow, error) {
	if userID <= 0 {
		return nil, errors.New("auth: MintKey requires a positive user id")
	}
	if workspace == "" {
		return nil, errors.New("auth: MintKey requires a workspace")
	}
	if scope != "read" && scope != "write" && scope != "admin" {
		return nil, fmt.Errorf("auth: MintKey scope must be read|write|admin, got %q", scope)
	}
	if label == "" {
		return nil, errors.New("auth: MintKey requires a non-empty label")
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, errors.New("auth: MintKey expiresAt must be in the future")
	}

	prefixSuffix, err := randomBase32(keyPrefixEntropyLen)
	if err != nil {
		return nil, fmt.Errorf("auth: prefix entropy: %w", err)
	}
	secretSuffix, err := randomBase32(keySuffixEntropyLen)
	if err != nil {
		return nil, fmt.Errorf("auth: secret entropy: %w", err)
	}
	prefix := keyTokenPrefix + prefixSuffix
	plaintext := prefix + "_" + secretSuffix

	salt := make([]byte, prodArgon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("auth: salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plaintext), salt,
		prodArgon2Iterations, prodArgon2Memory, prodArgon2Parallelism, prodArgon2KeyLen)

	// Encode salt+hash together for storage — the salt must travel
	// with the hash so verify uses the same salt. Format:
	//   <salt-hex>$<hash-hex>
	// Stored as BLOB (api_keys.key_hash). Cheap to parse; no PHC
	// header overhead needed since we only ever use one alg+params.
	stored := encodeSaltHash(salt, hash)

	var (
		expiresAtArg any
		expiresTime  *time.Time
	)
	if expiresAt != nil {
		expiresAtArg = expiresAt.UTC().Format(time.RFC3339Nano)
		t := *expiresAt
		expiresTime = &t
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, workspace_slug, scope, label, key_prefix, key_hash, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, workspace, scope, label, prefix, stored, expiresAtArg)
	if err != nil {
		return nil, fmt.Errorf("auth: insert key row: %w", err)
	}
	id, _ := res.LastInsertId()

	return &KeyRow{
		ID:            id,
		UserID:        userID,
		WorkspaceSlug: workspace,
		Scope:         scope,
		Label:         label,
		Prefix:        prefix,
		Plaintext:     plaintext,
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     expiresTime,
	}, nil
}

// ResolveBearerKey verifies a presented bearer token and returns the
// resolved Principal. Returns one of the Err* sentinels above on any
// failure (callers map all of these to HTTP 401 with a generic
// "unauthorized" message — leaking which leaf failed helps probing).
//
// Verification steps:
//  1. Parse plaintext as phr_live_<prefix>_<suffix>; if malformed,
//     return ErrKeyMalformed.
//  2. SELECT api_keys row by key_prefix; if missing, ErrKeyNotFound.
//  3. Check revoked_at IS NULL; otherwise ErrKeyRevoked.
//  4. Check expires_at IS NULL OR expires_at > now; otherwise
//     ErrKeyExpired.
//  5. Argon2id verify the plaintext against the stored salt+hash;
//     mismatch returns ErrInvalidKey.
//  6. JOIN to users to resolve the owner; build the Principal.
//
// Constant-time comparison on the Argon2id hash is implicit in
// argon2.IDKey + crypto/subtle.ConstantTimeCompare.
func ResolveBearerKey(ctx context.Context, db *sql.DB, plaintext string) (*principal.Principal, error) {
	prefix, ok := parseKeyPrefix(plaintext)
	if !ok {
		return nil, ErrKeyMalformed
	}

	var (
		id         int64
		userID     int64
		workspace  string
		scope      string
		stored     []byte
		revokedAt  sql.NullString
		expiresAt  sql.NullString
		oidcSub    string
		userStatus string
	)
	err := db.QueryRowContext(ctx, `
		SELECT k.id, k.user_id, k.workspace_slug, k.scope, k.key_hash,
		       k.revoked_at, k.expires_at,
		       u.oidc_sub, u.status
		  FROM api_keys k
		  JOIN users u ON u.id = k.user_id
		 WHERE k.key_prefix = ?
	`, prefix).Scan(&id, &userID, &workspace, &scope, &stored, &revokedAt, &expiresAt, &oidcSub, &userStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrKeyNotFound
	case err != nil:
		return nil, fmt.Errorf("auth: lookup key %q: %w", prefix, err)
	}

	if revokedAt.Valid {
		return nil, ErrKeyRevoked
	}
	if expiresAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, expiresAt.String)
		if err == nil && !t.After(time.Now()) {
			return nil, ErrKeyExpired
		}
	}
	// Owner-side suspension also revokes derived keys (S5 leaf).
	if userStatus == "suspended" {
		return nil, ErrKeyRevoked
	}

	salt, expectedHash, err := decodeSaltHash(stored)
	if err != nil {
		return nil, fmt.Errorf("auth: decode stored hash: %w", err)
	}
	candidate := argon2.IDKey([]byte(plaintext), salt,
		prodArgon2Iterations, prodArgon2Memory, prodArgon2Parallelism, prodArgon2KeyLen)
	if subtle.ConstantTimeCompare(candidate, expectedHash) != 1 {
		return nil, ErrInvalidKey
	}

	return &principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          prefix, // audit-meaningful identity per service-account
		WorkspaceID: workspace,
		Role:        scopeToRole(scope),
		Claims: map[string]string{
			"auth_method":    "api_key",
			"key_prefix":     prefix,
			"owner_oidc_sub": oidcSub,
			"key_id":         fmt.Sprintf("%d", id),
		},
	}, nil
}

// RevokeKey marks a key revoked. Idempotent: a second call on an
// already-revoked key is a no-op (returns ErrKeyNotFound only when
// the row does not exist at all).
func RevokeKey(ctx context.Context, db *sql.DB, keyID int64) error {
	res, err := db.ExecContext(ctx,
		`UPDATE api_keys
		    SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		  WHERE id = ? AND revoked_at IS NULL`, keyID)
	if err != nil {
		return fmt.Errorf("auth: revoke %d: %w", keyID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "doesn't exist" from "already revoked" via a
		// follow-up query — same SQL key, different semantics for
		// the caller.
		var dummy int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM api_keys WHERE id = ?`, keyID).Scan(&dummy)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrKeyNotFound
		}
		// Already-revoked is a no-op; return nil.
	}
	return nil
}

// scopeToRole maps the schema scope tier to the principal Role enum.
//
//	read  -> RoleViewer
//	write -> RoleEditor
//	admin -> RoleAdmin
//
// Used by ResolveBearerKey when constructing the Principal.
func scopeToRole(scope string) principal.Role {
	switch scope {
	case "read":
		return principal.RoleViewer
	case "write":
		return principal.RoleEditor
	case "admin":
		return principal.RoleAdmin
	default:
		return principal.RoleViewer // fail-closed
	}
}

// parseKeyPrefix returns the prefix portion of a phr_live_X_Y token
// (i.e. "phr_live_X"), or ok=false if the token is malformed.
func parseKeyPrefix(plaintext string) (string, bool) {
	if !strings.HasPrefix(plaintext, keyTokenPrefix) {
		return "", false
	}
	rest := plaintext[len(keyTokenPrefix):]
	idx := strings.IndexByte(rest, '_')
	if idx != keyPrefixEntropyLen {
		return "", false
	}
	if len(rest)-idx-1 < 8 {
		// Suffix too short — never a real key.
		return "", false
	}
	return keyTokenPrefix + rest[:idx], true
}

// randomBase32 returns n base32 characters' worth of cryptographic
// entropy. Lowercased, no padding, no I/L/0/1 (Crockford-style only
// in spirit — we just trim padding from std base32 and lowercase).
func randomBase32(n int) (string, error) {
	// Each base32 char carries 5 bits, so we need ceil(n*5/8) bytes.
	bytesNeeded := (n*5 + 7) / 8
	buf := make([]byte, bytesNeeded)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	if len(encoded) < n {
		return "", fmt.Errorf("auth: base32 produced %d chars, wanted %d", len(encoded), n)
	}
	return strings.ToLower(encoded[:n]), nil
}

// encodeSaltHash packs salt + hash into a single byte slice for
// storage in api_keys.key_hash (BLOB). Format:
//
//	<salt-byte-len:1><salt><hash>
//
// Single-byte length prefix is sufficient since Argon2id salt is
// 16 bytes by our choice and hash is 32 bytes by prodArgon2KeyLen.
func encodeSaltHash(salt, hash []byte) []byte {
	out := make([]byte, 0, 1+len(salt)+len(hash))
	out = append(out, byte(len(salt)))
	out = append(out, salt...)
	out = append(out, hash...)
	return out
}

func decodeSaltHash(b []byte) ([]byte, []byte, error) {
	if len(b) < 1 {
		return nil, nil, errors.New("auth: stored hash empty")
	}
	saltLen := int(b[0])
	if 1+saltLen > len(b) {
		return nil, nil, errors.New("auth: stored hash salt-length out of range")
	}
	salt := b[1 : 1+saltLen]
	hash := b[1+saltLen:]
	if len(hash) == 0 {
		return nil, nil, errors.New("auth: stored hash payload missing")
	}
	return salt, hash, nil
}
