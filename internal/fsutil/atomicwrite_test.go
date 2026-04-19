package fsutil

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// @constraint O6
// AtomicWrite creates the file at path and the content matches input.
func TestAtomicWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	content := []byte("hello, world")

	if err := AtomicWrite(path, content, 0o644); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content: got %q, want %q", got, content)
	}
}

// @constraint O6
// AtomicWrite replaces an existing file entirely; no trace of prior content.
func TestAtomicWriteReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.md")

	if err := os.WriteFile(path, []byte("OLD CONTENT TO BE OVERWRITTEN"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := AtomicWrite(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content: got %q, want 'new'", got)
	}
}

// @constraint O6
// After a successful AtomicWrite, no .tmp-* files remain in the target dir.
// This is the crash-recovery promise: readers never see a tempfile.
func TestAtomicWriteLeavesNoTempfiles(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "f", "x.md")
		_ = os.MkdirAll(filepath.Join(dir, "f"), 0o755)
		if err := AtomicWrite(path, []byte{byte(i)}, 0o644); err != nil {
			t.Fatalf("AtomicWrite %d: %v", i, err)
		}
	}
	count := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".tmp-") {
			count++
		}
		return nil
	})
	if count != 0 {
		t.Errorf("left %d tempfiles behind after successful writes", count)
	}
}

// @constraint O6
// Empty path is rejected explicitly (not silently writing to cwd).
func TestAtomicWriteRejectsEmptyPath(t *testing.T) {
	if err := AtomicWrite("", []byte("x"), 0o644); err == nil {
		t.Error("expected error for empty path, got nil")
	}
}

// @constraint O6
// Write to a non-existent directory returns an error; destination is not
// created partially (can't happen — os.CreateTemp fails before any rename).
func TestAtomicWriteFailsIfParentDirMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does", "not", "exist", "x.md")
	if err := AtomicWrite(path, []byte("x"), 0o644); err == nil {
		t.Error("expected error for missing parent dir, got nil")
	}
}

// @constraint O6
// AtomicWriteReader streams content to disk atomically (same contract as
// AtomicWrite). Useful for large blob uploads that shouldn't buffer in memory.
func TestAtomicWriteReaderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.bin")

	src := bytes.NewReader([]byte("streamed-content-bytes"))
	if err := AtomicWriteReader(path, src, 0o644); err != nil {
		t.Fatalf("AtomicWriteReader: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "streamed-content-bytes" {
		t.Errorf("content mismatch: %q", got)
	}
}

// @constraint O6
// AtomicWrite sets the file mode correctly.
func TestAtomicWriteHonorsPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")

	if err := AtomicWrite(path, []byte("hi"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// Mask off higher bits (e.g., setgid inheritance on some FS) for portability.
	gotPerm := info.Mode().Perm()
	if gotPerm != 0o600 {
		t.Errorf("perm: got %o, want 0600", gotPerm)
	}
}
