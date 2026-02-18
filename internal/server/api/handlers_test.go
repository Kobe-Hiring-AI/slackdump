package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// mockValidator is a test credential validator.
type mockValidator struct {
	workspace string
	teamID    string
	err       error
}

func (m *mockValidator) Validate(_ context.Context, _, _ string) (string, string, error) {
	return m.workspace, m.teamID, m.err
}

func (m *mockValidator) ValidateCookieOnly(_ context.Context, _, _ string) (string, string, error) {
	return "xoxc-derived-token", m.teamID, m.err
}

// mockEngine is a test export engine.
type mockEngine struct {
	submitted []*store.ExportJob
}

func (m *mockEngine) SubmitExport(_ context.Context, job *store.ExportJob) {
	m.submitted = append(m.submitted, job)
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func encKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

// withChiParams adds chi URL parameters to the request context.
func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestTenantHandler_Create(t *testing.T) {
	s := testStore(t)
	validator := &mockValidator{workspace: "test-ws", teamID: "T123"}
	engine := &mockEngine{}
	h := NewTenantHandler(s, encKey(), validator, engine)

	body := `{"name":"My Workspace","slack_token":"xoxc-test-token","slack_cookie":"xoxd-test-cookie"}`
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp TenantResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "My Workspace", resp.Name)
	assert.Equal(t, "test-ws", resp.Workspace)
	assert.Equal(t, "T123", resp.TeamID)
	assert.True(t, resp.Active)
	assert.NotEmpty(t, resp.APIKey)
	assert.NotEmpty(t, resp.ID)

	// Verify auto-export was triggered.
	assert.Len(t, engine.submitted, 1)
	assert.Equal(t, "tenant-create", engine.submitted[0].TriggeredBy)
}

func TestTenantHandler_Create_MissingFields(t *testing.T) {
	s := testStore(t)
	validator := &mockValidator{workspace: "test-ws", teamID: "T123"}
	h := NewTenantHandler(s, encKey(), validator, nil)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTenantHandler_Get(t *testing.T) {
	s := testStore(t)
	h := NewTenantHandler(s, encKey(), nil, nil)

	// Create a tenant.
	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	// Admin context (no tenant_id set, admin flag would be set).
	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1", nil)
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp TenantResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "t-1", resp.ID)
	assert.Equal(t, "Test", resp.Name)
}

func TestTenantHandler_Get_NotFound(t *testing.T) {
	s := testStore(t)
	h := NewTenantHandler(s, encKey(), nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tenants/nonexistent", nil)
	req = withChiParams(req, map[string]string{"id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTenantHandler_Delete(t *testing.T) {
	s := testStore(t)
	h := NewTenantHandler(s, encKey(), nil, nil)

	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	req := httptest.NewRequest(http.MethodDelete, "/tenants/t-1", nil)
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify tenant is deactivated (Get should return not found for inactive).
	_, err := s.Tenants.Get(context.Background(), "t-1")
	assert.Error(t, err)
}

func TestTenantHandler_Delete_NotFound(t *testing.T) {
	s := testStore(t)
	h := NewTenantHandler(s, encKey(), nil, nil)

	req := httptest.NewRequest(http.MethodDelete, "/tenants/nonexistent", nil)
	req = withChiParams(req, map[string]string{"id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAPIKeyHandler_Create(t *testing.T) {
	s := testStore(t)
	h := NewAPIKeyHandler(s)

	// Create a tenant first.
	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	body := `{"name":"my-key"}`
	req := httptest.NewRequest(http.MethodPost, "/tenants/t-1/keys", bytes.NewBufferString(body))
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp APIKeyResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "my-key", resp.Name)
	assert.NotEmpty(t, resp.RawKey)
	assert.NotEmpty(t, resp.ID)
	assert.NotEmpty(t, resp.KeyPrefix)
}

func TestAPIKeyHandler_Revoke(t *testing.T) {
	s := testStore(t)
	h := NewAPIKeyHandler(s)

	// Create tenant and key.
	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	apiKey := &store.APIKey{
		ID:        "key-1",
		TenantID:  "t-1",
		KeyHash:   "somehash",
		KeyPrefix: "abcd1234",
		Name:      "test-key",
	}
	require.NoError(t, s.APIKeys.Create(context.Background(), apiKey))

	req := httptest.NewRequest(http.MethodDelete, "/tenants/t-1/keys/key-1", nil)
	req = withChiParams(req, map[string]string{"id": "t-1", "key_id": "key-1"})
	rec := httptest.NewRecorder()

	h.Revoke(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAPIKeyHandler_Revoke_NotFound(t *testing.T) {
	s := testStore(t)
	h := NewAPIKeyHandler(s)

	req := httptest.NewRequest(http.MethodDelete, "/tenants/t-1/keys/nonexistent", nil)
	req = withChiParams(req, map[string]string{"id": "t-1", "key_id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.Revoke(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExportHandler_Create(t *testing.T) {
	s := testStore(t)
	engine := &mockEngine{}
	h := NewExportHandler(s, engine)

	// Create a tenant.
	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	body := `{"channels":["general","random"]}`
	req := httptest.NewRequest(http.MethodPost, "/tenants/t-1/exports", bytes.NewBufferString(body))
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp ExportResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "t-1", resp.TenantID)
	assert.Equal(t, "pending", resp.Status)
	assert.Equal(t, "general,random", resp.Channels)
	assert.Equal(t, "api", resp.TriggeredBy)

	assert.Len(t, engine.submitted, 1)
}

func TestExportHandler_List(t *testing.T) {
	s := testStore(t)
	engine := &mockEngine{}
	h := NewExportHandler(s, engine)

	// Create a tenant and some jobs.
	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	for _, id := range []string{"j-1", "j-2"} {
		job := &store.ExportJob{
			ID:          id,
			TenantID:    "t-1",
			Channels:    "general",
			TriggeredBy: "api",
		}
		require.NoError(t, s.Jobs.Create(context.Background(), job))
	}

	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1/exports", nil)
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp []ExportResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Len(t, resp, 2)
}

func TestExportHandler_Get(t *testing.T) {
	s := testStore(t)
	engine := &mockEngine{}
	h := NewExportHandler(s, engine)

	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	job := &store.ExportJob{
		ID:          "j-1",
		TenantID:    "t-1",
		Channels:    "general",
		TriggeredBy: "api",
	}
	require.NoError(t, s.Jobs.Create(context.Background(), job))

	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1/exports/j-1", nil)
	req = withChiParams(req, map[string]string{"id": "t-1", "job_id": "j-1"})
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp ExportResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "j-1", resp.ID)
	assert.Equal(t, "t-1", resp.TenantID)
}

func TestExportHandler_Get_NotFound(t *testing.T) {
	s := testStore(t)
	engine := &mockEngine{}
	h := NewExportHandler(s, engine)

	tenant := &store.Tenant{ID: "t-1", Name: "Test", Workspace: "ws", TeamID: "T1"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1/exports/nonexistent", nil)
	req = withChiParams(req, map[string]string{"id": "t-1", "job_id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExportHandler_Get_WrongTenant(t *testing.T) {
	s := testStore(t)
	engine := &mockEngine{}
	h := NewExportHandler(s, engine)

	for _, tn := range []struct{ id, name string }{{"t-1", "T1"}, {"t-2", "T2"}} {
		tenant := &store.Tenant{ID: tn.id, Name: tn.name, Workspace: "ws", TeamID: "T1"}
		require.NoError(t, s.Tenants.Create(context.Background(), tenant))
	}

	job := &store.ExportJob{
		ID:          "j-1",
		TenantID:    "t-1",
		Channels:    "general",
		TriggeredBy: "api",
	}
	require.NoError(t, s.Jobs.Create(context.Background(), job))

	// Try to access t-1's job from t-2.
	req := httptest.NewRequest(http.MethodGet, "/tenants/t-2/exports/j-1", nil)
	req = withChiParams(req, map[string]string{"id": "t-2", "job_id": "j-1"})
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadHandler_Download(t *testing.T) {
	dataDir := t.TempDir()
	h := NewDownloadHandler(dataDir)

	// Create the file.
	exportDir := filepath.Join(dataDir, "tenants", "t-1", "export")
	require.NoError(t, os.MkdirAll(exportDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exportDir, "slackdump.sqlite"), []byte("test-data"), 0o644))

	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1/export/download", nil)
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "test-data")
}

func TestDownloadHandler_Download_FileNotFound(t *testing.T) {
	h := NewDownloadHandler(t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/tenants/t-1/export/download", nil)
	req = withChiParams(req, map[string]string{"id": "t-1"})
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
