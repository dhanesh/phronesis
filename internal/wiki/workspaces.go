package wiki

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/dhanesh/phronesis/internal/fsutil"
)

// DefaultWorkspaceSlug names the workspace pages were rooted under
// before multi-workspace support landed. The migration in
// NewWorkspaces moves any pre-existing flat data/pages/*.md files
// into data/pages/default/*.md so that historical content keeps
// working without an admin rebuild.
const DefaultWorkspaceSlug = "default"

// slugRegex limits workspace slugs to lowercase alphanumeric + hyphens,
// 1-63 characters, with alphanumeric at both ends (no leading or
// trailing hyphens). Conservative on purpose — slugs become URL path
// segments and on-disk directory names.
var slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateSlug returns an error if slug is not a safe workspace
// identifier. Used by Create() and the admin HTTP handler.
func ValidateSlug(slug string) error {
	if !slugRegex.MatchString(slug) {
		return errors.New("workspace slug must be 1-63 chars: lowercase letters/digits/hyphens, starting with letter or digit")
	}
	return nil
}

// WorkspaceMeta is the JSON shape returned to clients and persisted
// in data/workspaces.json. New fields added here MUST default to
// zero-values so existing on-disk metadata files keep deserialising.
type WorkspaceMeta struct {
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// workspacesFile is the persisted structure on disk.
type workspacesFile struct {
	Version    int             `json:"version"`
	Workspaces []WorkspaceMeta `json:"workspaces"`
}

// workspaceState bundles the per-workspace runtime objects so callers
// can fetch all three (meta + store + hub) in one map lookup.
type workspaceState struct {
	meta  WorkspaceMeta
	store *Store
	hub   *Hub
}

// Workspaces owns the per-workspace lifecycle: metadata file, on-disk
// page directories, page Stores, and live-document Hubs. The Server
// holds a single *Workspaces and routes per-request page operations
// through Get(slug).
type Workspaces struct {
	pagesRoot string
	metaPath  string

	mu     sync.RWMutex
	states map[string]*workspaceState
}

// NewWorkspaces opens the multi-workspace store rooted at pagesRoot
// with metadata at metaPath. On first boot it migrates any pre-existing
// flat pages into the default workspace and seeds workspaces.json with
// just the default entry. Idempotent on subsequent boots.
func NewWorkspaces(pagesRoot, metaPath string) (*Workspaces, error) {
	if err := os.MkdirAll(pagesRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create pages root: %w", err)
	}
	if err := migrateFlatPages(pagesRoot); err != nil {
		return nil, fmt.Errorf("migrate flat pages: %w", err)
	}
	metas, err := loadOrSeedMeta(metaPath, pagesRoot)
	if err != nil {
		return nil, fmt.Errorf("load workspace metadata: %w", err)
	}

	ws := &Workspaces{
		pagesRoot: pagesRoot,
		metaPath:  metaPath,
		states:    make(map[string]*workspaceState, len(metas)),
	}
	for _, meta := range metas {
		if err := ws.openLocked(meta); err != nil {
			return nil, fmt.Errorf("open workspace %q: %w", meta.Slug, err)
		}
	}
	return ws, nil
}

// openLocked opens the per-workspace Store + Hub for the given meta
// and registers it in the states map. Callers must hold the write
// lock OR be in initial construction (where no concurrency exists).
func (ws *Workspaces) openLocked(meta WorkspaceMeta) error {
	root := filepath.Join(ws.pagesRoot, meta.Slug)
	store, err := NewStore(root)
	if err != nil {
		return err
	}
	ws.states[meta.Slug] = &workspaceState{
		meta:  meta,
		store: store,
		hub:   NewHub(store),
	}
	return nil
}

// Get returns the per-workspace Store + Hub. If the slug is not
// registered the third return value is false and callers should
// surface a 404.
func (ws *Workspaces) Get(slug string) (*Store, *Hub, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	state, ok := ws.states[slug]
	if !ok {
		return nil, nil, false
	}
	return state.store, state.hub, true
}

// List returns every registered workspace, sorted by slug. The
// returned slice is a fresh copy so callers can safely range over it
// without holding the lock.
func (ws *Workspaces) List() []WorkspaceMeta {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	out := make([]WorkspaceMeta, 0, len(ws.states))
	for _, state := range ws.states {
		out = append(out, state.meta)
	}
	sortMetas(out)
	return out
}

// Create registers a new workspace, creates its on-disk page directory,
// and persists the new metadata. Returns ErrWorkspaceExists if the slug
// is already taken. Slug is validated via ValidateSlug.
func (ws *Workspaces) Create(slug, name string) (WorkspaceMeta, error) {
	if err := ValidateSlug(slug); err != nil {
		return WorkspaceMeta{}, err
	}
	if name == "" {
		name = slug
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if _, ok := ws.states[slug]; ok {
		return WorkspaceMeta{}, ErrWorkspaceExists
	}
	meta := WorkspaceMeta{
		Slug:      slug,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	if err := ws.openLocked(meta); err != nil {
		// Roll back the on-disk directory if Store init failed.
		_ = os.RemoveAll(filepath.Join(ws.pagesRoot, slug))
		return WorkspaceMeta{}, err
	}
	if err := ws.persistMetaLocked(); err != nil {
		// Persist failed — undo the in-memory + disk state.
		delete(ws.states, slug)
		_ = os.RemoveAll(filepath.Join(ws.pagesRoot, slug))
		return WorkspaceMeta{}, err
	}
	return meta, nil
}

// Delete removes a workspace. The default workspace cannot be deleted
// (returns ErrCannotDeleteDefault). The on-disk page directory is
// removed; pages inside are gone.
func (ws *Workspaces) Delete(slug string) error {
	if slug == DefaultWorkspaceSlug {
		return ErrCannotDeleteDefault
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if _, ok := ws.states[slug]; !ok {
		return ErrWorkspaceNotFound
	}
	delete(ws.states, slug)
	if err := ws.persistMetaLocked(); err != nil {
		return err
	}
	// Best-effort directory cleanup. If this fails the metadata is
	// authoritative; the orphaned directory will be re-attached on
	// next boot via auto-discovery in loadOrSeedMeta.
	return os.RemoveAll(filepath.Join(ws.pagesRoot, slug))
}

// persistMetaLocked rewrites workspaces.json from the current in-memory
// state. Caller must hold the write lock. Uses fsutil.AtomicWrite so a
// concurrent crash never leaves a partial file.
func (ws *Workspaces) persistMetaLocked() error {
	metas := make([]WorkspaceMeta, 0, len(ws.states))
	for _, state := range ws.states {
		metas = append(metas, state.meta)
	}
	sortMetas(metas)
	body := workspacesFile{Version: 1, Workspaces: metas}
	buf, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ws.metaPath), 0o755); err != nil {
		return err
	}
	return fsutil.AtomicWrite(ws.metaPath, buf, 0o644)
}

var (
	ErrWorkspaceExists     = errors.New("workspace already exists")
	ErrWorkspaceNotFound   = errors.New("workspace not found")
	ErrCannotDeleteDefault = errors.New("cannot delete the default workspace")
)

// sortMetas sorts workspace metas by slug in place. Default is pinned
// to the front so UI lists always show it first regardless of slug
// alphabetics.
func sortMetas(metas []WorkspaceMeta) {
	// Lightweight sort; len typically small (1-50 workspaces).
	for i := 0; i < len(metas)-1; i++ {
		for j := i + 1; j < len(metas); j++ {
			if cmpMeta(metas[j], metas[i]) {
				metas[i], metas[j] = metas[j], metas[i]
			}
		}
	}
}

func cmpMeta(a, b WorkspaceMeta) bool {
	// default first, then alphabetical by slug.
	if a.Slug == DefaultWorkspaceSlug && b.Slug != DefaultWorkspaceSlug {
		return true
	}
	if b.Slug == DefaultWorkspaceSlug {
		return false
	}
	return a.Slug < b.Slug
}

// migrateFlatPages moves any flat data/pages/*.md files into
// data/pages/default/*.md. Runs once per boot; idempotent — if no flat
// .md files exist (already migrated, or empty pagesRoot) it returns
// nil with no side effects.
func migrateFlatPages(pagesRoot string) error {
	entries, err := os.ReadDir(pagesRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var flatFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".md" {
			flatFiles = append(flatFiles, e.Name())
		}
	}
	if len(flatFiles) == 0 {
		return nil
	}
	defaultDir := filepath.Join(pagesRoot, DefaultWorkspaceSlug)
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		return err
	}
	for _, f := range flatFiles {
		src := filepath.Join(pagesRoot, f)
		dst := filepath.Join(defaultDir, f)
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// loadOrSeedMeta reads workspaces.json. If the file is missing it
// auto-seeds from filesystem state: every subdirectory under pagesRoot
// becomes a workspace, plus an explicit default entry if missing.
// Auto-discovery on subsequent boots picks up directories created
// out-of-band.
func loadOrSeedMeta(metaPath, pagesRoot string) ([]WorkspaceMeta, error) {
	stored, err := readMetaFile(metaPath)
	if err != nil {
		return nil, err
	}

	// Set of slugs already in the persisted file.
	known := make(map[string]bool, len(stored))
	for _, m := range stored {
		known[m.Slug] = true
	}

	// Ensure default exists.
	if !known["default"] {
		stored = append(stored, WorkspaceMeta{
			Slug:      DefaultWorkspaceSlug,
			Name:      "Default",
			CreatedAt: time.Now().UTC(),
		})
		known[DefaultWorkspaceSlug] = true
	}

	// Auto-discover directories that aren't in metadata yet (attaches
	// orphans from a partially-failed Delete or out-of-band file moves).
	entries, err := os.ReadDir(pagesRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		if known[slug] {
			continue
		}
		if err := ValidateSlug(slug); err != nil {
			// Skip unrecognisable directory names — could be a stash dir
			// or other tooling artefact.
			continue
		}
		stored = append(stored, WorkspaceMeta{
			Slug:      slug,
			Name:      slug,
			CreatedAt: time.Now().UTC(),
		})
		known[slug] = true
	}
	sortMetas(stored)
	return stored, nil
}

func readMetaFile(metaPath string) ([]WorkspaceMeta, error) {
	buf, err := os.ReadFile(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var body workspacesFile
	if err := json.Unmarshal(buf, &body); err != nil {
		return nil, fmt.Errorf("parse %s: %w", metaPath, err)
	}
	return body.Workspaces, nil
}
