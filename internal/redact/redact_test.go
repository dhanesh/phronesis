package redact

import (
	"strings"
	"testing"
)

// TestStringRedactsSecretPatterns
//
// @constraint S2 — bearer tokens / OAuth params / API keys never appear
// in logs, audit bodies, or error responses.
//
// Satisfies RT-6 evidence E13 (panic_with_bearer_token_does_not_leak)
// and E14 (content_match for the redaction pattern set).
func TestStringRedactsSecretPatterns(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		mustNot  string
		mustHave string
	}{
		{
			name:     "Authorization Bearer with mixed case header",
			in:       "client made request with Authorization: Bearer phr_live_abc123def456ghijklm",
			mustNot:  "phr_live_abc123def456ghijklm",
			mustHave: "Authorization: Bearer <redacted>",
		},
		{
			name:     "OAuth code in query string",
			in:       "GET /oauth/callback?code=secret_auth_code_value&state=xyz HTTP/1.1",
			mustNot:  "secret_auth_code_value",
			mustHave: "?code=<redacted>",
		},
		{
			name:     "refresh_token JSON field",
			in:       `{"refresh_token":"rt_abc.def.ghi","other":"safe"}`,
			mustNot:  "rt_abc.def.ghi",
			mustHave: `"safe"`,
		},
		{
			name:     "PKCE code_verifier in query",
			in:       "POST /token?code_verifier=verifier_value&grant_type=authorization_code",
			mustNot:  "verifier_value",
			mustHave: "grant_type=authorization_code",
		},
		{
			name:     "phr_live_ key naked in stack trace",
			in:       "panic: failed to validate phr_live_a1b2c3d4e5f6g7h8i9j0k1l2 against db",
			mustNot:  "phr_live_a1b2c3d4e5f6g7h8i9j0k1l2",
			mustHave: "phr_live_<redacted>",
		},
		{
			name:     "phr_test_ key (test-mode prefix)",
			in:       "key=phr_test_xxxxxxxxxxxxxxxxxxxxxxxxxxxx audited",
			mustNot:  "phr_test_xxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			mustHave: "phr_test_<redacted>",
		},
		{
			name:     "client_secret JSON field",
			in:       `{"grant_type":"client_credentials","client_secret":"cs_supersecret"}`,
			mustNot:  "cs_supersecret",
			mustHave: `"grant_type":"client_credentials"`,
		},
		{
			name:     "access_token query param",
			in:       "callback /m?access_token=at_value_here&user=alice",
			mustNot:  "at_value_here",
			mustHave: "user=alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := String(tc.in)
			if strings.Contains(out, tc.mustNot) {
				t.Fatalf("output leaks secret %q\ninput:  %s\noutput: %s", tc.mustNot, tc.in, out)
			}
			if !strings.Contains(out, tc.mustHave) {
				t.Fatalf("output missing context marker %q\ninput:  %s\noutput: %s", tc.mustHave, tc.in, out)
			}
		})
	}
}

func TestStringEmptyAndPlainArePreserved(t *testing.T) {
	if String("") != "" {
		t.Fatal("empty string round-trip failed")
	}
	plain := "no secrets here, just GET /api/pages/foo"
	if got := String(plain); got != plain {
		t.Fatalf("plain string mutated: %q -> %q", plain, got)
	}
	// "code" appearing as a non-param word should NOT match.
	noSecret := "the source code here"
	if got := String(noSecret); got != noSecret {
		t.Fatalf("non-param 'code' mutated: %q -> %q", noSecret, got)
	}
}

func TestURLStripsSecretQueryParams(t *testing.T) {
	in := "https://example.com/callback?code=secret123&state=xyz&path=/foo"
	out := URL(in)
	if strings.Contains(out, "secret123") {
		t.Fatalf("URL still contains code: %s", out)
	}
	// path=/foo (or url-encoded variant) must survive.
	if !strings.Contains(out, "path=%2Ffoo") && !strings.Contains(out, "path=/foo") {
		t.Fatalf("URL stripped non-secret param: %s", out)
	}
	// code= must carry the redaction marker (URL-encoded form acceptable).
	if !strings.Contains(out, "code="+Redacted) && !strings.Contains(out, "code=%3Credacted%3E") {
		t.Fatalf("URL missing redaction marker for code: %s", out)
	}
}

func TestURLPreservesPathAndFragment(t *testing.T) {
	in := "https://x.example/admin/users/42?token=secret#tab=keys"
	out := URL(in)
	if !strings.Contains(out, "/admin/users/42") {
		t.Fatalf("path lost: %s", out)
	}
	if !strings.Contains(out, "#tab=keys") {
		t.Fatalf("fragment lost: %s", out)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("token leaked: %s", out)
	}
}

// TestPanicMessageWithBearerIsRedacted simulates a panic-recover handler
// that captures a stack trace containing a bearer token and a JSON body
// with refresh_token. Verifies all secret material is scrubbed while
// debug context (goroutine markers, panic header, non-secret headers)
// is preserved.
//
// @constraint S2 — defense-in-depth panic test.
// Satisfies RT-6 evidence E13.
func TestPanicMessageWithBearerIsRedacted(t *testing.T) {
	stack := `goroutine 1 [running]:
panic: handler crashed processing request
  url: https://phronesis.example.com/mcp?code=oauth_secret_abc123&state=s
  headers: map[Authorization:[Bearer phr_live_xxxxxxxxxxxxxxxxxxxx] X-Workspace:[default]]
  body: {"refresh_token":"rt.aaaa.bbbb"}
`
	got := String(stack)
	for _, secret := range []string{
		"oauth_secret_abc123",
		"phr_live_xxxxxxxxxxxxxxxxxxxx",
		"rt.aaaa.bbbb",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("panic stack leaks %q after redaction:\n%s", secret, got)
		}
	}
	for _, marker := range []string{
		"goroutine 1",
		"panic: handler crashed",
		"X-Workspace:[default]",
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("redaction destroyed debug context %q:\n%s", marker, got)
		}
	}
}

func TestBytesIsStringEquivalent(t *testing.T) {
	in := []byte("Authorization: Bearer phr_live_aaaaaaaaaaaaaaaaaaaa")
	out := Bytes(in)
	if strings.Contains(string(out), "phr_live_aaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("Bytes leaked: %s", out)
	}
	// Empty input round-trip.
	if got := Bytes(nil); got != nil {
		t.Fatalf("nil input should round-trip: %v", got)
	}
}
