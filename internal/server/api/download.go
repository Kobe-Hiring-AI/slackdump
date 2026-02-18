package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// DownloadHandler handles export file downloads.
type DownloadHandler struct {
	dataDir string
}

// NewDownloadHandler creates a new DownloadHandler.
func NewDownloadHandler(dataDir string) *DownloadHandler {
	return &DownloadHandler{
		dataDir: dataDir,
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

	filePath := filepath.Join(h.dataDir, "tenants", tenantID, "export", "slackdump.sqlite")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		respondError(w, http.StatusNotFound, "export file not found")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=slackdump.sqlite")
	http.ServeFile(w, r, filePath)
}
