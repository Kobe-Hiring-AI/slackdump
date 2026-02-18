package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// TenantRequest is the body of POST /tenants.
type TenantRequest struct {
	Name        string `json:"name"`
	Workspace   string `json:"workspace,omitempty"`
	SlackToken  string `json:"slack_token,omitempty"`
	SlackCookie string `json:"slack_cookie,omitempty"`
}

// TenantResponse is returned from tenant endpoints.
type TenantResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Workspace string    `json:"workspace"`
	TeamID    string    `json:"team_id"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	// APIKey is only populated on create (the raw key is shown once).
	APIKey string `json:"api_key,omitempty"`
}

// APIKeyRequest is the body of POST /tenants/{id}/keys.
type APIKeyRequest struct {
	Name string `json:"name"`
}

// APIKeyResponse is returned from API key endpoints.
type APIKeyResponse struct {
	ID        string     `json:"id"`
	KeyPrefix string     `json:"key_prefix"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RawKey is only set on creation.
	RawKey string `json:"api_key,omitempty"`
}

// ExportRequest is the body of POST /tenants/{id}/exports.
type ExportRequest struct {
	Channels []string `json:"channels,omitempty"`
}

// ExportResponse is returned from export endpoints.
type ExportResponse struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Status      string     `json:"status"`
	Channels    string     `json:"channels,omitempty"`
	TriggeredBy string     `json:"triggered_by"`
	ErrorMsg    string     `json:"error_msg,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func respondError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
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
