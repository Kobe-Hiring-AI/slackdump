package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rusq/slackdump/v4/internal/server/api"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// e2eMockValidator implements api.CredentialValidator for E2E tests.
type e2eMockValidator struct {
	workspace string
	teamID    string
}

func (m *e2eMockValidator) Validate(_ context.Context, _, _ string) (string, string, error) {
	return m.workspace, m.teamID, nil
}

func (m *e2eMockValidator) ValidateCookieOnly(_ context.Context, _, _ string) (string, string, error) {
	return "xoxc-derived-token", m.teamID, nil
}

// e2eMockEngine implements ExportEngine for E2E tests.
type e2eMockEngine struct {
	submitted []*store.ExportJob
}

func (m *e2eMockEngine) SubmitExport(_ context.Context, job *store.ExportJob) {
	m.submitted = append(m.submitted, job)
}

func encKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

type e2eEnv struct {
	ts      *httptest.Server
	store   *store.Store
	engine  *e2eMockEngine
	dataDir string
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	engine := &e2eMockEngine{}
	validator := &e2eMockValidator{workspace: "test-ws", teamID: "T12345"}
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))

	sv := newServer(Config{
		Addr:          ":0",
		AdminKey:      "admin-key",
		EncryptionKey: encKey(),
		DataDir:       dataDir,
	}, s, engine, validator)

	ts := httptest.NewServer(sv.srv.Handler)
	t.Cleanup(ts.Close)

	return &e2eEnv{
		ts:      ts,
		store:   s,
		engine:  engine,
		dataDir: dataDir,
	}
}

func (e *e2eEnv) doRequest(t *testing.T, method, path, bearerToken string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, e.ts.URL+path, bodyReader)
	require.NoError(t, err)
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return v
}

// TestE2E_FullWorkflow tests the complete lifecycle:
// create tenant -> get tenant -> create API key -> use tenant key ->
// create export -> list exports -> get export -> download -> deactivate.
func TestE2E_FullWorkflow(t *testing.T) {
	env := newE2EEnv(t)

	// 1. Create tenant (admin auth).
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:        "acme-corp",
		SlackToken:  "xoxc-fake-token",
		SlackCookie: "xoxd-fake-cookie",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)
	assert.Equal(t, "acme-corp", tenant.Name)
	assert.Equal(t, "test-ws", tenant.Workspace)
	assert.Equal(t, "T12345", tenant.TeamID)
	assert.True(t, tenant.Active)
	assert.NotEmpty(t, tenant.APIKey, "initial API key must be returned")
	assert.NotEmpty(t, tenant.ID)

	tenantID := tenant.ID
	tenantKey := tenant.APIKey

	// 2. Get tenant (using tenant API key).
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID, tenantKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got := decodeJSON[api.TenantResponse](t, resp)
	assert.Equal(t, tenantID, got.ID)
	assert.Equal(t, "acme-corp", got.Name)
	assert.Empty(t, got.APIKey, "API key must not be returned on GET")

	// 3. Get tenant (admin key should also work).
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID, "admin-key", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 4. Create additional API key.
	resp = env.doRequest(t, "POST", "/tenants/"+tenantID+"/keys", tenantKey, api.APIKeyRequest{
		Name: "ci-pipeline",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	newKey := decodeJSON[api.APIKeyResponse](t, resp)
	assert.Equal(t, "ci-pipeline", newKey.Name)
	assert.NotEmpty(t, newKey.RawKey)
	assert.NotEmpty(t, newKey.KeyPrefix)

	// 5. Verify the new API key works.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID, newKey.RawKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 6. Revoke the new API key.
	resp = env.doRequest(t, "DELETE", "/tenants/"+tenantID+"/keys/"+newKey.ID, tenantKey, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 7. Verify revoked key no longer works.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID, newKey.RawKey, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// 8. Create export.
	resp = env.doRequest(t, "POST", "/tenants/"+tenantID+"/exports", tenantKey, api.ExportRequest{
		Channels: []string{"C001", "C002"},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	export := decodeJSON[api.ExportResponse](t, resp)
	assert.Equal(t, tenantID, export.TenantID)
	assert.Equal(t, "pending", export.Status)
	assert.Equal(t, "C001,C002", export.Channels)
	assert.Equal(t, "api", export.TriggeredBy)
	assert.Len(t, env.engine.submitted, 2) // 1 auto-triggered by tenant-create + 1 manual

	jobID := export.ID

	// 9. List exports.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID+"/exports", tenantKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	exports := decodeJSON[[]api.ExportResponse](t, resp)
	assert.Len(t, exports, 2) // auto-triggered + manual

	// 10. Get single export.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID+"/exports/"+jobID, tenantKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	gotExport := decodeJSON[api.ExportResponse](t, resp)
	assert.Equal(t, jobID, gotExport.ID)

	// 11. Download before completion should fail.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID+"/exports/"+jobID+"/download", tenantKey, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// 12. Mark export completed and create the file.
	require.NoError(t, env.store.Jobs.SetCompleted(context.Background(), jobID, ""))
	exportDir := filepath.Join(env.dataDir, "tenants", tenantID, "export")
	require.NoError(t, os.MkdirAll(exportDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exportDir, "slackdump.sqlite"), []byte("sqlite-data"), 0o644))

	// 13. Download completed export.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID+"/exports/"+jobID+"/download", tenantKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, "sqlite-data", string(body))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "slackdump.sqlite")

	// 14. Deactivate tenant (admin only).
	resp = env.doRequest(t, "DELETE", "/tenants/"+tenantID, "admin-key", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 15. Tenant key should still authenticate but tenant is deactivated.
	// The API key still resolves, but the tenant GET should return not found.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantID, tenantKey, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

// TestE2E_CookieOnlyTenantCreation tests creating a tenant with cookie-only auth.
func TestE2E_CookieOnlyTenantCreation(t *testing.T) {
	env := newE2EEnv(t)

	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:        "cookie-ws",
		Workspace:   "my-team",
		SlackCookie: "xoxd-cookie-value",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)
	assert.Equal(t, "cookie-ws", tenant.Name)
	assert.Equal(t, "my-team", tenant.Workspace)
	assert.Equal(t, "T12345", tenant.TeamID)
	assert.NotEmpty(t, tenant.APIKey)
}

// TestE2E_CookieOnlyDefaultWorkspace tests that cookie-only without workspace defaults to kobe-ai.
func TestE2E_CookieOnlyDefaultWorkspace(t *testing.T) {
	env := newE2EEnv(t)

	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:        "cookie-ws",
		SlackCookie: "xoxd-cookie-value",
		// No workspace — should default to kobe-ai.
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)
	assert.Equal(t, "kobe-ai", tenant.Workspace)
}

// TestE2E_Auth_NoToken tests that requests without auth are rejected.
func TestE2E_Auth_NoToken(t *testing.T) {
	env := newE2EEnv(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/tenants"},
		{"GET", "/tenants/some-id"},
		{"DELETE", "/tenants/some-id"},
		{"POST", "/tenants/some-id/keys"},
		{"DELETE", "/tenants/some-id/keys/some-key"},
		{"POST", "/tenants/some-id/exports"},
		{"GET", "/tenants/some-id/exports"},
		{"GET", "/tenants/some-id/exports/some-job"},
		{"GET", "/tenants/some-id/exports/some-job/download"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			resp := env.doRequest(t, ep.method, ep.path, "", nil)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

// TestE2E_Auth_InvalidToken tests that requests with an invalid token are rejected.
func TestE2E_Auth_InvalidToken(t *testing.T) {
	env := newE2EEnv(t)

	resp := env.doRequest(t, "GET", "/tenants/some-id", "bad-token", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

// TestE2E_AdminOnly tests that non-admin keys can't access admin endpoints.
func TestE2E_AdminOnly(t *testing.T) {
	env := newE2EEnv(t)

	// Create a tenant to get a tenant API key.
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "test",
		SlackToken: "xoxc-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)
	tenantKey := tenant.APIKey

	// Tenant key should not be able to create tenants.
	resp = env.doRequest(t, "POST", "/tenants", tenantKey, api.TenantRequest{
		Name:       "hacker-ws",
		SlackToken: "xoxc-stolen",
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Tenant key should not be able to deactivate tenants.
	resp = env.doRequest(t, "DELETE", "/tenants/"+tenant.ID, tenantKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

// TestE2E_TenantIsolation tests that one tenant can't access another's resources.
func TestE2E_TenantIsolation(t *testing.T) {
	env := newE2EEnv(t)

	// Create two tenants.
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "tenant-a",
		SlackToken: "xoxc-a",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenantA := decodeJSON[api.TenantResponse](t, resp)

	resp = env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "tenant-b",
		SlackToken: "xoxc-b",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenantB := decodeJSON[api.TenantResponse](t, resp)

	// Tenant A can't access tenant B's info.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantB.ID, tenantA.APIKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Tenant B can't access tenant A's info.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantA.ID, tenantB.APIKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Create an export for tenant A.
	resp = env.doRequest(t, "POST", "/tenants/"+tenantA.ID+"/exports", tenantA.APIKey, api.ExportRequest{})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	exportA := decodeJSON[api.ExportResponse](t, resp)

	// Tenant B can't access tenant A's exports.
	resp = env.doRequest(t, "GET", "/tenants/"+tenantA.ID+"/exports", tenantB.APIKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	resp = env.doRequest(t, "GET", "/tenants/"+tenantA.ID+"/exports/"+exportA.ID, tenantB.APIKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Tenant B can't create API keys for tenant A.
	resp = env.doRequest(t, "POST", "/tenants/"+tenantA.ID+"/keys", tenantB.APIKey, api.APIKeyRequest{
		Name: "hijack",
	})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Tenant B can't create exports for tenant A.
	resp = env.doRequest(t, "POST", "/tenants/"+tenantA.ID+"/exports", tenantB.APIKey, api.ExportRequest{})
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

// TestE2E_PublicRoutes tests that /docs and /openapi.yaml are accessible without auth.
func TestE2E_PublicRoutes(t *testing.T) {
	env := newE2EEnv(t)

	t.Run("openapi.yaml", func(t *testing.T) {
		resp := env.doRequest(t, "GET", "/openapi.yaml", "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		assert.Contains(t, string(body), "openapi: 3.0.3")
		assert.Contains(t, string(body), "Slackdump Server API")
	})

	t.Run("docs", func(t *testing.T) {
		resp := env.doRequest(t, "GET", "/docs", "", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		assert.Contains(t, string(body), "swagger-ui")
	})
}

// TestE2E_CreateTenantValidation tests input validation for tenant creation.
func TestE2E_CreateTenantValidation(t *testing.T) {
	env := newE2EEnv(t)

	tests := []struct {
		name       string
		body       api.TenantRequest
		wantCode   int
		wantSubstr string
	}{
		{
			name:       "missing name",
			body:       api.TenantRequest{SlackToken: "xoxc-test"},
			wantCode:   http.StatusBadRequest,
			wantSubstr: "name is required",
		},
		{
			name:       "missing credentials",
			body:       api.TenantRequest{Name: "test"},
			wantCode:   http.StatusBadRequest,
			wantSubstr: "slack_token or slack_cookie is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.doRequest(t, "POST", "/tenants", "admin-key", tt.body)
			require.Equal(t, tt.wantCode, resp.StatusCode)
			errResp := decodeJSON[map[string]string](t, resp)
			assert.Contains(t, errResp["error"], tt.wantSubstr)
		})
	}
}

// TestE2E_ExportFullExport tests creating an export with no channel filter.
func TestE2E_ExportFullExport(t *testing.T) {
	env := newE2EEnv(t)

	// Create tenant.
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "full-export",
		SlackToken: "xoxc-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)

	// Create export with empty channels (full export).
	resp = env.doRequest(t, "POST", "/tenants/"+tenant.ID+"/exports", tenant.APIKey, api.ExportRequest{})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	export := decodeJSON[api.ExportResponse](t, resp)
	assert.Empty(t, export.Channels, "full export should have no channel filter")
}

// TestE2E_NotFound tests 404 responses for missing resources.
func TestE2E_NotFound(t *testing.T) {
	env := newE2EEnv(t)

	// Create a tenant to get valid auth.
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "test",
		SlackToken: "xoxc-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)

	t.Run("get nonexistent tenant", func(t *testing.T) {
		// Admin can try to get any tenant.
		resp := env.doRequest(t, "GET", "/tenants/nonexistent-id", "admin-key", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("get nonexistent export", func(t *testing.T) {
		resp := env.doRequest(t, "GET", "/tenants/"+tenant.ID+"/exports/nonexistent-job", tenant.APIKey, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("revoke nonexistent key", func(t *testing.T) {
		resp := env.doRequest(t, "DELETE", "/tenants/"+tenant.ID+"/keys/nonexistent-key", tenant.APIKey, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("deactivate nonexistent tenant", func(t *testing.T) {
		resp := env.doRequest(t, "DELETE", "/tenants/nonexistent-id", "admin-key", nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})
}

// TestE2E_MultipleExports tests creating and listing multiple exports.
func TestE2E_MultipleExports(t *testing.T) {
	env := newE2EEnv(t)

	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "multi-export",
		SlackToken: "xoxc-test",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	tenant := decodeJSON[api.TenantResponse](t, resp)

	// Create 3 exports.
	for i := 0; i < 3; i++ {
		resp = env.doRequest(t, "POST", "/tenants/"+tenant.ID+"/exports", tenant.APIKey, api.ExportRequest{})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	}

	assert.Len(t, env.engine.submitted, 4) // 1 auto-triggered by tenant-create + 3 manual

	// List should return all 4 (1 auto + 3 manual).
	resp = env.doRequest(t, "GET", "/tenants/"+tenant.ID+"/exports", tenant.APIKey, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	exports := decodeJSON[[]api.ExportResponse](t, resp)
	assert.Len(t, exports, 4)
}

// TestE2E_DownloadWrongTenant tests that a tenant can't download another's export.
func TestE2E_DownloadWrongTenant(t *testing.T) {
	env := newE2EEnv(t)

	// Create two tenants.
	resp := env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "owner",
		SlackToken: "xoxc-owner",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	owner := decodeJSON[api.TenantResponse](t, resp)

	resp = env.doRequest(t, "POST", "/tenants", "admin-key", api.TenantRequest{
		Name:       "attacker",
		SlackToken: "xoxc-attacker",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	attacker := decodeJSON[api.TenantResponse](t, resp)

	// Create export for owner.
	resp = env.doRequest(t, "POST", "/tenants/"+owner.ID+"/exports", owner.APIKey, api.ExportRequest{})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	export := decodeJSON[api.ExportResponse](t, resp)

	// Complete the export and create the file.
	require.NoError(t, env.store.Jobs.SetCompleted(context.Background(), export.ID, ""))
	exportDir := filepath.Join(env.dataDir, "tenants", owner.ID, "export")
	require.NoError(t, os.MkdirAll(exportDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exportDir, "slackdump.sqlite"), []byte("secret-data"), 0o644))

	// Attacker tries to download owner's export via owner's tenant URL.
	resp = env.doRequest(t, "GET", "/tenants/"+owner.ID+"/exports/"+export.ID+"/download", attacker.APIKey, nil)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}
