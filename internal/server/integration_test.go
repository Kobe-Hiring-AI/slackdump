package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rusq/slackdump/v4/internal/fixtures"
	"github.com/rusq/slackdump/v4/internal/server/api"
	"github.com/rusq/slackdump/v4/internal/server/engine"
	"github.com/rusq/slackdump/v4/internal/server/store"
)

// TestIntegration_ExportJobStarts verifies the full server flow with real Slack
// credentials: create tenant via cookie-only auth, submit an export, and
// confirm the job transitions to "running".
//
// Requires:
//   - Firefox with a Slack session on Linux
//   - SLACK_WORKSPACE env var (e.g. "mycompany")
//
// Skips gracefully when either is unavailable.
func TestIntegration_ExportJobStarts(t *testing.T) {
	fixtures.SkipInCI(t)

	workspace := os.Getenv("SLACK_WORKSPACE")
	if workspace == "" {
		workspace = "kobe-ai"
	}

	cookie := fixtures.FirefoxSlackDCookie(t)

	// Set up server with real engine and real Slack validator.
	dir := t.TempDir()
	s, err := store.New(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	encryptionKey := encKey()
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))

	eng := engine.New(s, encryptionKey, dataDir, 1)

	sv := newServer(Config{
		Addr:          ":0",
		AdminKey:      "admin-key",
		EncryptionKey: encryptionKey,
		DataDir:       dataDir,
	}, s, eng, &slackValidator{})

	ts := httptest.NewServer(sv.srv.Handler)
	t.Cleanup(ts.Close)

	adminKey := "admin-key"

	// 1. Create tenant with cookie-only auth (validates against real Slack).
	tenant := jsonRequest[api.TenantResponse](t, ts, "POST", "/tenants", adminKey, api.TenantRequest{
		Name:        "integration-test",
		Workspace:   workspace,
		SlackCookie: cookie,
	})
	require.NotEmpty(t, tenant.ID)
	assert.Equal(t, workspace, tenant.Workspace)
	assert.NotEmpty(t, tenant.TeamID)
	assert.NotEmpty(t, tenant.APIKey)
	t.Logf("tenant created: id=%s workspace=%s team_id=%s", tenant.ID, tenant.Workspace, tenant.TeamID)

	tenantKey := tenant.APIKey

	// 2. Submit export job.
	export := jsonRequest[api.ExportResponse](t, ts, "POST", "/tenants/"+tenant.ID+"/exports", tenantKey, api.ExportRequest{})
	require.Equal(t, store.JobStatusPending, export.Status)
	t.Logf("export created: id=%s", export.ID)

	// 3. Poll until job starts running (or completes/fails).
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for export job to start")
		case <-ticker.C:
			status := jsonRequest[api.ExportResponse](t, ts, "GET", "/tenants/"+tenant.ID+"/exports/"+export.ID, tenantKey, nil)
			switch status.Status {
			case store.JobStatusRunning:
				t.Log("job is running — cancelling")
				eng.Cancel(export.ID)
				eng.Wait()
				return
			case store.JobStatusCompleted:
				t.Log("job completed")
				return
			case store.JobStatusFailed:
				t.Fatalf("job failed: %s", status.ErrorMsg)
			}
		}
	}
}

// jsonRequest is a test helper that sends a JSON request and decodes the response.
func jsonRequest[T any](t *testing.T, ts *httptest.Server, method, path, bearer string, body any) T {
	t.Helper()

	var buf *bytes.Buffer
	if body != nil {
		buf = new(bytes.Buffer)
		require.NoError(t, json.NewEncoder(buf).Encode(body))
	}

	var bodyReader *bytes.Buffer
	if buf != nil {
		bodyReader = buf
	}

	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequest(method, ts.URL+path, bodyReader)
	} else {
		req, err = http.NewRequest(method, ts.URL+path, nil)
	}
	require.NoError(t, err)

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, expectStatus(method), resp.StatusCode, "unexpected status for %s %s", method, path)

	var v T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return v
}

func expectStatus(method string) int {
	if method == "POST" {
		return http.StatusCreated
	}
	return http.StatusOK
}
