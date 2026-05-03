package app

import (
	"encoding/json"
	"net/http"

	"github.com/dhanesh/phronesis/internal/redact"
)

// writeJSON serialises payload as JSON and writes it to w with the given
// HTTP status. Encoding errors are intentionally swallowed because the
// response is already in flight by the time Encode could fail.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeError emits a JSON error response with the given message,
// passing the message through internal/redact so any bearer token,
// OAuth code/state, or phr_live_* identifier accidentally embedded
// in an err.Error() string is scrubbed before egress.
//
// Satisfies: RT-6 (BINDING — cross-cutting redaction), S2.
//
// Centralising redaction here means the ~30 writeError(w, _, err.Error())
// call sites across the package don't need to remember to wrap; the
// safe default is automatic. Static-string callers pay the cost of a
// regex scan that finds nothing, which is cheap.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": redact.String(message)})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
