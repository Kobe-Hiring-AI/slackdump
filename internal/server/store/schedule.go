package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type scheduleStore struct {
	db *sqlx.DB
}

func (s *scheduleStore) Upsert(ctx context.Context, sc *Schedule) error {
	const q = `INSERT INTO SCHEDULE (ID, TENANT_ID, ENABLED, HOUR_UTC, JITTER_MIN, LAST_RUN_AT, NEXT_RUN_AT, CREATED_AT)
		VALUES (:ID, :TENANT_ID, :ENABLED, :HOUR_UTC, :JITTER_MIN, :LAST_RUN_AT, :NEXT_RUN_AT, :CREATED_AT)
		ON CONFLICT(TENANT_ID) DO UPDATE SET
			ENABLED = excluded.ENABLED,
			HOUR_UTC = excluded.HOUR_UTC,
			JITTER_MIN = excluded.JITTER_MIN,
			NEXT_RUN_AT = excluded.NEXT_RUN_AT`
	if _, err := s.db.NamedExecContext(ctx, q, sc); err != nil {
		return fmt.Errorf("schedule: upsert: %w", err)
	}
	return nil
}

func (s *scheduleStore) GetByTenant(ctx context.Context, tenantID string) (*Schedule, error) {
	var sc Schedule
	const q = `SELECT ID, TENANT_ID, ENABLED, HOUR_UTC, JITTER_MIN, LAST_RUN_AT, NEXT_RUN_AT, CREATED_AT
		FROM SCHEDULE WHERE TENANT_ID = ?`
	if err := s.db.GetContext(ctx, &sc, q, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("schedule: not found for tenant: %s", tenantID)
		}
		return nil, fmt.Errorf("schedule: get: %w", err)
	}
	return &sc, nil
}

func (s *scheduleStore) ListDue(ctx context.Context, now time.Time) ([]*Schedule, error) {
	var schedules []*Schedule
	const q = `SELECT ID, TENANT_ID, ENABLED, HOUR_UTC, JITTER_MIN, LAST_RUN_AT, NEXT_RUN_AT, CREATED_AT
		FROM SCHEDULE WHERE ENABLED = TRUE AND NEXT_RUN_AT <= ?`
	if err := s.db.SelectContext(ctx, &schedules, q, now); err != nil {
		return nil, fmt.Errorf("schedule: list due: %w", err)
	}
	return schedules, nil
}

func (s *scheduleStore) UpdateLastRun(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	const q = `UPDATE SCHEDULE SET LAST_RUN_AT = ?, NEXT_RUN_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, lastRun, nextRun, id)
	if err != nil {
		return fmt.Errorf("schedule: update last run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("schedule: update last run: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("schedule: not found: %s", id)
	}
	return nil
}
