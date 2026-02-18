package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// DownloadHandler handles export file downloads.
type DownloadHandler struct {
	store   *store.Store
	dataDir string
}

// NewDownloadHandler creates a new DownloadHandler.
func NewDownloadHandler(s *store.Store, dataDir string) *DownloadHandler {
	return &DownloadHandler{
		store:   s,
		dataDir: dataDir,
	}
}

// Download handles GET /tenants/{id}/exports/{job_id}/download.
func (h *DownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
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
		slog.Error("get export for download", "error", err)
		respondError(w, http.StatusInternalServerError, "failed to get export")
		return
	}

	if job.TenantID != tenantID {
		respondError(w, http.StatusNotFound, "export not found")
		return
	}

	if job.Status != store.JobStatusCompleted {
		respondError(w, http.StatusNotFound, "export not completed")
		return
	}

	filePath := filepath.Join(h.dataDir, "tenants", tenantID, "export", "slackdump.sqlite")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "export file not found")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=slackdump.sqlite")
	http.ServeFile(w, r, filePath)
}
