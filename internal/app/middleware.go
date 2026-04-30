package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// attachPrincipal is a middleware that resolves Principal from the request and
// attaches it to the context. It DOES NOT enforce authentication; handlers that
// need authn call principal.FromContext/Require. Handlers that don't need it
// (e.g. /api/health, /api/login) simply ignore the context value.
func (s *Server) attachPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.principalFromRequest(r); ok {
			r = r.WithContext(principal.WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

// withAuth wraps a handler so unauthenticated /api/* requests get 401 and
// unauthenticated page requests redirect to /. /api/login and /api/health
// are pinned-open exceptions so a logged-out client can still reach them.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/health" {
			next(w, r)
			return
		}
		if _, ok := s.auth.Username(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "authentication required")
			} else {
				http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
			}
			return
		}
		next(w, r)
	}
}

// statusRecorder wraps http.ResponseWriter so loggingMiddleware can capture
// the response status code and byte count without parsing the response body.
// Implements http.Flusher so SSE handlers in handlers_pages.go still see a
// flushable writer through the middleware chain.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// loggingMiddleware is the outermost middleware. Emits one structured log
// line per request with method, path, status, byte count, and duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		slog.LogAttrs(r.Context(), slog.LevelInfo, "http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start).Round(time.Millisecond)),
		)
	})
}
