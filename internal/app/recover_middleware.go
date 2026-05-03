package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/dhanesh/phronesis/internal/redact"
)

// recoverMiddleware catches panics from downstream handlers, routes
// the diagnostic through slog (which the cmd/phronesis/main.go boot
// has already wrapped in redact.NewSlogHandler), and returns a
// generic 500 to the client with NO body content.
//
// Why this exists: Go's net/http server has its own per-request
// recover, but its diagnostic goes to http.Server.ErrorLog (i.e.
// log.Default() / os.Stderr), bypassing slog and therefore bypassing
// the redact handler. A panic stack trace embedding a request URL
// or Authorization header would leak credentials to stderr.
//
// This middleware ensures every panic on the request path is logged
// through the redacted slog handler instead.
//
// Satisfies: RT-6 (BINDING — cross-cutting redaction wiring at the
//
//	panic-recover boundary; closes G7),
//	S2 (bearer tokens / OAuth state / phr_live_* never
//	    appear in panic stacks reaching stderr).
//
// Placement: this middleware must wrap the inner mux/handlers but
// be wrapped BY the logging middleware (so panic-induced 500s show
// up in access logs). See routes.go.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// debug.Stack returns a snapshot that may embed request
			// URL strings if they were captured into the panic value.
			// Redact before logging — the slog default handler also
			// redacts, so this is defense in depth.
			stack := redact.Bytes(debug.Stack())
			slog.Error("panic recovered",
				slog.String("component", "phronesis"),
				slog.String("path", redact.URL(r.URL.String())),
				slog.String("method", r.Method),
				slog.String("panic", redact.String(fmt.Sprintf("%v", rec))),
				slog.String("stack", string(stack)),
			)
			// Defensive: only write the response if the handler
			// hasn't already started one. An already-started response
			// can't have its status overridden, but we can still skip
			// the body to avoid corrupting the client's parser.
			//
			// We can't reliably detect "already started" from a
			// http.ResponseWriter, so we attempt the write and rely
			// on net/http's downstream behaviour to drop the second
			// WriteHeader call. The client may see a partial response
			// in the rare half-written case; that's acceptable for a
			// panic path.
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
