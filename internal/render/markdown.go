package render

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	wikiLinkPattern = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	mdLinkPattern   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	tagPattern      = regexp.MustCompile(`(^|\s)#([a-zA-Z0-9][\w/-]*)`)
)

type Task struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

type Result struct {
	HTML      string   `json:"html"`
	Tags      []string `json:"tags"`
	Links     []string `json:"links"`
	Backlinks []string `json:"backlinks,omitempty"`
	Tasks     []Task   `json:"tasks,omitempty"`
}

func RenderMarkdown(markdown string) Result {
	lines := strings.Split(markdown, "\n")
	var out strings.Builder
	var paragraph []string
	inCode := false
	inList := false

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		fmt.Fprintf(&out, "<p>%s</p>\n", renderInline(strings.Join(paragraph, " ")))
		paragraph = nil
	}

	flushList := func() {
		if inList {
			out.WriteString("</ul>\n")
			inList = false
		}
	}

	linksSet := map[string]struct{}{}
	tagsSet := map[string]struct{}{}
	var tasks []Task

	for _, match := range wikiLinkPattern.FindAllStringSubmatch(markdown, -1) {
		linksSet[normalizeWikiPage(match[1])] = struct{}{}
	}
	for _, match := range tagPattern.FindAllStringSubmatch(markdown, -1) {
		tagsSet[match[2]] = struct{}{}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flushParagraph()
			flushList()
			if inCode {
				out.WriteString("</code></pre>\n")
			} else {
				out.WriteString("<pre><code>")
			}
			inCode = !inCode
			continue
		}
		if inCode {
			out.WriteString(html.EscapeString(line))
			out.WriteString("\n")
			continue
		}
		if trimmed == "" {
			flushParagraph()
			flushList()
			continue
		}
		if level := headingLevel(trimmed); level > 0 {
			flushParagraph()
			flushList()
			content := strings.TrimSpace(trimmed[level:])
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, renderInline(content), level)
			continue
		}
		if text, checked, ok := parseTask(trimmed); ok {
			tasks = append(tasks, Task{Text: text, Checked: checked})
			flushParagraph()
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			state := ""
			if checked {
				state = " checked"
			}
			fmt.Fprintf(&out, "<li><input type=\"checkbox\" disabled%s /> %s</li>\n", state, renderInline(text))
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushParagraph()
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			fmt.Fprintf(&out, "<li>%s</li>\n", renderInline(strings.TrimSpace(trimmed[2:])))
			continue
		}
		flushList()
		paragraph = append(paragraph, trimmed)
	}
	flushParagraph()
	flushList()
	if inCode {
		out.WriteString("</code></pre>\n")
	}

	return Result{
		HTML:  out.String(),
		Tags:  sortedKeys(tagsSet),
		Links: sortedKeys(linksSet),
		Tasks: tasks,
	}
}

func RenderInline(text string) string {
	return renderInline(text)
}

func renderInline(text string) string {
	escaped := html.EscapeString(text)
	escaped = wikiLinkPattern.ReplaceAllStringFunc(escaped, func(match string) string {
		parts := wikiLinkPattern.FindStringSubmatch(html.UnescapeString(match))
		if len(parts) == 0 {
			return match
		}
		target := normalizeWikiPage(parts[1])
		label := parts[1]
		if parts[2] != "" {
			label = parts[2]
		}
		return fmt.Sprintf(`<a href="/w/%s" data-wiki-link="%s">%s</a>`, html.EscapeString(target), html.EscapeString(target), html.EscapeString(label))
	})
	escaped = mdLinkPattern.ReplaceAllString(escaped, `<a href="$2">$1</a>`)
	return escaped
}

func normalizeWikiPage(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.ReplaceAll(name, " ", "-")
	return strings.ToLower(name)
}

func headingLevel(line string) int {
	level := 0
	for _, r := range line {
		if r == '#' {
			level++
			continue
		}
		break
	}
	if level == 0 || level > 6 || len(line) <= level || line[level] != ' ' {
		return 0
	}
	return level
}

func parseTask(line string) (string, bool, bool) {
	if strings.HasPrefix(line, "- [ ] ") || strings.HasPrefix(line, "* [ ] ") {
		return strings.TrimSpace(line[6:]), false, true
	}
	if strings.HasPrefix(line, "- [x] ") || strings.HasPrefix(line, "* [x] ") || strings.HasPrefix(line, "- [X] ") || strings.HasPrefix(line, "* [X] ") {
		return strings.TrimSpace(line[6:]), true, true
	}
	return "", false, false
}

func sortedKeys[T any](set map[string]T) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	if len(out) < 2 {
		return out
	}
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
