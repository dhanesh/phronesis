// Package blob provides content-addressed binary storage for media, keeping
// binary assets OUT of the git-synced markdown corpus.
//
// Satisfies: RT-6, RT-4.3 adjacent, TN4, S6, O9 (keeps git repo small)
//
// Markdown documents reference blobs by their SHA-256 hash (/media/<hex>).
// Blobs are scoped per workspace so quotas and tenancy are enforced at Put-
// time. The Store interface is small on purpose so additional backends
// (S3-compatible, restic) can be dropped in without disturbing call sites.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// ErrNotFound is returned when Get or Stat cannot locate the blob.
var ErrNotFound = errors.New("blob: not found")

// ErrQuotaExceeded is returned by Put when a write would exceed the
// workspace's configured quota.
//
// Satisfies: S6
var ErrQuotaExceeded = errors.New("blob: workspace quota exceeded")

// ErrContentTypeDisallowed is returned by Put when the supplied content type
// is not in the store's allow-list.
//
// Satisfies: S6 (content-type allow-list)
var ErrContentTypeDisallowed = errors.New("blob: content type not allowed")

// Info describes a stored blob.
type Info struct {
	Hash        string // lowercase hex sha256
	Size        int64
	ContentType string
}

// QuotaUsage reports a workspace's current storage footprint and quota.
type QuotaUsage struct {
	BytesUsed  int64
	QuotaBytes int64
}

// Store is the abstract binary-blob persistence layer.
//
// Implementations MUST be safe for concurrent use and MUST compute or verify
// the sha256 hash of the content at Put-time.
type Store interface {
	// Put stores r under workspaceID, computing its sha256 as the canonical
	// key. Returns the hash, size, and content type actually recorded. If
	// the content type is not in the allow-list, returns ErrContentTypeDisallowed.
	// If the write would exceed the workspace's quota, returns ErrQuotaExceeded.
	Put(ctx context.Context, workspaceID string, r io.Reader, contentType string) (Info, error)

	// Get returns a ReadCloser for the blob + its Info, or ErrNotFound.
	Get(ctx context.Context, workspaceID, hash string) (io.ReadCloser, Info, error)

	// Delete removes the blob. Missing is not an error.
	Delete(ctx context.Context, workspaceID, hash string) error

	// Usage returns the workspace's byte usage and configured quota.
	Usage(ctx context.Context, workspaceID string) (QuotaUsage, error)
}

// ComputeHash returns the lowercase-hex sha256 of the supplied data.
// Useful for tests and for content-addressed URL construction.
func ComputeHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Config tunes a Store's security + quota behavior.
//
// Satisfies: S6
type Config struct {
	AllowedTypes map[string]struct{} // content-types permitted by Put
	QuotaBytes   int64               // per-workspace byte cap; 0 = unlimited
	MaxBlobBytes int64               // per-blob byte cap; 0 = unlimited
}

// DefaultAllowedTypes matches the default in S6 (png, jpeg, gif, webp, mp4, pdf).
func DefaultAllowedTypes() map[string]struct{} {
	return map[string]struct{}{
		"image/png":       {},
		"image/jpeg":      {},
		"image/gif":       {},
		"image/webp":      {},
		"video/mp4":       {},
		"application/pdf": {},
	}
}

// DefaultQuotaBytes is S6's default per-workspace quota: 5 GB.
const DefaultQuotaBytes int64 = 5 * 1024 * 1024 * 1024
