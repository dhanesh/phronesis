package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// @constraint RT-6 TN4 S6
// Put + Get round-trips content; computed hash matches ComputeHash of input.
func TestLocalFSPutGetRoundTrip(t *testing.T) {
	s, err := NewLocalFSStore(t.TempDir(), Config{})
	if err != nil {
		t.Fatalf("NewLocalFSStore: %v", err)
	}
	ctx := context.Background()

	data := []byte("\x89PNG\r\n\x1a\ntest-png-bytes")
	info, err := s.Put(ctx, "ws-1", bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if info.Hash != ComputeHash(data) {
		t.Errorf("hash: got %s, want %s", info.Hash, ComputeHash(data))
	}
	if info.Size != int64(len(data)) {
		t.Errorf("size: got %d, want %d", info.Size, len(data))
	}
	if info.ContentType != "image/png" {
		t.Errorf("content-type: got %q, want image/png", info.ContentType)
	}

	rc, got, err := s.Get(ctx, "ws-1", info.Hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !bytes.Equal(body, data) {
		t.Errorf("body mismatch after Get")
	}
	if got.ContentType != "image/png" {
		t.Errorf("Get content-type: got %q, want image/png", got.ContentType)
	}
}

// @constraint RT-6 S6
// Put rejects disallowed content types.
func TestLocalFSRejectsDisallowedContentType(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{})
	_, err := s.Put(context.Background(), "ws", strings.NewReader("x"), "text/html")
	if !errors.Is(err, ErrContentTypeDisallowed) {
		t.Errorf("expected ErrContentTypeDisallowed, got %v", err)
	}
}

// @constraint RT-6 S6
// Put enforces per-workspace quota.
func TestLocalFSEnforcesQuota(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{QuotaBytes: 100})
	ctx := context.Background()

	big := make([]byte, 200)
	_, err := s.Put(ctx, "ws", bytes.NewReader(big), "image/png")
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("expected ErrQuotaExceeded, got %v", err)
	}
}

// @constraint RT-6
// Get of unknown hash returns ErrNotFound.
func TestLocalFSGetMissing(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{})
	_, _, err := s.Get(context.Background(), "ws", "0000deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// @constraint RT-6 TN4
// Duplicate Put of identical content returns the same hash and does not
// double-count quota (content-addressing de-duplicates).
func TestLocalFSPutIsContentAddressed(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{QuotaBytes: 1024})
	ctx := context.Background()

	data := []byte("duplicate-content")
	i1, err := s.Put(ctx, "ws", bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	i2, err := s.Put(ctx, "ws", bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatalf("Put 2 (dedup): %v", err)
	}
	if i1.Hash != i2.Hash {
		t.Errorf("content-address broken: %s != %s", i1.Hash, i2.Hash)
	}
	usage, _ := s.Usage(ctx, "ws")
	if usage.BytesUsed != int64(len(data)) {
		t.Errorf("usage: got %d, want %d (dedup should not double-count)", usage.BytesUsed, len(data))
	}
}

// @constraint RT-6
// Usage reflects QuotaBytes from config.
func TestLocalFSUsageReportsQuota(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{QuotaBytes: 4096})
	u, err := s.Usage(context.Background(), "ws-empty")
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.QuotaBytes != 4096 {
		t.Errorf("QuotaBytes: got %d, want 4096", u.QuotaBytes)
	}
	if u.BytesUsed != 0 {
		t.Errorf("BytesUsed for empty workspace: got %d, want 0", u.BytesUsed)
	}
}

// @constraint RT-6
// Delete is idempotent on missing blobs.
func TestLocalFSDeleteIdempotent(t *testing.T) {
	s, _ := NewLocalFSStore(t.TempDir(), Config{})
	if err := s.Delete(context.Background(), "ws", "deadbeef"); err != nil {
		t.Errorf("Delete of missing blob: got %v, want nil", err)
	}
}

// @constraint RT-6
// ComputeHash is deterministic + lowercase hex.
func TestComputeHashDeterministic(t *testing.T) {
	a := ComputeHash([]byte("hello"))
	b := ComputeHash([]byte("hello"))
	if a != b {
		t.Errorf("ComputeHash non-deterministic")
	}
	if a != strings.ToLower(a) {
		t.Errorf("ComputeHash must emit lowercase hex")
	}
}
