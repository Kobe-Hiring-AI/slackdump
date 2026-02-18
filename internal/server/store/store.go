package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Tenant represents a registered workspace tenant.
type Tenant struct {
	ID        string    `db:"ID"         json:"id"`
	Name      string    `db:"NAME"       json:"name"`
	Workspace string    `db:"WORKSPACE"  json:"workspace"`
	TeamID    string    `db:"TEAM_ID"    json:"team_id"`
	CreatedAt time.Time `db:"CREATED_AT" json:"created_at"`
	UpdatedAt time.Time `db:"UPDATED_AT" json:"updated_at"`
	Active    bool      `db:"ACTIVE"     json:"active"`
}

// APIKey represents an API key for a tenant.
type APIKey struct {
	ID        string     `db:"ID"         json:"id"`
	TenantID  string     `db:"TENANT_ID"  json:"tenant_id"`
	KeyHash   string     `db:"KEY_HASH"   json:"-"`
	KeyPrefix string     `db:"KEY_PREFIX" json:"key_prefix"`
	Name      string     `db:"NAME"       json:"name"`
	CreatedAt time.Time  `db:"CREATED_AT" json:"created_at"`
	ExpiresAt *time.Time `db:"EXPIRES_AT" json:"expires_at,omitempty"`
	Active    bool       `db:"ACTIVE"     json:"active"`
}

// Credential holds encrypted Slack credentials for a tenant.
type Credential struct {
	ID        string    `db:"ID"         json:"id"`
	TenantID  string    `db:"TENANT_ID"  json:"tenant_id"`
	TokenEnc  []byte    `db:"TOKEN_ENC"  json:"-"`
	CookieEnc []byte    `db:"COOKIE_ENC" json:"-"`
	CreatedAt time.Time `db:"CREATED_AT" json:"created_at"`
	UpdatedAt time.Time `db:"UPDATED_AT" json:"updated_at"`
}

// ExportJob represents a running or completed export job.
type ExportJob struct {
	ID          string     `db:"ID"           json:"id"`
	TenantID    string     `db:"TENANT_ID"    json:"tenant_id"`
	Status      string     `db:"STATUS"       json:"status"`
	Channels    string     `db:"CHANNELS"     json:"channels"`
	TriggeredBy string     `db:"TRIGGERED_BY" json:"triggered_by"`
	ErrorMsg    string     `db:"ERROR_MSG"    json:"error_msg,omitempty"`
	OutputPath  string     `db:"OUTPUT_PATH"  json:"output_path,omitempty"`
	StartedAt   *time.Time `db:"STARTED_AT"   json:"started_at,omitempty"`
	FinishedAt  *time.Time `db:"FINISHED_AT"  json:"finished_at,omitempty"`
	CreatedAt   time.Time  `db:"CREATED_AT"   json:"created_at"`
	UpdatedAt   time.Time  `db:"UPDATED_AT"   json:"updated_at"`
}

// Job status constants.
const (
	JobStatusPending   = "pending"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
)

// Schedule represents a tenant's recurring export schedule.
type Schedule struct {
	ID         string     `db:"ID"          json:"id"`
	TenantID   string     `db:"TENANT_ID"   json:"tenant_id"`
	Enabled    bool       `db:"ENABLED"     json:"enabled"`
	HourUTC    int        `db:"HOUR_UTC"    json:"hour_utc"`
	JitterMin  int        `db:"JITTER_MIN"  json:"jitter_min"`
	LastRunAt  *time.Time `db:"LAST_RUN_AT" json:"last_run_at,omitempty"`
	NextRunAt  *time.Time `db:"NEXT_RUN_AT" json:"next_run_at,omitempty"`
	CreatedAt  time.Time  `db:"CREATED_AT"  json:"created_at"`
}

// TenantStore manages tenant records.
type TenantStore interface {
	Create(ctx context.Context, t *Tenant) error
	Get(ctx context.Context, id string) (*Tenant, error)
	Deactivate(ctx context.Context, id string) error
}

// APIKeyStore manages API keys.
type APIKeyStore interface {
	Create(ctx context.Context, k *APIKey) error
	// LookupByHash returns the API key matching the given SHA-256 hash.
	LookupByHash(ctx context.Context, hash string) (*APIKey, error)
	ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error)
	Revoke(ctx context.Context, id string) error
}

// CredentialStore manages encrypted credentials.
type CredentialStore interface {
	Upsert(ctx context.Context, c *Credential) error
	GetByTenant(ctx context.Context, tenantID string) (*Credential, error)
}

// JobStore manages export jobs.
type JobStore interface {
	Create(ctx context.Context, j *ExportJob) error
	Get(ctx context.Context, id string) (*ExportJob, error)
	ListByTenant(ctx context.Context, tenantID string) ([]*ExportJob, error)
	ListResumable(ctx context.Context) ([]*ExportJob, error)
	UpdateStatus(ctx context.Context, id, status, errorMsg string) error
	SetRunning(ctx context.Context, id string) error
	SetCompleted(ctx context.Context, id, outputPath string) error
	SetFailed(ctx context.Context, id, errorMsg string) error
}

// ScheduleStore manages recurring schedules.
type ScheduleStore interface {
	Upsert(ctx context.Context, s *Schedule) error
	GetByTenant(ctx context.Context, tenantID string) (*Schedule, error)
	ListDue(ctx context.Context, now time.Time) ([]*Schedule, error)
	UpdateLastRun(ctx context.Context, id string, lastRun, nextRun time.Time) error
}

// Store aggregates all sub-stores.
type Store struct {
	DB          *sqlx.DB
	Tenants     TenantStore
	APIKeys     APIKeyStore
	Credentials CredentialStore
	Jobs        JobStore
	Schedules   ScheduleStore
}

var dbInitCommands = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA foreign_keys=ON",
}

// New opens (or creates) the SQLite database at dbPath, runs migrations, and
// returns a fully initialised Store.
func New(ctx context.Context, dbPath string) (*Store, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	for _, q := range dbInitCommands {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return nil, fmt.Errorf("store: init: %w", err)
		}
	}
	if err := migrate(ctx, db.DB); err != nil {
		return nil, err
	}
	return &Store{
		DB:          db,
		Tenants:     &tenantStore{db: db},
		APIKeys:     &apiKeyStore{db: db},
		Credentials: &credentialStore{db: db},
		Jobs:        &jobStore{db: db},
		Schedules:   &scheduleStore{db: db},
	}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: migrations fs: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub, goose.WithAllowOutofOrder(true))
	if err != nil {
		return fmt.Errorf("store: goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.DB.Close()
}
