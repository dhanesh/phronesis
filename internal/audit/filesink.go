package audit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// FileSink appends audit events as JSON Lines to a file. It is the default
// Sink for v1 deployments; a Postgres or OTel log exporter can replace it
// later without changing the Drainer.
//
// Satisfies: RT-4.4 default, S2 (durable audit)
//
// Every Write call is protected by a mutex to serialize file appends.
// os.File.Sync() is called after each write so process crashes lose at most
// the in-flight batch (bounded-loss contract of S9).
type FileSink struct {
	mu     sync.Mutex
	f      *os.File
	closed bool
}

// NewFileSink opens path in append mode, creating parent dirs if needed.
func NewFileSink(path string) (*FileSink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	return &FileSink{f: f}, nil
}

func (s *FileSink) Write(ctx context.Context, events []Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	enc := json.NewEncoder(s.f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return s.f.Sync()
}

func (s *FileSink) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.f.Sync()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
