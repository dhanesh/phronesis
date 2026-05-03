package redact

import (
	"context"
	"log/slog"
)

// SlogHandler wraps an inner slog.Handler. Every Record passed through
// Handle is cloned with its Message and string-typed Attrs redacted via
// String(). Non-string attributes (numbers, durations, groups) pass
// through untouched. error-typed Any attrs have their Error() string
// redacted.
//
// Satisfies: RT-6 (BINDING). Wired in cmd/phronesis/main.go as the
// default slog handler so every logger downstream of slog.Default()
// inherits redaction.
type SlogHandler struct {
	inner slog.Handler
}

// NewSlogHandler returns h wrapped so that records are redacted before
// reaching it.
func NewSlogHandler(h slog.Handler) *SlogHandler { return &SlogHandler{inner: h} }

func (h *SlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	clone := slog.NewRecord(r.Time, r.Level, String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clone)
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return &SlogHandler{inner: h.inner.WithAttrs(out)}
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{inner: h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, String(a.Value.String()))
	case slog.KindAny:
		// Errors carry their message via Error(); redact it.
		if err, ok := a.Value.Any().(error); ok && err != nil {
			return slog.String(a.Key, String(err.Error()))
		}
		// Stringers: try to redact the printed form.
		if s, ok := a.Value.Any().(interface{ String() string }); ok {
			return slog.String(a.Key, String(s.String()))
		}
		return a
	case slog.KindGroup:
		grp := a.Value.Group()
		out := make([]slog.Attr, len(grp))
		for i, g := range grp {
			out[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	default:
		return a
	}
}
