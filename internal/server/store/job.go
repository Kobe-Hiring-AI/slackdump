package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type jobStore struct {
	db *sqlx.DB
}

func (s *jobStore) Create(ctx context.Context, j *ExportJob) error {
	now := time.Now().UTC()
	j.CreatedAt = now
	j.UpdatedAt = now
	if j.Status == "" {
		j.Status = JobStatusPending
	}

	const q = `INSERT INTO EXPORT_JOB (ID, TENANT_ID, STATUS, CHANNELS, TRIGGERED_BY, ERROR_MSG, OUTPUT_PATH, STARTED_AT, FINISHED_AT, CREATED_AT, UPDATED_AT)
		VALUES (:ID, :TENANT_ID, :STATUS, :CHANNELS, :TRIGGERED_BY, :ERROR_MSG, :OUTPUT_PATH, :STARTED_AT, :FINISHED_AT, :CREATED_AT, :UPDATED_AT)`
	if _, err := s.db.NamedExecContext(ctx, q, j); err != nil {
		return fmt.Errorf("job: create: %w", err)
	}
	return nil
}

func (s *jobStore) Get(ctx context.Context, id string) (*ExportJob, error) {
	var j ExportJob
	const q = `SELECT ID, TENANT_ID, STATUS, CHANNELS, TRIGGERED_BY, ERROR_MSG, OUTPUT_PATH, STARTED_AT, FINISHED_AT, CREATED_AT, UPDATED_AT
		FROM EXPORT_JOB WHERE ID = ?`
	if err := s.db.GetContext(ctx, &j, q, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("job: not found: %s", id)
		}
		return nil, fmt.Errorf("job: get: %w", err)
	}
	return &j, nil
}

func (s *jobStore) ListByTenant(ctx context.Context, tenantID string) ([]*ExportJob, error) {
	var jobs []*ExportJob
	const q = `SELECT ID, TENANT_ID, STATUS, CHANNELS, TRIGGERED_BY, ERROR_MSG, OUTPUT_PATH, STARTED_AT, FINISHED_AT, CREATED_AT, UPDATED_AT
		FROM EXPORT_JOB WHERE TENANT_ID = ? ORDER BY CREATED_AT DESC`
	if err := s.db.SelectContext(ctx, &jobs, q, tenantID); err != nil {
		return nil, fmt.Errorf("job: list: %w", err)
	}
	return jobs, nil
}

func (s *jobStore) HasActive(ctx context.Context, tenantID string) (bool, error) {
	var count int
	const q = `SELECT COUNT(*) FROM EXPORT_JOB WHERE TENANT_ID = ? AND STATUS IN ('pending', 'running')`
	if err := s.db.GetContext(ctx, &count, q, tenantID); err != nil {
		return false, fmt.Errorf("job: has active: %w", err)
	}
	return count > 0, nil
}

func (s *jobStore) UpdateStatus(ctx context.Context, id, status, errorMsg string) error {
	now := time.Now().UTC()
	const q = `UPDATE EXPORT_JOB SET STATUS = ?, ERROR_MSG = ?, UPDATED_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, status, errorMsg, now, id)
	if err != nil {
		return fmt.Errorf("job: update status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("job: update status: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("job: not found: %s", id)
	}
	return nil
}

func (s *jobStore) SetRunning(ctx context.Context, id string) error {
	now := time.Now().UTC()
	const q = `UPDATE EXPORT_JOB SET STATUS = 'running', STARTED_AT = ?, UPDATED_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, now, now, id)
	if err != nil {
		return fmt.Errorf("job: set running: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("job: set running: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("job: not found: %s", id)
	}
	return nil
}

func (s *jobStore) SetCompleted(ctx context.Context, id, outputPath string) error {
	now := time.Now().UTC()
	const q = `UPDATE EXPORT_JOB SET STATUS = 'completed', OUTPUT_PATH = ?, FINISHED_AT = ?, UPDATED_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, outputPath, now, now, id)
	if err != nil {
		return fmt.Errorf("job: set completed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("job: set completed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("job: not found: %s", id)
	}
	return nil
}

func (s *jobStore) ListResumable(ctx context.Context) ([]*ExportJob, error) {
	const q = `UPDATE EXPORT_JOB SET STATUS = 'pending', STARTED_AT = NULL, UPDATED_AT = ?
		WHERE STATUS IN ('pending', 'running')
		RETURNING ID, TENANT_ID, STATUS, CHANNELS, TRIGGERED_BY, ERROR_MSG, OUTPUT_PATH, STARTED_AT, FINISHED_AT, CREATED_AT, UPDATED_AT`
	now := time.Now().UTC()
	var jobs []*ExportJob
	if err := s.db.SelectContext(ctx, &jobs, q, now); err != nil {
		return nil, fmt.Errorf("job: list resumable: %w", err)
	}
	return jobs, nil
}

func (s *jobStore) SetFailed(ctx context.Context, id, errorMsg string) error {
	now := time.Now().UTC()
	const q = `UPDATE EXPORT_JOB SET STATUS = 'failed', ERROR_MSG = ?, FINISHED_AT = ?, UPDATED_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, errorMsg, now, now, id)
	if err != nil {
		return fmt.Errorf("job: set failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("job: set failed: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("job: not found: %s", id)
	}
	return nil
}
