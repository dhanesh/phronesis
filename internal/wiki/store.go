package wiki

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
}

type Page struct {
	Name      string        `json:"name"`
	Content   string        `json:"content"`
	Version   int64         `json:"version"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Render    render.Result `json:"render"`
}

type Summary struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
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

func (s *Store) Backlinks(target string) ([]string, error) {
	summaries, err := s.List()
	if err != nil {
		return nil, err
	}
	target = normalizeName(target)
	var backlinks []string
	for _, summary := range summaries {
		page, err := s.Get(summary.Name)
		if err != nil {
			return nil, err
		}
		for _, link := range page.Render.Links {
			if link == target {
				backlinks = append(backlinks, summary.Name)
				break
			}
		}
	}
	return backlinks, nil
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
			return Page{
				Name:      normalized,
				Content:   "",
				Version:   0,
				UpdatedAt: time.Time{},
				Render:    render.RenderMarkdown(""),
			}, nil
		}
		return Page{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Page{}, err
	}
	result := render.RenderMarkdown(string(buf))
	backlinks, err := s.BacklinksWithoutLock(normalized)
	if err != nil {
		return Page{}, err
	}
	result.Backlinks = backlinks
	return Page{
		Name:      normalized,
		Content:   string(buf),
		Version:   info.ModTime().UnixMilli(),
		UpdatedAt: info.ModTime().UTC(),
		Render:    result,
	}, nil
}

func (s *Store) BacklinksWithoutLock(target string) ([]string, error) {
	var backlinks []string
	err := filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
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
		if name == target {
			return nil
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result := render.RenderMarkdown(string(buf))
		for _, link := range result.Links {
			if link == target {
				backlinks = append(backlinks, name)
				break
			}
		}
		return nil
	})
	return backlinks, err
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
