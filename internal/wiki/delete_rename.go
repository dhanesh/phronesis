package wiki

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrPageNotFound is returned when deleting or renaming a page that has no
// file on disk.
var ErrPageNotFound = errors.New("wiki: page not found")

// ErrPageExists is returned when a rename target already exists.
var ErrPageExists = errors.New("wiki: target page already exists")

// Delete removes a page's Markdown file. Links that pointed at it now point
// at a missing page, which the wiki treats as a new editable page.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.pagePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrPageNotFound
		}
		return err
	}
	n := normalizeName(name)
	delete(s.pageLinks, n)
	delete(s.pageTags, n)
	return nil
}

// Rename moves a page's file to a new name. Inbound wiki links are not
// rewritten (they keep pointing at the old name).
func (s *Store) Rename(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.pagePath(from)
	if err != nil {
		return err
	}
	dst, err := s.pagePath(to)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return ErrPageNotFound
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrPageExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	f, t := normalizeName(from), normalizeName(to)
	s.pageLinks[t], s.pageTags[t] = s.pageLinks[f], s.pageTags[f]
	delete(s.pageLinks, f)
	delete(s.pageTags, f)
	return nil
}

// Delete removes a page and tells live subscribers ("deleted" event).
func (h *Hub) Delete(name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.Delete(name); err != nil {
		return err
	}
	h.endSessionLocked(normalizeName(name), Event{Type: "deleted", Page: Page{Name: normalizeName(name)}})
	return nil
}

// Rename moves a page and tells live subscribers of the old name
// ("renamed" event carrying the new page).
func (h *Hub) Rename(from, to string) (Page, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.Rename(from, to); err != nil {
		return Page{}, err
	}
	page, err := h.store.Get(to)
	if err != nil {
		return Page{}, err
	}
	h.endSessionLocked(normalizeName(from), Event{Type: "renamed", Page: page})
	return page, nil
}

func (h *Hub) endSessionLocked(name string, ev Event) {
	session, ok := h.sessions[name]
	if !ok {
		return
	}
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339)
	for ch := range session.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
	delete(h.sessions, name)
}
