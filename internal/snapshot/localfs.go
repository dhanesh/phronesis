package snapshot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/fsutil"
)

// LocalFSTarget writes snapshots to a timestamped directory under a root path.
// Directory layout:
//
//	<root>/<workspaceID>/<RFC3339-compact>/
//	    manifest.json       (metadata + file index)
//	    md/<path>           (markdown corpus, directory-preserving)
//	    blobs/<hash>        (blob bytes, flat)
//
// Satisfies: RT-4.3 adjacent, O7, O6 (manifest + content use atomic write).
type LocalFSTarget struct {
	root string
}

// NewLocalFSTarget ensures root exists and returns a Target.
func NewLocalFSTarget(root string) (*LocalFSTarget, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &LocalFSTarget{root: root}, nil
}

type manifestFile struct {
	Version     int       `json:"version"`
	WorkspaceID string    `json:"workspace_id"`
	At          time.Time `json:"at"`
	MarkdownN   int       `json:"markdown_n"`
	BlobN       int       `json:"blob_n"`
}

func (t *LocalFSTarget) Store(ctx context.Context, s Snapshot) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if s.WorkspaceID == "" {
		return Info{}, errors.New("snapshot: WorkspaceID required")
	}

	// Compact, sort-friendly timestamp: 20260418T124800Z
	stamp := s.At.UTC().Format("20060102T150405Z")
	dir := filepath.Join(t.root, safe(s.WorkspaceID), stamp)
	if err := os.MkdirAll(filepath.Join(dir, "md"), 0o755); err != nil {
		return Info{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return Info{}, err
	}

	// Markdown files preserve path structure under md/.
	var totalBytes int64
	for path, content := range s.Markdown {
		dest := filepath.Join(dir, "md", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return Info{}, err
		}
		if err := atomicWrite(dest, content); err != nil {
			return Info{}, err
		}
		totalBytes += int64(len(content))
	}

	// Blobs are stored flat under blobs/ keyed by hash.
	for hash, bytes := range s.Blobs {
		if !validHexHash(hash) {
			return Info{}, fmt.Errorf("snapshot: invalid blob hash %q", hash)
		}
		dest := filepath.Join(dir, "blobs", hash)
		if err := atomicWrite(dest, bytes); err != nil {
			return Info{}, err
		}
		totalBytes += int64(len(bytes))
	}

	// Manifest last; its presence signals a completed snapshot.
	m := manifestFile{
		Version:     1,
		WorkspaceID: s.WorkspaceID,
		At:          s.At,
		MarkdownN:   len(s.Markdown),
		BlobN:       len(s.Blobs),
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Info{}, err
	}
	if err := atomicWrite(filepath.Join(dir, "manifest.json"), mb); err != nil {
		return Info{}, err
	}

	return Info{
		ID:          filepath.Join(safe(s.WorkspaceID), stamp),
		WorkspaceID: s.WorkspaceID,
		At:          s.At,
		Size:        totalBytes + int64(len(mb)),
	}, nil
}

func (t *LocalFSTarget) List(ctx context.Context, workspaceID string) ([]Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wsDir := filepath.Join(t.root, safe(workspaceID))
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mPath := filepath.Join(wsDir, e.Name(), "manifest.json")
		mb, err := os.ReadFile(mPath)
		if err != nil {
			continue // incomplete snapshot
		}
		var m manifestFile
		if err := json.Unmarshal(mb, &m); err != nil {
			continue
		}
		info, _ := e.Info()
		var size int64 = -1
		if info != nil {
			size = dirSize(filepath.Join(wsDir, e.Name()))
		}
		out = append(out, Info{
			ID:          filepath.Join(safe(workspaceID), e.Name()),
			WorkspaceID: workspaceID,
			At:          m.At,
			Size:        size,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (t *LocalFSTarget) Restore(ctx context.Context, id string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	dir := filepath.Join(t.root, filepath.FromSlash(id))
	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	var m manifestFile
	if err := json.Unmarshal(mb, &m); err != nil {
		return Snapshot{}, err
	}

	s := Snapshot{
		WorkspaceID: m.WorkspaceID,
		At:          m.At,
		Markdown:    make(map[string][]byte),
		Blobs:       make(map[string][]byte),
	}

	mdRoot := filepath.Join(dir, "md")
	_ = filepath.WalkDir(mdRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(mdRoot, path)
		if err != nil {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		s.Markdown[filepath.ToSlash(rel)] = content
		return nil
	})

	blobRoot := filepath.Join(dir, "blobs")
	entries, _ := os.ReadDir(blobRoot)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		bytes, err := os.ReadFile(filepath.Join(blobRoot, e.Name()))
		if err != nil {
			continue
		}
		s.Blobs[e.Name()] = bytes
	}
	return s, nil
}

func (t *LocalFSTarget) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(t.root, filepath.FromSlash(id))
	err := os.RemoveAll(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// atomicWrite delegates to the shared fsutil.AtomicWrite (INT-12 dedup).
// Kept as a package-private trampoline to minimize call-site churn; new
// code should prefer calling fsutil.AtomicWrite directly.
//
// Satisfies: O6
func atomicWrite(path string, data []byte) error {
	return fsutil.AtomicWrite(path, data, 0o644)
}

// safe sanitizes a workspace id for use as a path segment.
func safe(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	id = strings.ReplaceAll(id, "..", "_")
	if id == "" {
		return "_"
	}
	return id
}

func validHexHash(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func dirSize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}
