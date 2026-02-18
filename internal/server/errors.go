package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(errorResponse{Error: msg}); err != nil {
		slog.Error("failed to write error response", "error", err)
	}
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
