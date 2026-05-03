package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// RecoverMiddleware wraps an MCP handler with a panic-recover boundary
// scoped to /mcp. A panic inside any tool dispatcher returns a
// JSON-RPC -32603 internal-error envelope (HTTP 200) and logs the
// stack via the redact-wrapped slog default handler — it MUST NOT
// propagate to net/http's default recover, which would tear down the
// listener and take the wiki API down with it.
//
// Satisfies: RT-11 (MCP sub-handler isolation), T6 (panic in any
//
//	MCP tool dispatcher MUST NOT propagate to the main HTTP API).
//
// The redaction property survives because slog at the package default
// is wrapped by internal/redact's slog handler — see Stage 1a
// (RT-6 binding constraint). Stack traces emitted via slog.LogAttrs
// flow through the same scrubbing path.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			slog.LogAttrs(context.Background(), slog.LevelError, "mcp handler panic",
				slog.String("component", "phronesis"),
				slog.String("path", r.URL.Path),
				slog.String("recovered", fmt.Sprint(rec)),
				slog.String("stack", string(debug.Stack())),
			)
			// Always emit a JSON-RPC envelope so the MCP client gets
			// a structured response. The id is unknown here (panic
			// could happen before request parsing), so the envelope
			// uses null id per JSON-RPC §4.1.
			writeJSONRPCError(w, nil, ErrCodeInternalError, "internal error")
		}()
		next.ServeHTTP(w, r)
	})
}
