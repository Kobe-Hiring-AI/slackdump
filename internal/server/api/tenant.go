package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rusq/slackdump/v4/internal/server/engine"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// CredentialValidator validates Slack credentials and returns workspace info.
type CredentialValidator interface {
	// Validate validates token+cookie credentials.
	Validate(ctx context.Context, token, cookie string) (workspace, teamID string, err error)
	// ValidateCookieOnly derives the token from the cookie and workspace name.
	ValidateCookieOnly(ctx context.Context, workspace, cookie string) (token, teamID string, err error)
}

// TenantHandler handles tenant CRUD operations.
type TenantHandler struct {
	store         *store.Store
	encryptionKey []byte
	validator     CredentialValidator
	engine        ExportEngine
	sqldClient    *engine.SqldClient
}

// NewTenantHandler creates a new TenantHandler.
func NewTenantHandler(s *store.Store, encKey []byte, v CredentialValidator, eng ExportEngine, sqld *engine.SqldClient) *TenantHandler {
	return &TenantHandler{
		store:         s,
		encryptionKey: encKey,
		validator:     v,
		engine:        eng,
		sqldClient:    sqld,
	}
}

// Create handles POST /tenants.
func (h *TenantHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req TenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Prefer user_id (Supabase auth_id); fall back to legacy id field.
	if req.UserID == "" {
		req.UserID = req.ID
	}
	if req.UserID == "" {
		respondError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.SlackToken == "" && req.SlackCookie == "" {
		respondError(w, http.StatusBadRequest, "slack_token or slack_cookie is required")
		return
	}

	var (
		workspace string
		teamID    string
		token     = req.SlackToken
		cookie    = req.SlackCookie
	)

	if token == "" {
		// Cookie-only mode: derive token from cookie + workspace name.
		if req.Workspace == "" {
			req.Workspace = "kobe-ai"
		}
		var err error
		token, teamID, err = h.validator.ValidateCookieOnly(r.Context(), req.Workspace, cookie)
		if err != nil {
			respondError(w, http.StatusBadRequest, "credential validation failed: "+err.Error())
			return
		}
		workspace = req.Workspace
	} else {
		var err error
		workspace, teamID, err = h.validator.Validate(r.Context(), token, cookie)
		if err != nil {
			respondError(w, http.StatusBadRequest, "credential validation failed: "+err.Error())
			return
		}
	}

	tenantID := req.UserID

	fmt.Printf("[DEBUG] Create tenant: token=%q cookie=%q (cookie len=%d)\n", token, cookie, len(cookie))
	fmt.Printf("[DEBUG] Create tenant: encryption key len=%d\n", len(h.encryptionKey))

	// Encrypt credentials.
	tokenEnc, err := store.Encrypt(h.encryptionKey, []byte(token))
	if err != nil {
		slog.Error("encrypt token", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to encrypt credentials")
		return
	}
	var cookieEnc []byte
	if cookie != "" {
		cookieEnc, err = store.Encrypt(h.encryptionKey, []byte(cookie))
		if err != nil {
			slog.Error("encrypt cookie", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to encrypt credentials")
			return
		}
	}
	var botTokenEnc []byte
	if req.SlackBotToken != "" {
		botTokenEnc, err = store.Encrypt(h.encryptionKey, []byte(req.SlackBotToken))
		if err != nil {
			slog.Error("encrypt bot token", "error", err)
			respondError(w, http.StatusInternalServerError, "failed to encrypt credentials")
			return
		}
	}

	// Create tenant.
	tenant := &store.Tenant{
		ID:        tenantID,
		Name:      req.Name,
		Workspace: workspace,
		TeamID:    teamID,
	}
	if err := h.store.Tenants.Create(r.Context(), tenant); err != nil {
		slog.Error("create tenant", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to create tenant")
		return
	}

	// Store credential.
	cred := &store.Credential{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		TokenEnc:    tokenEnc,
		CookieEnc:   cookieEnc,
		BotTokenEnc: botTokenEnc,
	}
	if err := h.store.Credentials.Upsert(r.Context(), cred); err != nil {
		slog.Error("upsert credentials", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to store credentials")
		return
	}

	// Create sqld namespace for the tenant.
	if h.sqldClient != nil {
		ns := "user-" + tenantID
		if err := h.sqldClient.CreateNamespace(r.Context(), ns); err != nil {
			slog.Error("create sqld namespace", "error", err, "namespace", ns)
			// Rollback: deactivate the tenant we just created.
			if rbErr := h.store.Tenants.Deactivate(r.Context(), tenantID); rbErr != nil {
				slog.Error("rollback tenant deactivation failed", "error", rbErr)
			}
			respondError(w, http.StatusInternalServerError, "failed to create database namespace")
			return
		}
	}

	// Create initial API key.
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
		Name:      "default",
	}
	if err := h.store.APIKeys.Create(r.Context(), apiKey); err != nil {
		slog.Error("create api key", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	// Auto-trigger initial export.
	job := &store.ExportJob{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Status:      store.JobStatusPending,
		TriggeredBy: "tenant-create",
	}
	if err := h.store.Jobs.Create(r.Context(), job); err != nil {
		slog.Error("create initial export job", "error", err)
		// Non-fatal: tenant was created successfully, export can be triggered manually.
	} else {
		h.engine.SubmitExport(context.WithoutCancel(r.Context()), job)
	}

	respondJSON(w, http.StatusCreated, TenantResponse{
		ID:        tenant.ID,
		Name:      tenant.Name,
		Workspace: tenant.Workspace,
		TeamID:    tenant.TeamID,
		Active:    tenant.Active,
		CreatedAt: tenant.CreatedAt,
		APIKey:    rawKey,
	})
}

// List handles GET /tenants (admin only).
func (h *TenantHandler) List(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.store.Tenants.List(r.Context())
	if err != nil {
		slog.Error("list tenants", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to list tenants")
		return
	}

	resp := make([]TenantResponse, 0, len(tenants))
	for _, t := range tenants {
		resp = append(resp, TenantResponse{
			ID:        t.ID,
			Name:      t.Name,
			Workspace: t.Workspace,
			TeamID:    t.TeamID,
			Active:    t.Active,
			CreatedAt: t.CreatedAt,
		})
	}
	respondJSON(w, http.StatusOK, resp)
}

// Get handles GET /tenants/{id}.
func (h *TenantHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Verify the caller is the tenant owner or admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != id {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	tenant, err := h.store.Tenants.Get(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "tenant not found")
			return
		}
		slog.Error("get tenant", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to get tenant")
		return
	}

	respondJSON(w, http.StatusOK, TenantResponse{
		ID:        tenant.ID,
		Name:      tenant.Name,
		Workspace: tenant.Workspace,
		TeamID:    tenant.TeamID,
		Active:    tenant.Active,
		CreatedAt: tenant.CreatedAt,
	})
}

// Delete handles DELETE /tenants/{id}.
func (h *TenantHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.store.Tenants.Deactivate(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "tenant not found")
			return
		}
		slog.Error("deactivate tenant", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to deactivate tenant")
		return
	}

	// Delete sqld namespace (non-fatal).
	if h.sqldClient != nil {
		ns := "user-" + id
		if err := h.sqldClient.DeleteNamespace(r.Context(), ns); err != nil {
			slog.Error("delete sqld namespace", "error", err, "namespace", ns)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
