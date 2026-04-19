package wiki

import (
	"sync"
	"time"
)

type Hub struct {
	store *Store

	mu       sync.Mutex
	sessions map[string]*DocumentSession
}

type DocumentSession struct {
	name        string
	content     string
	version     int64
	history     map[int64]string
	subscribers map[chan Event]struct{}
}

type Event struct {
	Type      string `json:"type"`
	Page      Page   `json:"page"`
	Author    string `json:"author,omitempty"`
	Merged    bool   `json:"merged,omitempty"`
	Timestamp string `json:"timestamp"`
}

func NewHub(store *Store) *Hub {
	return &Hub{
		store:    store,
		sessions: make(map[string]*DocumentSession),
	}
}

func (h *Hub) Snapshot(name string) (Page, error) {
	page, err := h.store.Get(name)
	if err != nil {
		return Page{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.ensureSession(page)
	return Page{
		Name:      page.Name,
		Content:   session.content,
		Version:   session.version,
		UpdatedAt: page.UpdatedAt,
		Render:    page.Render,
	}, nil
}

func (h *Hub) Subscribe(name string) (<-chan Event, func(), error) {
	page, err := h.Snapshot(name)
	if err != nil {
		return nil, nil, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.ensureSession(page)
	ch := make(chan Event, 8)
	session.subscribers[ch] = struct{}{}
	ch <- Event{Type: "snapshot", Page: page, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(session.subscribers, ch)
		close(ch)
	}
	return ch, cancel, nil
}

func (h *Hub) Apply(name string, baseVersion int64, content, author string) (Page, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	page, err := h.store.Get(name)
	if err != nil {
		return Page{}, false, err
	}
	session := h.ensureSession(page)

	merged := false
	next := content
	if baseVersion != session.version {
		base := session.history[baseVersion]
		next, merged = mergeContent(base, session.content, content)
	}

	updated, err := h.store.Put(name, next)
	if err != nil {
		return Page{}, false, err
	}
	session.content = updated.Content
	session.version = updated.Version
	session.history[updated.Version] = updated.Content
	if len(session.history) > 20 {
		for version := range session.history {
			if version != session.version {
				delete(session.history, version)
				break
			}
		}
	}

	event := Event{
		Type:      "update",
		Page:      updated,
		Author:    author,
		Merged:    merged,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	for ch := range session.subscribers {
		select {
		case ch <- event:
		default:
		}
	}

	return updated, merged, nil
}

func (h *Hub) ensureSession(page Page) *DocumentSession {
	session, ok := h.sessions[page.Name]
	if ok {
		if page.Version > session.version {
			session.content = page.Content
			session.version = page.Version
			session.history[page.Version] = page.Content
		}
		return session
	}

	session = &DocumentSession{
		name:        page.Name,
		content:     page.Content,
		version:     page.Version,
		history:     map[int64]string{page.Version: page.Content},
		subscribers: make(map[chan Event]struct{}),
	}
	h.sessions[page.Name] = session
	return session
}

func mergeContent(base, current, incoming string) (string, bool) {
	if incoming == current {
		return current, false
	}
	if base == "" || base == current {
		return incoming, false
	}
	if incoming == base {
		return current, true
	}

	prefix := sharedPrefixLen(base, incoming, current)
	suffix := sharedSuffixLen(base[prefix:], incoming[prefix:], current[prefix:])

	currentMid := sliceMiddle(current, prefix, suffix)
	incomingMid := sliceMiddle(incoming, prefix, suffix)
	if currentMid == incomingMid {
		return incoming, true
	}

	merged := current[:prefix] + currentMid + "\n" + incomingMid + current[len(current)-suffix:]
	return merged, true
}

func sharedPrefixLen(base, incoming, current string) int {
	limit := min3(len(base), len(incoming), len(current))
	i := 0
	for i < limit && base[i] == incoming[i] && base[i] == current[i] {
		i++
	}
	return i
}

func sharedSuffixLen(base, incoming, current string) int {
	limit := min3(len(base), len(incoming), len(current))
	i := 0
	for i < limit &&
		base[len(base)-1-i] == incoming[len(incoming)-1-i] &&
		base[len(base)-1-i] == current[len(current)-1-i] {
		i++
	}
	return i
}

func sliceMiddle(s string, prefix, suffix int) string {
	end := len(s) - suffix
	if prefix > end {
		return ""
	}
	return s[prefix:end]
}

func min3(a, b, c int) int {
	if a > b {
		a = b
	}
	if a > c {
		a = c
	}
	return a
}
