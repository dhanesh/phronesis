package app

import (
	"encoding/json"
	"net/http"
)

// writeJSON serialises payload as JSON and writes it to w with the given
// HTTP status. Encoding errors are intentionally swallowed because the
// response is already in flight by the time Encode could fail.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}
