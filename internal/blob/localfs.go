package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LocalFSStore stores blobs on the local filesystem under a workspace-keyed
// layout:
//
//	<root>/<workspaceID>/<hash[0:2]>/<hash>
//	<root>/<workspaceID>/meta/<hash>.json   (content-type + size)
//
// The two-char sharding keeps per-directory entry counts sane even at 10K+
// blobs per workspace (T4 scale target).
//
// Satisfies: RT-6 default, TN4, S6 (quota + content-type allow-list), O6
// (atomic write via tempfile + rename).
type LocalFSStore struct {
	root string
	cfg  Config
	mu   sync.Mutex // serializes Puts per workspace; small critical sections
}

// NewLocalFSStore ensures root exists and returns a Store with the given cfg.
// If cfg.AllowedTypes is nil, DefaultAllowedTypes() is used. If cfg.QuotaBytes
// is 0, DefaultQuotaBytes is used.
func NewLocalFSStore(root string, cfg Config) (*LocalFSStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if cfg.AllowedTypes == nil {
		cfg.AllowedTypes = DefaultAllowedTypes()
	}
	if cfg.QuotaBytes == 0 {
		cfg.QuotaBytes = DefaultQuotaBytes
	}
	return &LocalFSStore{root: root, cfg: cfg}, nil
}

type blobMeta struct {
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

func (s *LocalFSStore) Put(ctx context.Context, workspaceID string, r io.Reader, contentType string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if _, ok := s.cfg.AllowedTypes[contentType]; !ok {
		return Info{}, ErrContentTypeDisallowed
	}

	wsDir := filepath.Join(s.root, safeID(workspaceID))
	if err := os.MkdirAll(filepath.Join(wsDir, "meta"), 0o755); err != nil {
		return Info{}, err
	}

	tmp, err := os.CreateTemp(wsDir, ".tmp-*")
	if err != nil {
		return Info{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // best-effort cleanup if rename fails

	h := sha256.New()
	mw := io.MultiWriter(tmp, h)

	var written int64
	if s.cfg.MaxBlobBytes > 0 {
		r = io.LimitReader(r, s.cfg.MaxBlobBytes+1)
	}
	written, err = io.Copy(mw, r)
	if err != nil {
		tmp.Close()
		return Info{}, err
	}
	if s.cfg.MaxBlobBytes > 0 && written > s.cfg.MaxBlobBytes {
		tmp.Close()
		return Info{}, errors.New("blob: exceeds per-blob size limit")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Info{}, err
	}
	if err := tmp.Close(); err != nil {
		return Info{}, err
	}

	hash := hex.EncodeToString(h.Sum(nil))

	// Quota check under the store mutex so we don't race concurrent puts.
	s.mu.Lock()
	usage, err := s.usageLocked(wsDir)
	if err != nil {
		s.mu.Unlock()
		return Info{}, err
	}
	if s.cfg.QuotaBytes > 0 && usage+written > s.cfg.QuotaBytes {
		s.mu.Unlock()
		return Info{}, ErrQuotaExceeded
	}
	destDir := filepath.Join(wsDir, hash[:2])
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		s.mu.Unlock()
		return Info{}, err
	}
	dest := filepath.Join(destDir, hash)
	// If the blob already exists (duplicate content), skip re-write and
	// return the existing info. Content addressing means same bytes == same blob.
	if _, err := os.Stat(dest); err == nil {
		s.mu.Unlock()
		info, _ := s.readMeta(wsDir, hash)
		return info, nil
	}
	if err := os.Rename(tmpName, dest); err != nil {
		s.mu.Unlock()
		return Info{}, err
	}

	meta := blobMeta{Hash: hash, Size: written, ContentType: contentType}
	metaBytes, _ := json.Marshal(meta)
	metaTmp := filepath.Join(wsDir, "meta", ".tmp-"+hash)
	if err := os.WriteFile(metaTmp, metaBytes, 0o644); err != nil {
		s.mu.Unlock()
		return Info{}, err
	}
	if err := os.Rename(metaTmp, filepath.Join(wsDir, "meta", hash+".json")); err != nil {
		s.mu.Unlock()
		return Info{}, err
	}
	s.mu.Unlock()

	return Info{Hash: hash, Size: written, ContentType: contentType}, nil
}

func (s *LocalFSStore) Get(ctx context.Context, workspaceID, hash string) (io.ReadCloser, Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	if !validHex(hash) || len(hash) < 4 {
		return nil, Info{}, ErrNotFound
	}
	wsDir := filepath.Join(s.root, safeID(workspaceID))
	path := filepath.Join(wsDir, hash[:2], hash)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Info{}, ErrNotFound
		}
		return nil, Info{}, err
	}
	info, err := s.readMeta(wsDir, hash)
	if err != nil {
		f.Close()
		return nil, Info{}, err
	}
	return f, info, nil
}

func (s *LocalFSStore) Delete(ctx context.Context, workspaceID, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validHex(hash) || len(hash) < 4 {
		return nil
	}
	wsDir := filepath.Join(s.root, safeID(workspaceID))
	_ = os.Remove(filepath.Join(wsDir, hash[:2], hash))
	_ = os.Remove(filepath.Join(wsDir, "meta", hash+".json"))
	return nil
}

func (s *LocalFSStore) Usage(ctx context.Context, workspaceID string) (QuotaUsage, error) {
	if err := ctx.Err(); err != nil {
		return QuotaUsage{}, err
	}
	wsDir := filepath.Join(s.root, safeID(workspaceID))
	used, err := s.usageLocked(wsDir)
	if err != nil {
		return QuotaUsage{}, err
	}
	return QuotaUsage{BytesUsed: used, QuotaBytes: s.cfg.QuotaBytes}, nil
}

func (s *LocalFSStore) usageLocked(wsDir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(wsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip the meta/ tree; it's small and its size is not billable content.
		if strings.Contains(path, string(filepath.Separator)+"meta"+string(filepath.Separator)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (s *LocalFSStore) readMeta(wsDir, hash string) (Info, error) {
	b, err := os.ReadFile(filepath.Join(wsDir, "meta", hash+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Info{Hash: hash}, nil
		}
		return Info{}, err
	}
	var m blobMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return Info{}, err
	}
	return Info(m), nil
}

func safeID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	id = strings.ReplaceAll(id, "..", "_")
	if id == "" {
		return "_"
	}
	return id
}

func validHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
