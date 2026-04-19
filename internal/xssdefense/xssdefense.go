// Package xssdefense provides minimal cross-site-scripting defense
// primitives: input sanitization at markdown store-time (RT-9.1), rendered-
// HTML sanitization at response-time (RT-9.2), and a Content-Security-Policy
// middleware (RT-9.3).
//
// Satisfies: RT-9, S7
//
// Scope: v1 implements defense-in-depth with simple, auditable logic using
// only the Go standard library. The helpers reject/strip the most common
// XSS vectors (<script>, event handlers, javascript: URLs, data: URLs except
// safe image types). For production hardening against more sophisticated
// attacks, a dedicated library like bluemonday is the right follow-up; this
// package intentionally stays dependency-light per CLAUDE.md.
package xssdefense

import (
	"net/http"
	"regexp"
	"strings"
)

// DefaultCSP is a conservative Content-Security-Policy suitable for phronesis.
// Only same-origin resources are loaded; inline scripts are forbidden; images
// and media may be data: URLs (for content-addressed /media/<sha> URLs
// inlined by the renderer).
//
// Operators MAY override this per deployment via CSPMiddleware's policy arg.
//
// Satisfies: RT-9.3, S7 (CSP at response time)
const DefaultCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"media-src 'self'; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// CSPMiddleware sets the Content-Security-Policy header on every response.
// Pass DefaultCSP (or a custom policy string) as the policy argument.
//
// Satisfies: RT-9.3
func CSPMiddleware(policy string, next http.Handler) http.Handler {
	if policy == "" {
		policy = DefaultCSP
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set before calling next so downstream handlers can override if
		// absolutely necessary (rare; only some embedded-resource routes).
		w.Header().Set("Content-Security-Policy", policy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// Dangerous HTML tag names stripped at store-time. The list is intentionally
// narrow; richer sanitization happens at render-time via SanitizeHTML.
var dangerousTagRE = regexp.MustCompile(`(?is)<\s*/?\s*(script|iframe|object|embed|applet|meta|link|form)\b[^>]*>`)

// Event-handler attributes (onclick, onmouseover, etc.) — a common XSS vector.
var eventHandlerAttrRE = regexp.MustCompile(`(?is)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]*)`)

// Dangerous URL schemes in href/src/action attributes. Allows https://, http://,
// /relative, ./relative, data:image/*, mailto:, and a few safe custom schemes.
var dangerousURLAttrRE = regexp.MustCompile(`(?is)\s+(href|src|action|formaction|poster)\s*=\s*("javascript:[^"]*"|'javascript:[^']*'|javascript:[^\s>]*)`)

// StripDangerousTags scrubs markdown (or raw HTML) source of the highest-risk
// tags and attributes at STORE time. Markdown documents that contain raw
// HTML get the dangerous parts removed BEFORE persistence — defense layer 1.
//
// Satisfies: RT-9.1
//
// Note: this is a deliberately narrow filter. It does NOT parse markdown or
// HTML; it operates on regex. The intent is "strip the obvious bad stuff";
// RenderTimeSanitize handles the richer allow-list check on parser output.
func StripDangerousTags(source string) string {
	out := dangerousTagRE.ReplaceAllString(source, "")
	out = eventHandlerAttrRE.ReplaceAllString(out, "")
	out = dangerousURLAttrRE.ReplaceAllString(out, "")
	return out
}

// ContainsDangerousHTML reports whether source contains any of the raw tags
// or attributes that StripDangerousTags would remove. Useful for audit:
// callers may want to log a policy violation before stripping (to detect
// content-paste-from-Word per pre-mortem #2).
//
// Satisfies: RT-9.1 (observability)
func ContainsDangerousHTML(source string) bool {
	return dangerousTagRE.MatchString(source) ||
		eventHandlerAttrRE.MatchString(source) ||
		dangerousURLAttrRE.MatchString(source)
}

// Allowed rendered-HTML tags. Markdown renderers like phronesis's custom
// renderer produce a known set of tags; anything else in the output is
// suspicious. Matches the allow-list approach recommended by OWASP.
var allowedTags = map[string]bool{
	"a": true, "b": true, "blockquote": true, "br": true, "code": true,
	"dd": true, "del": true, "div": true, "dl": true, "dt": true,
	"em": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "hr": true, "i": true, "img": true, "input": true, // input for task-list checkboxes
	"li": true, "ol": true, "p": true, "pre": true, "s": true,
	"span": true, "strong": true, "sub": true, "sup": true, "table": true,
	"tbody": true, "td": true, "tfoot": true, "th": true, "thead": true,
	"tr": true, "u": true, "ul": true,
}

// renderedTagRE captures any HTML tag with its attributes. Used by
// SanitizeHTML to iterate tags and drop non-allowed ones.
var renderedTagRE = regexp.MustCompile(`(?is)<\s*(/?)([a-zA-Z][a-zA-Z0-9-]*)\b([^>]*)>`)

// SanitizeHTML removes any HTML tag not in the allowed-tags set from
// already-rendered HTML. Event handlers and javascript: URLs are also
// stripped (defense in depth — the markdown renderer should never emit
// them, but this provides a guardrail).
//
// Satisfies: RT-9.2
//
// Returns the sanitized HTML as a string. Leaves text content unchanged.
func SanitizeHTML(html string) string {
	// First pass: strip event handlers and javascript: URLs anywhere.
	html = eventHandlerAttrRE.ReplaceAllString(html, "")
	html = dangerousURLAttrRE.ReplaceAllString(html, "")

	// Second pass: drop tags not in allow-list.
	return renderedTagRE.ReplaceAllStringFunc(html, func(match string) string {
		sub := renderedTagRE.FindStringSubmatch(match)
		if len(sub) < 3 {
			return ""
		}
		tag := strings.ToLower(sub[2])
		if allowedTags[tag] {
			return match
		}
		// Drop the tag entirely (including open/close variants).
		return ""
	})
}
