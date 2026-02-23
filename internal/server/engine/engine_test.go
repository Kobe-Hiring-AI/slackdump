package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/rusq/slackdump/v4/internal/server/store"
)

// mockJobStore implements store.JobStore for testing.
type mockJobStore struct {
	resumable []*store.ExportJob
	resumeErr error

	mu      sync.Mutex
	running []string // IDs passed to SetRunning
	failed  []string // IDs passed to SetFailed
}

func (m *mockJobStore) Create(context.Context, *store.ExportJob) error                  { return nil }
func (m *mockJobStore) Get(context.Context, string) (*store.ExportJob, error)           { return nil, nil }
func (m *mockJobStore) ListByTenant(context.Context, string) ([]*store.ExportJob, error) { return nil, nil }
func (m *mockJobStore) UpdateStatus(context.Context, string, string, string) error       { return nil }
func (m *mockJobStore) SetCompleted(context.Context, string, string) error               { return nil }

func (m *mockJobStore) SetRunning(_ context.Context, id string) error {
	m.mu.Lock()
	m.running = append(m.running, id)
	m.mu.Unlock()
	return nil
}

func (m *mockJobStore) SetFailed(_ context.Context, id, _ string) error {
	m.mu.Lock()
	m.failed = append(m.failed, id)
	m.mu.Unlock()
	return nil
}

func (m *mockJobStore) ListResumable(context.Context) ([]*store.ExportJob, error) {
	return m.resumable, m.resumeErr
}

func (m *mockJobStore) HasActive(context.Context, string) (bool, error) { return false, nil }

// mockCredStore implements store.CredentialStore returning an error so that
// RunExport fails fast (we only care that jobs get submitted).
type mockCredStore struct{}

func (m *mockCredStore) Upsert(context.Context, *store.Credential) error { return nil }
func (m *mockCredStore) GetByTenant(context.Context, string) (*store.Credential, error) {
	return nil, errors.New("no creds")
}

func TestResumeJobs(t *testing.T) {
	jobs := []*store.ExportJob{
		{ID: "j1", TenantID: "t1", Status: store.JobStatusPending},
		{ID: "j2", TenantID: "t1", Status: store.JobStatusPending},
	}
	mock := &mockJobStore{resumable: jobs}
	st := &store.Store{
		Jobs:        mock,
		Credentials: &mockCredStore{},
	}
	eng := New(st, nil, t.TempDir(), 4, nil)

	if err := eng.ResumeJobs(context.Background()); err != nil {
		t.Fatalf("ResumeJobs: %v", err)
	}

	// Wait for the pool to drain (RunExport will fail on credentials, but
	// the jobs are still submitted and SetRunning is called).
	eng.Wait()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.running) != 2 {
		t.Errorf("expected 2 SetRunning calls, got %d", len(mock.running))
	}
}

func TestResumeJobs_StoreError(t *testing.T) {
	mock := &mockJobStore{resumeErr: errors.New("db down")}
	st := &store.Store{Jobs: mock}
	eng := New(st, nil, t.TempDir(), 4, nil)

	err := eng.ResumeJobs(context.Background())
	if err == nil {
		t.Fatal("expected error from ResumeJobs")
	}
}

func TestResumeJobs_NoJobs(t *testing.T) {
	mock := &mockJobStore{}
	st := &store.Store{Jobs: mock}
	eng := New(st, nil, t.TempDir(), 4, nil)

	if err := eng.ResumeJobs(context.Background()); err != nil {
		t.Fatalf("ResumeJobs with no jobs: %v", err)
	}
}
