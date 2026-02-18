package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// APIKeyHandler handles API key operations.
type APIKeyHandler struct {
	store *store.Store
}

// NewAPIKeyHandler creates a new APIKeyHandler.
func NewAPIKeyHandler(s *store.Store) *APIKeyHandler {
	return &APIKeyHandler{store: s}
}

// Create handles POST /tenants/{id}/keys.
func (h *APIKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	var req APIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rawKey, err := store.GenerateAPIKey()
	if err != nil {
		slog.Error("generate api key", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	apiKey := &store.APIKey{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		KeyHash:   store.HashKey(rawKey),
		KeyPrefix: store.KeyPrefix(rawKey),
		Name:      req.Name,
	}
	if err := h.store.APIKeys.Create(r.Context(), apiKey); err != nil {
		slog.Error("create api key", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	respondJSON(w, http.StatusCreated, APIKeyResponse{
		ID:        apiKey.ID,
		KeyPrefix: apiKey.KeyPrefix,
		Name:      apiKey.Name,
		CreatedAt: apiKey.CreatedAt,
		ExpiresAt: apiKey.ExpiresAt,
		RawKey:    rawKey,
	})
}

// Revoke handles DELETE /tenants/{id}/keys/{key_id}.
func (h *APIKeyHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")
	keyID := chi.URLParam(r, "key_id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	if err := h.store.APIKeys.Revoke(r.Context(), keyID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "api key not found")
			return
		}
		slog.Error("revoke api key", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to revoke api key")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
