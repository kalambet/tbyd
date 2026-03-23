package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON encodes v as JSON into w and sets Content-Type. Any encoding error
// is logged at Warn level; headers have already been sent so it cannot be
// surfaced to the caller as an HTTP error.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writeJSON: failed to encode response", "error", err)
	}
}
