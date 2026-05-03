// Package redact implements secret-pattern redaction for log writers,
// error response builders, and panic-recover handlers.
//
// Satisfies: RT-6 (BINDING) — bearer-token redaction at every egress.
//
//	S2 — bearer tokens / API keys / OAuth state never appear
//	     in logs, audit bodies, panic stacks, or error responses.
//
// Threat model: a single missed egress point is a credential-leak
// incident. Patterns covered:
//   - Authorization: Bearer <token>     (any opaque bearer)
//   - ?code= / ?state= / ?token= / ?refresh_token= / ?code_verifier=
//   - phr_live_<...> / phr_test_<...>   (workspace API keys, RT-3)
//   - JSON fields named password / secret / token / api_key /
//     access_token / refresh_token / code_verifier
//
// Build-order: this package MUST be wired into slog handlers, error
// response writers, and panic-recover middleware BEFORE T1 (OAuth 2.1)
// handlers ship. See TN8 in .manifold/user-mgmt-mcp.md.
package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// Patterns matched for redaction. All operate on byte sequences and are
// case-insensitive where the underlying spec allows.
//
// Update note: when adding a new credential format, add a pattern here
// AND add a corresponding case to the test panic-with-secret table in
// redact_test.go. Both must travel together.
var (
	// Authorization: Bearer <token> — covers OAuth, opaque API keys.
	bearerHeader = regexp.MustCompile(`(?i)(authorization\s*:\s*)(bearer\s+)([A-Za-z0-9._\-+/=]+)`)

	// OAuth 2.1 / PKCE query/form params: code, state, token,
	// refresh_token, access_token, id_token, code_verifier, code_challenge.
	// Accept either a query-string delimiter (?/&) OR a free-text boundary
	// (start-of-string, whitespace, comma) so error messages of the form
	// "verification failed for code=secret" are also scrubbed.
	oauthQueryParam = regexp.MustCompile(`(?i)((?:^|[?&\s,])(?:code|state|token|refresh_token|access_token|id_token|code_verifier|code_challenge|client_secret|api_key|apikey)=)([^&\s"<>,]+)`)

	// Phronesis API key prefix: phr_live_/phr_test_ followed by entropy.
	// >=16 chars covers our minimum entropy budget without false-positive
	// matching on short identifiers.
	phrKey = regexp.MustCompile(`(?i)(phr_(?:live|test)_)([A-Za-z0-9]{16,})`)

	// JSON fields that commonly hold secrets in body / response payloads.
	jsonSecretField = regexp.MustCompile(`(?i)("(?:password|secret|token|api[_-]?key|access[_-]?token|refresh[_-]?token|code[_-]?verifier|client[_-]?secret)"\s*:\s*)"([^"]+)"`)
)

// Redacted is the marker substituted in place of any matched secret. It
// is intentionally short and unambiguous so log readers know what was
// removed without leaking length information.
const Redacted = "<redacted>"

// String returns a copy of s with credential-bearing substrings replaced.
//
// Replacement is non-reversible — the original values cannot be
// recovered from the redacted output. Surrounding context (e.g.
// "Authorization: Bearer <redacted>") is preserved so logs remain
// debuggable.
func String(s string) string {
	if s == "" {
		return s
	}
	s = bearerHeader.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = oauthQueryParam.ReplaceAllString(s, "${1}"+Redacted)
	s = phrKey.ReplaceAllString(s, "${1}"+Redacted)
	s = jsonSecretField.ReplaceAllString(s, `${1}"`+Redacted+`"`)
	return s
}

// Bytes returns String applied to b. Useful for io.Writer wrappers and
// panic-recover handlers that capture stack output as []byte.
func Bytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	return []byte(String(string(b)))
}

// URL strips secret-bearing query parameters from u, returning a
// redacted copy. The path and fragment are preserved verbatim. On parse
// failure, falls back to String redaction over the raw form.
//
// Defense in depth for code paths that log raw URLs (slog access logs,
// panic dumps embedding request URLs).
func URL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return String(u)
	}
	q := parsed.Query()
	for k := range q {
		if isSecretParam(k) {
			q.Set(k, Redacted)
		}
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func isSecretParam(name string) bool {
	switch strings.ToLower(name) {
	case "code", "state", "token", "access_token", "id_token", "refresh_token",
		"code_verifier", "code_challenge", "client_secret", "api_key", "apikey":
		return true
	}
	return false
}
