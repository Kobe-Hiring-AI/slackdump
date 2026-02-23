package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/rusq/slackdump/v4/internal/server/storage"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// DownloadHandler handles export file downloads.
type DownloadHandler struct {
	dataDir string
	store   *store.Store
	storage storage.Storage
}

// NewDownloadHandler creates a new DownloadHandler.
func NewDownloadHandler(dataDir string, s *store.Store, st storage.Storage) *DownloadHandler {
	return &DownloadHandler{
		dataDir: dataDir,
		store:   s,
		storage: st,
	}
}

// Download handles GET /tenants/{id}/export/download.
func (h *DownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "id")

	// Verify caller owns this tenant or is admin.
	callerTenant := TenantFromContext(r.Context())
	if callerTenant != "" && callerTenant != tenantID {
		respondError(w, http.StatusForbidden, "access denied")
		return
	}

	// Look up tenant to get workspace name.
	tenant, err := h.store.Tenants.Get(r.Context(), tenantID)
	if err != nil {
		respondError(w, http.StatusNotFound, "tenant not found")
		return
	}

	filePath := filepath.Join(h.dataDir, tenantID, tenant.Workspace, "slackdump.sqlite")

	// Try local file first.
	if _, err := os.Stat(filePath); os.IsNotExist(err) && h.storage != nil {
		// Try downloading from storage.
		s3Key := filepath.Join(tenantID, tenant.Workspace, "slackdump.sqlite")
		if dlErr := h.storage.Download(r.Context(), s3Key, filePath); dlErr != nil {
			slog.Error("storage download failed", "error", dlErr, "key", s3Key)
			respondError(w, http.StatusNotFound, "export file not found")
			return
		}
	} else if os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "export file not found")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=slackdump.sqlite")
	http.ServeFile(w, r, filePath)
}
