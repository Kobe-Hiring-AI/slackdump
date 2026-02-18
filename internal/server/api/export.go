package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// ExportEngine is the interface the export handler needs from the engine.
type ExportEngine interface {
	SubmitExport(ctx context.Context, job *store.ExportJob)
}

// ExportHandler handles export operations.
type ExportHandler struct {
	store  *store.Store
	engine ExportEngine
}

// NewExportHandler creates a new ExportHandler.
func NewExportHandler(s *store.Store, engine ExportEngine) *ExportHandler {
	return &ExportHandler{
		store:  s,
		engine: engine,
	}
}

// Create handles POST /tenants/{id}/exports.
func (h *ExportHandler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	var req ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	job := &store.ExportJob{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Status:      store.JobStatusPending,
		Channels:    strings.Join(req.Channels, ","),
		TriggeredBy: "api",
	}
	if err := h.store.Jobs.Create(r.Context(), job); err != nil {
		slog.Error("create export job", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to create export job")
		return
	}

	h.engine.SubmitExport(context.WithoutCancel(r.Context()), job)

	respondJSON(w, http.StatusCreated, exportJobToResponse(job))
}

// List handles GET /tenants/{id}/exports.
func (h *ExportHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	jobs, err := h.store.Jobs.ListByTenant(r.Context(), tenantID)
	if err != nil {
		slog.Error("list exports", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to list exports")
		return
	}

	resp := make([]ExportResponse, 0, len(jobs))
	for _, j := range jobs {
		resp = append(resp, exportJobToResponse(j))
	}
	respondJSON(w, http.StatusOK, resp)
}

// Get handles GET /tenants/{id}/exports/{job_id}.
func (h *ExportHandler) Get(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")
	jobID := chi.URLParam(r, "job_id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	job, err := h.store.Jobs.Get(r.Context(), jobID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, "export not found")
			return
		}
		slog.Error("get export", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to get export")
		return
	}

	if job.TenantID != tenantID {
		respondError(w, http.StatusNotFound, "export not found")
		return
	}

	respondJSON(w, http.StatusOK, exportJobToResponse(job))
}

func exportJobToResponse(j *store.ExportJob) ExportResponse {
	return ExportResponse{
		ID:          j.ID,
		TenantID:    j.TenantID,
		Status:      j.Status,
		Channels:    j.Channels,
		TriggeredBy: j.TriggeredBy,
		ErrorMsg:    j.ErrorMsg,
		StartedAt:   j.StartedAt,
		FinishedAt:  j.FinishedAt,
		CreatedAt:   j.CreatedAt,
	}
}
