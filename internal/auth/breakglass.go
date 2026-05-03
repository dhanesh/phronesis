// Break-glass admin authentication.
//
// Satisfies: RT-13 (break-glass admin segmented from OIDC, env-gated,
//
//	audit-emitting), S4, TN3 (segmented path).
//
// Threat model:
//   - When the OIDC IdP is down (pre-mortem C1) or unconfigured
//     (pre-mortem A1), legitimate admins must still be able to log in.
//   - Always-on break-glass is a credential-sprawl risk. The path
//     therefore exists ONLY when the operator opts in via env var.
//   - When the env var is unset, the route returns 404 — not 401. The
//     handler is not registered with the mux at all (BreakGlassHandler
//     returns nil, the caller is expected to skip mounting).
//   - Every successful break-glass authentication emits a slog event at
//     severity=high so abuse is post-hoc detectable. Once Stage 2's
//     SQLite audit table lands, this becomes a structured audit row.
package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
)

// BreakGlassConfigEnv is the environment variable that carries the
// PHC-formatted Argon2id hash of the break-glass secret. When unset
// (or empty, or malformed), break-glass is disabled.
const BreakGlassConfigEnv = "PHRONESIS_BREAKGLASS"

// BreakGlassHeader is the HTTP header on /admin/break-glass that
// carries the candidate secret value.
const BreakGlassHeader = "X-Breakglass-Secret"

// argon2idParams holds parameters parsed from a PHC string of the form
//
//	$argon2id$v=19$m=<mem>,t=<time>,p=<para>$<salt-b64>$<hash-b64>
type argon2idParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	salt        []byte
	hash        []byte
}

// BreakGlassEnabled reports whether the env var is set and parses to a
// valid PHC string. A malformed env var is treated as DISABLED (with a
// startup-time error log) — never a runtime crash.
func BreakGlassEnabled() bool {
	v := strings.TrimSpace(os.Getenv(BreakGlassConfigEnv))
	if v == "" {
		return false
	}
	if _, err := parseArgon2idPHC(v); err != nil {
		slog.Error("break-glass env var is set but malformed; break-glass remains DISABLED",
			slog.String("err", err.Error()),
			slog.String("component", "phronesis"),
		)
		return false
	}
	return true
}

// BreakGlassHandler returns an HTTP handler that verifies a candidate
// secret in the BreakGlassHeader against the configured Argon2id hash.
//
// On success: emit a severity=high slog event and 200 with body "ok".
// The caller-side server is expected to also issue an admin session
// via its own mechanism — that integration lands in Stage 2.
//
// On failure: 401 with no body. Constant-time comparison.
//
// If break-glass is disabled, BreakGlassHandler returns nil. Callers
// SHOULD mount the route only when this returns non-nil — keeping the
// route 404 (not 401) when break-glass is unconfigured (TN3 contract).
func BreakGlassHandler() http.Handler {
	v := strings.TrimSpace(os.Getenv(BreakGlassConfigEnv))
	if v == "" {
		return nil
	}
	params, err := parseArgon2idPHC(v)
	if err != nil {
		slog.Error("break-glass parse error at handler-init time; not mounting",
			slog.String("err", err.Error()),
			slog.String("component", "phronesis"),
		)
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		candidate := r.Header.Get(BreakGlassHeader)
		if candidate == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		hashOfCandidate := argon2.IDKey(
			[]byte(candidate),
			params.salt,
			params.iterations,
			params.memory,
			params.parallelism,
			uint32(len(params.hash)),
		)
		if subtle.ConstantTimeCompare(hashOfCandidate, params.hash) != 1 {
			// No body, no hint as to which part failed. S2-aligned.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// severity=high is the audit signal. Once Stage 2's SQLite
		// audit table lands, this becomes a structured audit row.
		slog.Warn("break-glass admin authentication SUCCEEDED",
			slog.String("severity", "high"),
			slog.String("component", "phronesis"),
			slog.String("event", "breakglass.use"),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
		)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func parseArgon2idPHC(s string) (argon2idParams, error) {
	// $argon2id$v=19$m=<m>,t=<t>,p=<p>$<salt>$<hash>
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" {
		return argon2idParams{}, fmt.Errorf("not a PHC string (need 6 dollar-separated parts)")
	}
	if parts[1] != "argon2id" {
		return argon2idParams{}, fmt.Errorf("unsupported algorithm %q (want argon2id)", parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != 19 {
		return argon2idParams{}, fmt.Errorf("unsupported version %q (want v=19)", parts[2])
	}
	var p argon2idParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return argon2idParams{}, fmt.Errorf("bad params block %q: %w", parts[3], err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argon2idParams{}, fmt.Errorf("salt base64: %w", err)
	}
	if len(salt) < 8 {
		return argon2idParams{}, errors.New("salt is too short (<8 bytes)")
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argon2idParams{}, fmt.Errorf("hash base64: %w", err)
	}
	if len(hash) < 16 {
		return argon2idParams{}, errors.New("hash is too short (<16 bytes)")
	}
	p.salt = salt
	p.hash = hash
	return p, nil
}
