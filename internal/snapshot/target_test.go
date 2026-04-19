package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// @constraint RT-4.3 O7 TN4
// Store + Restore must round-trip markdown AND blobs. Missing either signals
// a TN4 regression (snapshot would silently lose media).
func TestLocalFSStoreRestoreRoundTrip(t *testing.T) {
	target, err := NewLocalFSTarget(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalFSTarget: %v", err)
	}
	ctx := context.Background()

	original := Snapshot{
		WorkspaceID: "ws-alpha",
		At:          time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
		Markdown: map[string][]byte{
			"home.md":          []byte("# Home\n\nWelcome."),
			"notes/daily.md":   []byte("- item 1\n- item 2\n"),
			"deep/very/nest.md": []byte("deep"),
		},
		Blobs: map[string][]byte{
			"abc123deadbeef": []byte("\x89PNG\r\n\x1a\n"),
			"ff00":            []byte("tiny blob"),
		},
	}

	info, err := target.Store(ctx, original)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if info.ID == "" {
		t.Error("Info.ID empty")
	}

	restored, err := target.Restore(ctx, info.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.WorkspaceID != original.WorkspaceID {
		t.Errorf("WorkspaceID: got %q, want %q", restored.WorkspaceID, original.WorkspaceID)
	}
	if len(restored.Markdown) != len(original.Markdown) {
		t.Errorf("Markdown count: got %d, want %d", len(restored.Markdown), len(original.Markdown))
	}
	for path, want := range original.Markdown {
		got, ok := restored.Markdown[path]
		if !ok {
			t.Errorf("Markdown[%s] missing after restore", path)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("Markdown[%s]: got %q, want %q", path, got, want)
		}
	}
	for hash, want := range original.Blobs {
		got, ok := restored.Blobs[hash]
		if !ok {
			t.Errorf("Blobs[%s] missing after restore (TN4 regression)", hash)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("Blobs[%s] content mismatch", hash)
		}
	}
}

// @constraint RT-4.3 O7
// List surfaces completed snapshots sorted by time.
func TestLocalFSList(t *testing.T) {
	target, _ := NewLocalFSTarget(t.TempDir())
	ctx := context.Background()

	for i, when := range []time.Time{
		time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC),
	} {
		_, err := target.Store(ctx, Snapshot{
			WorkspaceID: "ws-list",
			At:          when,
			Markdown:    map[string][]byte{"x.md": []byte{byte(i)}},
		})
		if err != nil {
			t.Fatalf("Store %d: %v", i, err)
		}
	}

	got, err := target.List(ctx, "ws-list")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List len: got %d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].At.After(got[i].At) {
			t.Errorf("List not sorted ascending: %v before %v", got[i-1].At, got[i].At)
		}
	}
}

// @constraint RT-4.3
// Restore of unknown id returns ErrNotFound.
func TestLocalFSRestoreMissing(t *testing.T) {
	target, _ := NewLocalFSTarget(t.TempDir())
	_, err := target.Restore(context.Background(), "nonexistent/path")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// @constraint RT-4.3 O6
// atomicWrite must not leave a tempfile on success.
func TestAtomicWriteCleansUp(t *testing.T) {
	dir := t.TempDir()
	target, _ := NewLocalFSTarget(dir)
	_, err := target.Store(context.Background(), Snapshot{
		WorkspaceID: "ws-atomic",
		At:          time.Now().UTC(),
		Markdown:    map[string][]byte{"a.md": []byte("content")},
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	// There should be no .tmp-* files under the root after a successful store.
	count := 0
	_ = walkRecursive(dir, func(path string) {
		if hasTmpPrefix(path) {
			count++
		}
	})
	if count > 0 {
		t.Errorf("leftover tempfiles: %d", count)
	}
}

// @constraint RT-4.3
// Invalid blob hash is rejected at Store (content-addressed correctness).
func TestLocalFSRejectsInvalidBlobHash(t *testing.T) {
	target, _ := NewLocalFSTarget(t.TempDir())
	_, err := target.Store(context.Background(), Snapshot{
		WorkspaceID: "ws-bad",
		At:          time.Now().UTC(),
		Blobs:       map[string][]byte{"NOT-HEX!!": []byte("x")},
	})
	if err == nil {
		t.Error("Store accepted invalid hex blob hash; want error")
	}
}

// --- helpers ---

func walkRecursive(root string, fn func(string)) error {
	entries, err := readDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := root + "/" + e.Name()
		if e.IsDir() {
			_ = walkRecursive(full, fn)
			continue
		}
		fn(full)
	}
	return nil
}

func hasTmpPrefix(path string) bool {
	// filepath separator-aware basename check.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			base := path[i+1:]
			return len(base) > 4 && base[:4] == ".tmp"
		}
	}
	return len(path) > 4 && path[:4] == ".tmp"
}
