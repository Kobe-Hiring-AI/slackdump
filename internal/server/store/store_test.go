package store

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(context.Background(), filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func createTestTenant(t *testing.T, s *Store, id string) *Tenant {
	t.Helper()
	tenant := &Tenant{
		ID:        id,
		Name:      "Test Tenant " + id,
		Workspace: "workspace-" + id,
		TeamID:    "T" + id,
	}
	require.NoError(t, s.Tenants.Create(context.Background(), tenant))
	return tenant
}

// --- Tenant Tests ---

func TestTenantCreate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	tenant := &Tenant{
		ID:        "t1",
		Name:      "Acme Corp",
		Workspace: "acme",
		TeamID:    "T001",
	}
	require.NoError(t, s.Tenants.Create(ctx, tenant))
	assert.True(t, tenant.Active)
	assert.False(t, tenant.CreatedAt.IsZero())
}

func TestTenantGet(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	createTestTenant(t, s, "t1")

	got, err := s.Tenants.Get(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, "t1", got.ID)
	assert.Equal(t, "Test Tenant t1", got.Name)
	assert.True(t, got.Active)
}

func TestTenantGetNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	_, err := s.Tenants.Get(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTenantDeactivate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	createTestTenant(t, s, "t1")

	require.NoError(t, s.Tenants.Deactivate(ctx, "t1"))

	// Should not be found after deactivation (Get filters by ACTIVE=TRUE).
	_, err := s.Tenants.Get(ctx, "t1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTenantDeactivateNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	err := s.Tenants.Deactivate(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- API Key Tests ---

func TestAPIKeyCreate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	raw, err := GenerateAPIKey()
	require.NoError(t, err)

	key := &APIKey{
		ID:        "k1",
		TenantID:  "t1",
		KeyHash:   HashKey(raw),
		KeyPrefix: KeyPrefix(raw),
		Name:      "test-key",
	}
	require.NoError(t, s.APIKeys.Create(ctx, key))
	assert.True(t, key.Active)
	assert.False(t, key.CreatedAt.IsZero())
}

func TestAPIKeyLookupByHash(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	raw, err := GenerateAPIKey()
	require.NoError(t, err)
	hash := HashKey(raw)

	key := &APIKey{
		ID:        "k1",
		TenantID:  "t1",
		KeyHash:   hash,
		KeyPrefix: KeyPrefix(raw),
		Name:      "lookup-key",
	}
	require.NoError(t, s.APIKeys.Create(ctx, key))

	got, err := s.APIKeys.LookupByHash(ctx, hash)
	require.NoError(t, err)
	assert.Equal(t, "k1", got.ID)
	assert.Equal(t, "t1", got.TenantID)
}

func TestAPIKeyLookupByHashNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	_, err := s.APIKeys.LookupByHash(ctx, "nonexistent-hash")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIKeyExpiry(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	raw, err := GenerateAPIKey()
	require.NoError(t, err)
	hash := HashKey(raw)

	past := time.Now().Add(-1 * time.Hour).UTC()
	key := &APIKey{
		ID:        "k-expired",
		TenantID:  "t1",
		KeyHash:   hash,
		KeyPrefix: KeyPrefix(raw),
		Name:      "expired-key",
		ExpiresAt: &past,
	}
	require.NoError(t, s.APIKeys.Create(ctx, key))

	// Expired key should not be found.
	_, err = s.APIKeys.LookupByHash(ctx, hash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIKeyRevoke(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	raw, err := GenerateAPIKey()
	require.NoError(t, err)
	hash := HashKey(raw)

	key := &APIKey{
		ID:        "k-revoke",
		TenantID:  "t1",
		KeyHash:   hash,
		KeyPrefix: KeyPrefix(raw),
		Name:      "revokable-key",
	}
	require.NoError(t, s.APIKeys.Create(ctx, key))
	require.NoError(t, s.APIKeys.Revoke(ctx, "k-revoke"))

	// Revoked key should not be found via LookupByHash.
	_, err = s.APIKeys.LookupByHash(ctx, hash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIKeyRevokeNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	err := s.APIKeys.Revoke(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAPIKeyListByTenant(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")
	createTestTenant(t, s, "t2")

	for i, tid := range []string{"t1", "t1", "t2"} {
		raw, err := GenerateAPIKey()
		require.NoError(t, err)
		key := &APIKey{
			ID:        "k" + string(rune('a'+i)),
			TenantID:  tid,
			KeyHash:   HashKey(raw),
			KeyPrefix: KeyPrefix(raw),
			Name:      "key",
		}
		require.NoError(t, s.APIKeys.Create(ctx, key))
	}

	keys, err := s.APIKeys.ListByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.Len(t, keys, 2)

	keys, err = s.APIKeys.ListByTenant(ctx, "t2")
	require.NoError(t, err)
	assert.Len(t, keys, 1)
}

// --- API Key Helper Tests ---

func TestGenerateAPIKeyUniqueness(t *testing.T) {
	k1, err := GenerateAPIKey()
	require.NoError(t, err)
	k2, err := GenerateAPIKey()
	require.NoError(t, err)
	assert.NotEqual(t, k1, k2)
	assert.Len(t, k1, 64)
	assert.Len(t, k2, 64)
}

func TestHashKeyDeterminism(t *testing.T) {
	key := "test-api-key-12345"
	h1 := HashKey(key)
	h2 := HashKey(key)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64) // SHA-256 hex = 64 chars
}

func TestKeyPrefix(t *testing.T) {
	assert.Equal(t, "abcdefgh", KeyPrefix("abcdefghij"))
	assert.Equal(t, "short", KeyPrefix("short"))
	assert.Equal(t, "", KeyPrefix(""))
}

// --- Credential Tests ---

func TestCredentialUpsertAndGet(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	cred := &Credential{
		ID:        "c1",
		TenantID:  "t1",
		TokenEnc:  []byte("encrypted-token"),
		CookieEnc: []byte("encrypted-cookie"),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Credentials.Upsert(ctx, cred))

	got, err := s.Credentials.GetByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, "c1", got.ID)
	assert.Equal(t, []byte("encrypted-token"), got.TokenEnc)
	assert.Equal(t, []byte("encrypted-cookie"), got.CookieEnc)
}

func TestCredentialUpsertUpdate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	cred := &Credential{
		ID:        "c1",
		TenantID:  "t1",
		TokenEnc:  []byte("old-token"),
		CookieEnc: []byte("old-cookie"),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Credentials.Upsert(ctx, cred))

	// Upsert again with new values.
	cred2 := &Credential{
		ID:        "c2",
		TenantID:  "t1",
		TokenEnc:  []byte("new-token"),
		CookieEnc: []byte("new-cookie"),
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Credentials.Upsert(ctx, cred2))

	got, err := s.Credentials.GetByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, []byte("new-token"), got.TokenEnc)
	assert.Equal(t, []byte("new-cookie"), got.CookieEnc)
}

func TestCredentialGetNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	_, err := s.Credentials.GetByTenant(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- Encrypt / Decrypt Tests ---

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	plaintext := []byte("super-secret-slack-token")
	ciphertext, err := Encrypt(key, plaintext)
	require.NoError(t, err)

	decrypted, err := Decrypt(key, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	ciphertext, err := Encrypt(key, []byte{})
	require.NoError(t, err)

	decrypted, err := Decrypt(key, ciphertext)
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	_, err := rand.Read(key1)
	require.NoError(t, err)
	_, err = rand.Read(key2)
	require.NoError(t, err)

	ciphertext, err := Encrypt(key1, []byte("secret"))
	require.NoError(t, err)

	_, err = Decrypt(key2, ciphertext)
	require.Error(t, err)
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)

	_, err = Decrypt(key, []byte("short"))
	require.Error(t, err)
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	_, err := Encrypt([]byte("short"), []byte("data"))
	require.Error(t, err)
}

// --- Job Tests ---

func TestJobCreate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001,C002",
		TriggeredBy: "manual",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))
	assert.Equal(t, JobStatusPending, job.Status)
	assert.False(t, job.CreatedAt.IsZero())
}

func TestJobGet(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001",
		TriggeredBy: "schedule",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))

	got, err := s.Jobs.Get(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, "j1", got.ID)
	assert.Equal(t, "t1", got.TenantID)
	assert.Equal(t, JobStatusPending, got.Status)
}

func TestJobGetNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	_, err := s.Jobs.Get(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestJobListByTenant(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")
	createTestTenant(t, s, "t2")

	for _, jid := range []string{"j1", "j2"} {
		require.NoError(t, s.Jobs.Create(ctx, &ExportJob{
			ID:          jid,
			TenantID:    "t1",
			Channels:    "C001",
			TriggeredBy: "manual",
		}))
	}
	require.NoError(t, s.Jobs.Create(ctx, &ExportJob{
		ID:          "j3",
		TenantID:    "t2",
		Channels:    "C002",
		TriggeredBy: "manual",
	}))

	jobs, err := s.Jobs.ListByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	jobs, err = s.Jobs.ListByTenant(ctx, "t2")
	require.NoError(t, err)
	assert.Len(t, jobs, 1)
}

func TestJobUpdateStatus(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001",
		TriggeredBy: "manual",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))

	require.NoError(t, s.Jobs.UpdateStatus(ctx, "j1", JobStatusRunning, ""))
	got, err := s.Jobs.Get(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, JobStatusRunning, got.Status)
}

func TestJobUpdateStatusNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	err := s.Jobs.UpdateStatus(ctx, "nonexistent", JobStatusRunning, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestJobSetRunning(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001",
		TriggeredBy: "manual",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))
	require.NoError(t, s.Jobs.SetRunning(ctx, "j1"))

	got, err := s.Jobs.Get(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, JobStatusRunning, got.Status)
	assert.NotNil(t, got.StartedAt)
}

func TestJobSetCompleted(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001",
		TriggeredBy: "manual",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))
	require.NoError(t, s.Jobs.SetRunning(ctx, "j1"))
	require.NoError(t, s.Jobs.SetCompleted(ctx, "j1", "/output/archive.zip"))

	got, err := s.Jobs.Get(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, JobStatusCompleted, got.Status)
	assert.Equal(t, "/output/archive.zip", got.OutputPath)
	assert.NotNil(t, got.FinishedAt)
}

func TestJobSetFailed(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	job := &ExportJob{
		ID:          "j1",
		TenantID:    "t1",
		Channels:    "C001",
		TriggeredBy: "manual",
	}
	require.NoError(t, s.Jobs.Create(ctx, job))
	require.NoError(t, s.Jobs.SetRunning(ctx, "j1"))
	require.NoError(t, s.Jobs.SetFailed(ctx, "j1", "rate limited"))

	got, err := s.Jobs.Get(ctx, "j1")
	require.NoError(t, err)
	assert.Equal(t, JobStatusFailed, got.Status)
	assert.Equal(t, "rate limited", got.ErrorMsg)
	assert.NotNil(t, got.FinishedAt)
}

// --- ListResumable Tests ---

func TestJobListResumable(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	// Create jobs in various states.
	for _, tc := range []struct {
		id     string
		status string
	}{
		{"j-pending", JobStatusPending},
		{"j-running", JobStatusRunning},
		{"j-completed", JobStatusCompleted},
		{"j-failed", JobStatusFailed},
	} {
		job := &ExportJob{
			ID:          tc.id,
			TenantID:    "t1",
			Channels:    "C001",
			TriggeredBy: "manual",
			Status:      tc.status,
		}
		require.NoError(t, s.Jobs.Create(ctx, job))
		if tc.status == JobStatusRunning {
			require.NoError(t, s.Jobs.SetRunning(ctx, tc.id))
		}
	}

	jobs, err := s.Jobs.ListResumable(ctx)
	require.NoError(t, err)

	// Should return only the pending and (formerly) running jobs.
	assert.Len(t, jobs, 2)
	for _, j := range jobs {
		assert.Equal(t, JobStatusPending, j.Status, "all resumable jobs should be reset to pending")
	}

	// Verify the running job was actually reset in the DB.
	got, err := s.Jobs.Get(ctx, "j-running")
	require.NoError(t, err)
	assert.Equal(t, JobStatusPending, got.Status)
	assert.Nil(t, got.StartedAt)
}

func TestJobListResumableEmpty(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	// Only completed/failed jobs — nothing resumable.
	require.NoError(t, s.Jobs.Create(ctx, &ExportJob{
		ID: "j1", TenantID: "t1", Channels: "C001", TriggeredBy: "manual", Status: JobStatusCompleted,
	}))
	require.NoError(t, s.Jobs.Create(ctx, &ExportJob{
		ID: "j2", TenantID: "t1", Channels: "C001", TriggeredBy: "manual", Status: JobStatusFailed,
	}))

	jobs, err := s.Jobs.ListResumable(ctx)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// --- Schedule Tests ---

func TestScheduleUpsertAndGet(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	nextRun := time.Now().Add(24 * time.Hour).UTC()
	sched := &Schedule{
		ID:        "s1",
		TenantID:  "t1",
		Enabled:   true,
		HourUTC:   3,
		JitterMin: 30,
		NextRunAt: &nextRun,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Schedules.Upsert(ctx, sched))

	got, err := s.Schedules.GetByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, "s1", got.ID)
	assert.Equal(t, 3, got.HourUTC)
	assert.Equal(t, 30, got.JitterMin)
	assert.True(t, got.Enabled)
}

func TestScheduleUpsertUpdate(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	nextRun := time.Now().Add(24 * time.Hour).UTC()
	sched := &Schedule{
		ID:        "s1",
		TenantID:  "t1",
		Enabled:   true,
		HourUTC:   3,
		JitterMin: 30,
		NextRunAt: &nextRun,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Schedules.Upsert(ctx, sched))

	// Update via upsert.
	newNext := time.Now().Add(48 * time.Hour).UTC()
	sched2 := &Schedule{
		ID:        "s2",
		TenantID:  "t1",
		Enabled:   false,
		HourUTC:   5,
		JitterMin: 15,
		NextRunAt: &newNext,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, s.Schedules.Upsert(ctx, sched2))

	got, err := s.Schedules.GetByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	assert.Equal(t, 5, got.HourUTC)
	assert.Equal(t, 15, got.JitterMin)
}

func TestScheduleGetNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	_, err := s.Schedules.GetByTenant(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestScheduleListDue(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")
	createTestTenant(t, s, "t2")
	createTestTenant(t, s, "t3")

	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// Due schedule.
	require.NoError(t, s.Schedules.Upsert(ctx, &Schedule{
		ID: "s1", TenantID: "t1", Enabled: true, HourUTC: 2, JitterMin: 60,
		NextRunAt: &past, CreatedAt: now,
	}))
	// Not due yet.
	require.NoError(t, s.Schedules.Upsert(ctx, &Schedule{
		ID: "s2", TenantID: "t2", Enabled: true, HourUTC: 2, JitterMin: 60,
		NextRunAt: &future, CreatedAt: now,
	}))
	// Due but disabled.
	require.NoError(t, s.Schedules.Upsert(ctx, &Schedule{
		ID: "s3", TenantID: "t3", Enabled: false, HourUTC: 2, JitterMin: 60,
		NextRunAt: &past, CreatedAt: now,
	}))

	due, err := s.Schedules.ListDue(ctx, now)
	require.NoError(t, err)
	assert.Len(t, due, 1)
	assert.Equal(t, "s1", due[0].ID)
}

func TestScheduleUpdateLastRun(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()
	createTestTenant(t, s, "t1")

	now := time.Now().UTC()
	nextRun := now.Add(24 * time.Hour)
	sched := &Schedule{
		ID: "s1", TenantID: "t1", Enabled: true, HourUTC: 2, JitterMin: 60,
		NextRunAt: &nextRun, CreatedAt: now,
	}
	require.NoError(t, s.Schedules.Upsert(ctx, sched))

	lastRun := now
	newNext := now.Add(48 * time.Hour)
	require.NoError(t, s.Schedules.UpdateLastRun(ctx, "s1", lastRun, newNext))

	got, err := s.Schedules.GetByTenant(ctx, "t1")
	require.NoError(t, err)
	assert.NotNil(t, got.LastRunAt)
	assert.NotNil(t, got.NextRunAt)
}

func TestScheduleUpdateLastRunNotFound(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	err := s.Schedules.UpdateLastRun(ctx, "nonexistent", time.Now(), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
