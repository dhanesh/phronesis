package render

import (
	"strings"
	"testing"
)

func TestSafeURLAllowedSchemes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"http", "http://example.com", "http://example.com"},
		{"https", "https://example.com/path", "https://example.com/path"},
		{"mailto", "mailto:a@b.com", "mailto:a@b.com"},
		{"absolute path", "/foo/bar", "/foo/bar"},
		{"fragment", "#section", "#section"},
		{"query", "?q=1", "?q=1"},
		{"scheme relative", "//cdn.example.com/x", "//cdn.example.com/x"},
		{"plain relative", "foo/bar", "foo/bar"},
		{"uppercase HTTPS", "HTTPS://example.com", "HTTPS://example.com"},
		{"empty", "", "#"},
		{"whitespace", "   ", "#"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeURL(tt.in); got != tt.want {
				t.Errorf("safeURL(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeURLRejectsUnsafeSchemes(t *testing.T) {
	tests := []string{
		"javascript:alert(1)",
		"JavaScript:alert(1)",
		"  javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"about:blank",
	}

	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			if got := safeURL(in); got != "#" {
				t.Errorf("safeURL(%q) = %q; want %q", in, got, "#")
			}
		})
	}
}

func TestRenderInlineMarkdownLinkRejectsJavaScriptURL(t *testing.T) {
	got := RenderInline("[click](javascript:alert(1))")
	if strings.Contains(strings.ToLower(got), "javascript:") {
		t.Errorf("renderInline leaked javascript: scheme: %q", got)
	}
	if !strings.Contains(got, `href="#"`) {
		t.Errorf("expected inert href=\"#\", got: %q", got)
	}
}

func TestRenderInlineEscapesAttributeQuotes(t *testing.T) {
	// Malformed URL with a stray quote — must not break out of the href
	// attribute or inject extra attributes.
	got := RenderInline(`[x](http://a.com" onclick=alert)`)
	if strings.Contains(got, "onclick") && !strings.Contains(got, "&#34;") && !strings.Contains(got, "&quot;") {
		t.Errorf("attribute boundary appears broken: %q", got)
	}
}

func TestRenderInlineEscapesLabel(t *testing.T) {
	got := RenderInline(`[<img src=x onerror=alert(1)>](http://a.com)`)
	if strings.Contains(got, "<img") {
		t.Errorf("label HTML not escaped: %q", got)
	}
}

func TestRenderInlinePreservesValidLinks(t *testing.T) {
	got := RenderInline("[example](https://example.com)")
	want := `<a href="https://example.com">example</a>`
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestRenderInlineWikiLinkSafe(t *testing.T) {
	got := RenderInline("[[Some Page|label]]")
	if !strings.Contains(got, `href="/w/some-page"`) {
		t.Errorf("wiki link not normalized: %q", got)
	}
	if !strings.Contains(got, `>label<`) {
		t.Errorf("wiki label missing: %q", got)
	}
}

func TestRenderMarkdownTagsAndLinks(t *testing.T) {
	src := "# Title\n\nSee [[Other]] and #important.\n"
	got := RenderMarkdown(src)
	if len(got.Links) != 1 || got.Links[0] != "other" {
		t.Errorf("Links = %v; want [other]", got.Links)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "important" {
		t.Errorf("Tags = %v; want [important]", got.Tags)
	}
	if !strings.Contains(got.HTML, "<h1>") {
		t.Errorf("expected <h1>, got: %q", got.HTML)
	}
}

func TestRenderMarkdownTaskList(t *testing.T) {
	src := "- [ ] todo one\n- [x] done two\n"
	got := RenderMarkdown(src)
	if len(got.Tasks) != 2 {
		t.Fatalf("expected 2 tasks; got %d", len(got.Tasks))
	}
	if got.Tasks[0].Checked || !got.Tasks[1].Checked {
		t.Errorf("task checked flags wrong: %+v", got.Tasks)
	}
}
