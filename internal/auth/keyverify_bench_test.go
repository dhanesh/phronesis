// Microbench for the cold-path key-verify pipeline. Closes G3 from the
// user-mgmt-mcp Stage 1c verify cycle.
//
// Satisfies: T4 (≤5ms p95 cached / ≤50ms p95 cold auth verify),
//            S1 (Argon2id hash; bench confirms production-cost
//                parameters fit the budget),
//            G3 (T4 challenger: assumption — empirical confirmation
//                before Stage 2 cache code lands).
//
// What this measures:
//
//   - Argon2id verify cost at OWASP-2023 production params
//     (m=64MiB, t=3, p=4). This is the dominant cold-path cost.
//   - SQLite SELECT on api_keys WHERE key_prefix = ? — the index
//     lookup the production verify function will do first.
//   - Combined cold path: prefix lookup -> hash compare. Approximates
//     the verify function Stage 2 will ship.
//
// What this does NOT measure:
//
//   - The cached path (sync.Map / LRU hit). The cache doesn't exist
//     yet; Stage 2 must add a parallel BenchmarkKeyVerifyCached.
//   - Event-driven invalidation overhead. Same — Stage 2 work.
//
// Run: go test -bench=. -benchtime=3x ./internal/auth -run=^$
//
// Conventions:
//   - benchtime=3x keeps the Argon2id benchmarks runnable in a few
//     seconds without sacrificing signal — Argon2id is by design
//     slow, and we care about absolute cost per invocation, not
//     iteration count.
//   - The seeded api_keys rows use an isolated tempdir SQLite so
//     concurrent test runs don't contend.

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// Production Argon2id parameters per OWASP Password Storage Cheat
// Sheet (2023 recommendation for Argon2id):
//
//	m = 64 MiB (memory)
//	t = 3      (iterations)
//	p = 4      (parallelism)
//
// These are the params Stage 2's key-mint flow is expected to use.
// The breakglass.go test helper deliberately uses lower params
// (64 MiB, t=2, p=1) for fast unit tests — those are NOT the prod
// params and are not benchmarked here.
const (
	prodArgon2Memory      uint32 = 64 * 1024
	prodArgon2Iterations  uint32 = 3
	prodArgon2Parallelism uint8  = 4
	prodArgon2KeyLen      uint32 = 32
)

// BenchmarkArgon2idVerify_Production times a single Argon2id verify
// at production parameters. This is the dominant cold-path cost.
func BenchmarkArgon2idVerify_Production(b *testing.B) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		b.Fatalf("salt: %v", err)
	}
	secret := []byte("the-quick-brown-fox-jumps-over-the-lazy-dog")
	expected := argon2.IDKey(secret, salt,
		prodArgon2Iterations, prodArgon2Memory, prodArgon2Parallelism, prodArgon2KeyLen)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		got := argon2.IDKey(secret, salt,
			prodArgon2Iterations, prodArgon2Memory, prodArgon2Parallelism, prodArgon2KeyLen)
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			b.Fatal("verify mismatch")
		}
	}
}

// BenchmarkSQLiteKeyLookup times the indexed SELECT a verify function
// will run before Argon2id. Measures the lookup component in isolation
// so Stage 2 can confirm the index is doing its job under the medium-
// scale ceiling (B3: 1000 active service principals).
func BenchmarkSQLiteKeyLookup(b *testing.B) {
	store, prefixes := seedKeyTable(b, 1000)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var hash []byte
		err := store.DB().QueryRowContext(ctx,
			`SELECT key_hash FROM api_keys WHERE key_prefix = ?`,
			prefixes[i%len(prefixes)]).Scan(&hash)
		if err != nil {
			b.Fatalf("lookup: %v", err)
		}
	}
}

// BenchmarkKeyVerifyCold_Production composes the full cold-path
// pipeline: SQLite prefix lookup -> Argon2id verify. This is the
// shape of the verify function Stage 2 will ship for the
// CACHE-MISS branch (the cache hit will be much faster).
//
// Result is interpreted against T4's 50ms p95 cold ceiling. If the
// observed p99 here exceeds 50ms, T4 needs relaxation OR Argon2id
// params need to be lowered (which trades attacker cost for serving
// budget — both have to be acknowledged).
func BenchmarkKeyVerifyCold_Production(b *testing.B) {
	store, prefixes := seedKeyTable(b, 1000)
	ctx := context.Background()

	// Pre-derive plaintext for prefixes[0] so we can verify against it.
	// The seeded plaintext is deterministic from the prefix index.
	candidatePlaintext := []byte("plaintext-key-for-row-0")
	salt, err := hex.DecodeString("0123456789ABCDEF0123456789ABCDEF")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Step 1: index lookup
		var stored []byte
		err := store.DB().QueryRowContext(ctx,
			`SELECT key_hash FROM api_keys WHERE key_prefix = ?`,
			prefixes[i%len(prefixes)]).Scan(&stored)
		if err != nil {
			b.Fatalf("lookup: %v", err)
		}
		// Step 2: Argon2id at production cost
		got := argon2.IDKey(candidatePlaintext, salt,
			prodArgon2Iterations, prodArgon2Memory, prodArgon2Parallelism, prodArgon2KeyLen)
		_ = got
		// We don't ConstantTimeCompare here because the seeded hashes
		// were generated with index-keyed plaintext (not the candidate)
		// — the bench measures the verify pipeline cost, not the
		// equality outcome.
	}
}

// seedKeyTable opens a fresh SQLite store and inserts n rows into
// api_keys with deterministic prefixes. Returns the store and the
// list of prefixes (caller indexes into them).
func seedKeyTable(tb testing.TB, n int) (*sqlite.Store, []string) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "bench.db")
	store, err := sqlite.Open(path)
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	tb.Cleanup(func() { _ = store.Close() })

	// Seed a single user; keys reference users.id via FK.
	res, err := store.DB().Exec(
		`INSERT INTO users (oidc_sub, email, display_name, role) VALUES (?, ?, ?, ?)`,
		"sub-bench", "bench@example.com", "Bench", "user")
	if err != nil {
		tb.Fatalf("seed user: %v", err)
	}
	userID, _ := res.LastInsertId()

	tx, err := store.DB().Begin()
	if err != nil {
		tb.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(
		`INSERT INTO api_keys (user_id, workspace_slug, scope, label, key_prefix, key_hash)
		 VALUES (?, 'default', 'read', ?, ?, ?)`)
	if err != nil {
		tb.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	prefixes := make([]string, n)
	salt, _ := hex.DecodeString("0123456789ABCDEF0123456789ABCDEF")
	for i := 0; i < n; i++ {
		prefix := fmt.Sprintf("phr_live_bench%010d", i)
		prefixes[i] = prefix
		// Seed hashes at LOW params so seeding stays fast; the bench
		// loop runs Argon2id at production params separately.
		hash := argon2.IDKey(
			[]byte(fmt.Sprintf("plaintext-key-for-row-%d", i)),
			salt, 1, 8*1024, 1, 32)
		if _, err := stmt.Exec(userID, "row-"+prefix, prefix, hash); err != nil {
			tb.Fatalf("insert row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("commit: %v", err)
	}
	return store, prefixes
}

// TestKeyVerifyBenchSeedSucceeds ensures the seed helper works under
// `go test ./...` (without -bench), so unit-test runs catch any seed
// breakage early.
func TestKeyVerifyBenchSeedSucceeds(t *testing.T) {
	store, prefixes := seedKeyTable(t, 4)
	if len(prefixes) != 4 {
		t.Fatalf("expected 4 prefixes, got %d", len(prefixes))
	}
	var hash []byte
	err := store.DB().QueryRow(
		`SELECT key_hash FROM api_keys WHERE key_prefix = ?`, prefixes[0],
	).Scan(&hash)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(hash) == 0 {
		t.Fatal("key_hash is empty")
	}
	_ = sql.ErrNoRows // ensures stdlib import stays referenced
}
