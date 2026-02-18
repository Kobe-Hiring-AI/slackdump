package server

import (
	"context"
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

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestJSONMiddleware(t *testing.T) {
	handler := JSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthMiddleware_AdminKey(t *testing.T) {
	s := testStore(t)
	adminKey := "test-admin-key"

	handler := AuthMiddleware(s, adminKey)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, api.IsAdmin(r.Context()))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthMiddleware_ValidTenantKey(t *testing.T) {
	s := testStore(t)

	// Create a tenant and API key.
	tenant := &store.Tenant{ID: "tenant-1", Name: "Test", Workspace: "test-ws", TeamID: "T123"}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))

	rawKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	apiKey := &store.APIKey{
		ID:        "key-1",
		TenantID:  "tenant-1",
		KeyHash:   store.HashKey(rawKey),
		KeyPrefix: store.KeyPrefix(rawKey),
		Name:      "test-key",
	}
	require.NoError(t, s.APIKeys.Create(context.Background(), apiKey))

	handler := AuthMiddleware(s, "admin-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tenant-1", TenantFromContext(r.Context()))
		assert.False(t, api.IsAdmin(r.Context()))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+rawKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	s := testStore(t)

	handler := AuthMiddleware(s, "admin-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	s := testStore(t)

	handler := AuthMiddleware(s, "admin-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminOnly_AllowsAdmin(t *testing.T) {
	handler := AdminOnly("admin-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx := context.WithValue(context.Background(), api.IsAdminKey, true)
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminOnly_BlocksNonAdmin(t *testing.T) {
	handler := AdminOnly("admin-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	// No admin flag in context.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestTenantFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), api.TenantIDKey, "my-tenant")
	assert.Equal(t, "my-tenant", TenantFromContext(ctx))
}

func TestTenantFromContext_Empty(t *testing.T) {
	assert.Equal(t, "", TenantFromContext(context.Background()))
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
