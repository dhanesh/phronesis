package wiki

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/fsutil"
	"github.com/dhanesh/phronesis/internal/render"
	"github.com/dhanesh/phronesis/internal/xssdefense"
)

type Store struct {
	root string
	mu   sync.RWMutex
	// pageLinks maps page name -> outgoing wiki-link targets. Maintained
	// in lockstep with the on-disk state: built from the FS in NewStore,
	// refreshed in Put. backlinksLocked walks this map (cheap) instead of
	// re-reading + re-rendering every .md file on disk on each Get.
	pageLinks map[string][]string
	// pageTags maps page name -> hashtags it contains (lowercased, no `#`
	// prefix). Maintained alongside pageLinks. Drives PagesByTag — for a
	// page named `urgent`, the API returns the names of pages that have
	// `#urgent` in their content, so navigating to /w/urgent acts as a
	// synthetic tag-index page.
	pageTags map[string][]string
}

type Page struct {
	Name      string        `json:"name"`
	Content   string        `json:"content"`
	Version   int64         `json:"version"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Render    render.Result `json:"render"`
	// Tagged lists pages whose markdown content contains `#<this page's
	// name>` as a hashtag. Lets the frontend render /w/<tag> as a
	// synthetic index of pages tagged with that tag. Always non-nil per
	// the JSON-array-contract memory; defaults to [].
	Tagged []string `json:"tagged"`
}

type Summary struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		root:      root,
		pageLinks: make(map[string][]string),
		pageTags:  make(map[string][]string),
	}
	if err := s.rebuildLinkGraph(); err != nil {
		return nil, fmt.Errorf("build link graph: %w", err)
	}
	return s, nil
}

func (s *Store) Get(name string) (Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readPage(name)
}

func (s *Store) Put(name, content string) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.pagePath(name)
	if err != nil {
		return Page{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Page{}, err
	}
	// INT-15 / RT-9.1 / S7: strip the most dangerous HTML tags + event
	// handlers + javascript: URLs at store time. Defense layer 1 — layer 2
	// is xssdefense.SanitizeHTML at render time (integrated separately).
	// Safe markdown content is unchanged by this call.
	safeContent := xssdefense.StripDangerousTags(content)
	// INT-12 / O6: atomic write (tempfile + fsync + rename + dir fsync) so
	// concurrent readers never see a partial page. The old os.WriteFile path
	// could leave a truncated file on crash during write.
	if err := fsutil.AtomicWrite(path, []byte(safeContent), 0o644); err != nil {
		return Page{}, err
	}

	// Refresh the link-graph and tag-graph entries for this page so
	// subsequent Gets serve fresh backlinks and tagged-page lists.
	// Rendering safeContent here matches what's on disk (AtomicWrite is
	// durable), avoiding a second os.ReadFile.
	normalized := normalizeName(name)
	rendered := render.RenderMarkdown(safeContent)
	s.pageLinks[normalized] = rendered.Links
	s.pageTags[normalized] = rendered.Tags

	return s.readPage(name)
}

func (s *Store) List() ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var pages []Summary
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		name, err := s.pageName(path)
		if err != nil {
			return err
		}
		pages = append(pages, Summary{Name: name, UpdatedAt: info.ModTime()})
		return nil
	})
	return pages, err
}

// Backlinks returns the names of pages that link to target. Reads the
// in-memory link graph; no disk I/O.
func (s *Store) Backlinks(target string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backlinksLocked(target), nil
}

// PagesByTag returns the names of pages whose content contains
// `#<tag>` as a hashtag. Reads the in-memory tag graph.
func (s *Store) PagesByTag(tag string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pagesByTagLocked(tag), nil
}

func (s *Store) readPage(name string) (Page, error) {
	normalized := normalizeName(name)
	path, err := s.pagePath(normalized)
	if err != nil {
		return Page{}, err
	}
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			emptyResult := render.RenderMarkdown("")
			emptyResult.Backlinks = s.backlinksLocked(normalized)
			return Page{
				Name:      normalized,
				Content:   "",
				Version:   0,
				UpdatedAt: time.Time{},
				Render:    emptyResult,
				Tagged:    s.pagesByTagLocked(normalized),
			}, nil
		}
		return Page{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Page{}, err
	}
	result := render.RenderMarkdown(string(buf))
	// Defense layer 2 (RT-9.2): the renderer escapes its own output, but any
	// raw HTML the user pasted into a markdown source can still flow through
	// untouched. SanitizeHTML drops non-allowlisted tags and re-strips event
	// handlers + javascript: URLs. StripDangerousTags at Put time is layer 1.
	result.HTML = xssdefense.SanitizeHTML(result.HTML)
	result.Backlinks = s.backlinksLocked(normalized)
	return Page{
		Name:      normalized,
		Content:   string(buf),
		Version:   info.ModTime().UnixMilli(),
		UpdatedAt: info.ModTime().UTC(),
		Render:    result,
		Tagged:    s.pagesByTagLocked(normalized),
	}, nil
}

// backlinksLocked walks the in-memory link graph for pages that target this
// page name. Caller MUST hold s.mu (read or write).
func (s *Store) backlinksLocked(target string) []string {
	target = normalizeName(target)
	out := []string{}
	for page, links := range s.pageLinks {
		if page == target {
			continue
		}
		if slices.Contains(links, target) {
			out = append(out, page)
		}
	}
	slices.Sort(out)
	return out
}

// pagesByTagLocked walks the in-memory tag graph for pages whose
// content contains `#<tag>`. Caller MUST hold s.mu (read or write).
// The tag is normalised the same way render.RenderMarkdown extracts
// tags (lowercased, stripped of the leading `#`).
func (s *Store) pagesByTagLocked(tag string) []string {
	tag = strings.ToLower(strings.TrimPrefix(tag, "#"))
	tag = normalizeName(tag)
	out := []string{}
	for page, tags := range s.pageTags {
		if page == tag {
			// A page named `urgent` would otherwise list itself if it
			// also mentions `#urgent` — skip self.
			continue
		}
		if slices.Contains(tags, tag) {
			out = append(out, page)
		}
	}
	slices.Sort(out)
	return out
}

// rebuildLinkGraph walks the page tree once and caches each page's outgoing
// links + hashtags. Called from NewStore so the very first Get already
// serves backlinks and tagged-page lists from memory. Takes s.mu for writes.
func (s *Store) rebuildLinkGraph() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageLinks = make(map[string][]string)
	s.pageTags = make(map[string][]string)
	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		name, err := s.pageName(path)
		if err != nil {
			return err
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rendered := render.RenderMarkdown(string(buf))
		s.pageLinks[name] = rendered.Links
		s.pageTags[name] = rendered.Tags
		return nil
	})
}

func (s *Store) pagePath(name string) (string, error) {
	name = normalizeName(name)
	if name == "" {
		name = "home"
	}
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", errors.New("invalid page name")
	}
	return filepath.Join(s.root, clean+".md"), nil
}

func (s *Store) pageName(path string) (string, error) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", err
	}
	rel = strings.TrimSuffix(rel, ".md")
	return normalizeName(filepath.ToSlash(rel)), nil
}

func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(filepath.ToSlash(name))
	if name == "" {
		return "home"
	}
	return name
}
