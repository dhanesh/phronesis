// Package fsutil provides small filesystem primitives used by multiple
// packages in phronesis.
//
// The central utility is AtomicWrite: a POSIX-safe "write a file such that
// readers never see a partial result" pattern. Used by internal/wiki (page
// storage) and internal/snapshot (manifest + content) to satisfy O6 of the
// collab-wiki manifold.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path via the tempfile-rename dance:
//
//  1. Create a tempfile in the SAME directory as path (so os.Rename is atomic).
//  2. Write data to the tempfile.
//  3. fsync the tempfile (data on disk, not just in the page cache).
//  4. Rename tempfile -> path (atomic on POSIX and Windows replace-ok).
//  5. fsync the parent directory (so the rename itself is durable).
//
// On any error along the way, the tempfile is removed. The destination path
// is untouched unless step 4 succeeded, so readers never see a partial file.
//
// Satisfies: O6 (atomic disk writes before ACK)
//
// Callers should pass the same perm they would give to os.WriteFile; it only
// applies to newly-created destination files (rename does not alter the
// mode of an existing path).
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return errors.New("fsutil: empty path")
	}
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("fsutil: create temp: %w", err)
	}
	tmpName := tmp.Name()

	// If anything below fails, remove the tempfile. (os.Remove is a no-op
	// after a successful rename.)
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsutil: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("fsutil: rename: %w", err)
	}
	cleanup = false // rename succeeded; tmpName no longer exists

	// Best-effort directory fsync for rename durability. Some filesystems
	// and Windows do not support directory sync; errors here are not fatal
	// because the rename itself already succeeded.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// AtomicWriteReader streams data from r to path using the same tempfile-rename
// pattern as AtomicWrite. Intended for cases where the caller has a Reader
// (e.g., a large uploaded blob) and wants to avoid buffering the whole payload
// in memory.
//
// Satisfies: O6
func AtomicWriteReader(path string, r io.Reader, perm os.FileMode) error {
	if path == "" {
		return errors.New("fsutil: empty path")
	}
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("fsutil: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: copy temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsutil: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fsutil: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("fsutil: rename: %w", err)
	}
	cleanup = false

	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
