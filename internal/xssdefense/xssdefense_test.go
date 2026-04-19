package xssdefense

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// @constraint RT-9.1 S7
// StripDangerousTags removes <script> in any case variation.
func TestStripDangerousTagsRemovesScript(t *testing.T) {
	cases := []string{
		`Hello <script>alert(1)</script> world`,
		`Hello <SCRIPT>alert(1)</SCRIPT> world`,
		`Hello <script type="text/javascript">bad</script> world`,
		"Hello <script\n src=\"x\">alert</script> world",
	}
	for _, in := range cases {
		out := StripDangerousTags(in)
		if strings.Contains(strings.ToLower(out), "<script") {
			t.Errorf("still contains <script>: %q -> %q", in, out)
		}
		if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") {
			t.Errorf("benign content lost: %q -> %q", in, out)
		}
	}
}

// @constraint RT-9.1 S7
// Event-handler attributes (on*) are stripped regardless of tag.
func TestStripDangerousTagsRemovesEventHandlers(t *testing.T) {
	in := `<img src="x.png" onerror="alert(1)" onclick='evil()' alt="ok"><a href="/" onmouseover="bad">`
	out := StripDangerousTags(in)
	if strings.Contains(strings.ToLower(out), "onerror") {
		t.Errorf("onerror not stripped: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "onclick") {
		t.Errorf("onclick not stripped: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "onmouseover") {
		t.Errorf("onmouseover not stripped: %q", out)
	}
	// Benign alt/href preserved.
	if !strings.Contains(out, "alt=\"ok\"") {
		t.Errorf("alt attribute lost: %q", out)
	}
}

// @constraint RT-9.1 S7
// javascript: URLs in href/src are stripped.
func TestStripDangerousTagsRemovesJavaScriptURLs(t *testing.T) {
	in := `<a href="javascript:alert(1)">click</a><img src="javascript:evil">`
	out := StripDangerousTags(in)
	if strings.Contains(strings.ToLower(out), "javascript:") {
		t.Errorf("javascript: URL survived: %q", out)
	}
}

// @constraint RT-9.1
// Safe markdown content is unchanged.
func TestStripDangerousTagsPreservesSafeContent(t *testing.T) {
	in := "# Heading\n\nSome text with [a link](https://example.com) and **bold**.\n\n- item 1\n- item 2\n"
	out := StripDangerousTags(in)
	if out != in {
		t.Errorf("safe content mutated:\nin:  %q\nout: %q", in, out)
	}
}

// @constraint RT-9.1
// ContainsDangerousHTML detects the same vectors StripDangerousTags would remove.
func TestContainsDangerousHTMLDetection(t *testing.T) {
	dangerous := []string{
		`<script>x</script>`,
		`<img onerror=bad>`,
		`<a href="javascript:alert">x</a>`,
		`<iframe src="evil"></iframe>`,
	}
	for _, s := range dangerous {
		if !ContainsDangerousHTML(s) {
			t.Errorf("should detect as dangerous: %q", s)
		}
	}
	safe := []string{
		`plain text`,
		`<b>bold</b>`,
		`[link](https://example.com)`,
		`<a href="/docs/home">internal</a>`,
	}
	for _, s := range safe {
		if ContainsDangerousHTML(s) {
			t.Errorf("false positive on safe input: %q", s)
		}
	}
}

// @constraint RT-9.2 S7
// SanitizeHTML drops tags not in the allow-list (e.g., <iframe>, <object>).
func TestSanitizeHTMLAllowList(t *testing.T) {
	in := `<p>hello</p><iframe src="evil"></iframe><b>bold</b><object>x</object>`
	out := SanitizeHTML(in)
	if strings.Contains(strings.ToLower(out), "<iframe") {
		t.Errorf("iframe survived: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "<object") {
		t.Errorf("object survived: %q", out)
	}
	if !strings.Contains(out, "<p>hello</p>") {
		t.Errorf("p tag dropped: %q", out)
	}
	if !strings.Contains(out, "<b>bold</b>") {
		t.Errorf("b tag dropped: %q", out)
	}
}

// @constraint RT-9.2
// SanitizeHTML preserves the standard markdown-rendered tag set.
func TestSanitizeHTMLPreservesCommonTags(t *testing.T) {
	tags := []string{"h1", "h2", "p", "a", "img", "ul", "ol", "li", "code", "pre", "blockquote", "em", "strong", "table", "td", "tr"}
	for _, tag := range tags {
		html := "<" + tag + ">content</" + tag + ">"
		out := SanitizeHTML(html)
		if !strings.Contains(out, "<"+tag+">") {
			t.Errorf("allowed tag %s was stripped: %q", tag, out)
		}
	}
}

// @constraint RT-9.2
// Event handlers are stripped even from allowed tags.
func TestSanitizeHTMLStripsHandlersFromAllowedTags(t *testing.T) {
	in := `<p onclick="evil()">safe content</p>`
	out := SanitizeHTML(in)
	if strings.Contains(strings.ToLower(out), "onclick") {
		t.Errorf("onclick survived on allowed tag: %q", out)
	}
	if !strings.Contains(out, "safe content") {
		t.Errorf("benign text lost: %q", out)
	}
}

// @constraint RT-9.3 S7
// CSPMiddleware sets the Content-Security-Policy header on every response.
func TestCSPMiddlewareSetsHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	m := CSPMiddleware(DefaultCSP, next)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP missing 'default-src self': %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none' (clickjacking defense): %q", csp)
	}
}

// @constraint RT-9.3
// CSPMiddleware also sets X-Content-Type-Options and Referrer-Policy — small
// wins alongside CSP.
func TestCSPMiddlewareSetsAuxHeaders(t *testing.T) {
	m := CSPMiddleware("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q, want nosniff", got)
	}
	if got := rr.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy: got %q, want strict-origin-when-cross-origin", got)
	}
}

// @constraint RT-9.3
// Custom policy overrides DefaultCSP.
func TestCSPMiddlewareHonorsCustomPolicy(t *testing.T) {
	custom := "default-src 'none'"
	m := CSPMiddleware(custom, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, req)
	if got := rr.Header().Get("Content-Security-Policy"); got != custom {
		t.Errorf("CSP: got %q, want %q", got, custom)
	}
}

// @constraint RT-9.3
// Empty policy falls back to DefaultCSP.
func TestCSPMiddlewareEmptyPolicyUsesDefault(t *testing.T) {
	m := CSPMiddleware("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, req)
	if got := rr.Header().Get("Content-Security-Policy"); got != DefaultCSP {
		t.Errorf("empty policy didn't fall back to DefaultCSP: got %q", got)
	}
}
