package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type tenantStore struct {
	db *sqlx.DB
}

func (s *tenantStore) Create(ctx context.Context, t *Tenant) error {
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now
	t.Active = true

	const q = `INSERT INTO TENANT (ID, NAME, WORKSPACE, TEAM_ID, CREATED_AT, UPDATED_AT, ACTIVE)
		VALUES (:ID, :NAME, :WORKSPACE, :TEAM_ID, :CREATED_AT, :UPDATED_AT, :ACTIVE)`
	if _, err := s.db.NamedExecContext(ctx, q, t); err != nil {
		return fmt.Errorf("tenant: create: %w", err)
	}
	return nil
}

func (s *tenantStore) Get(ctx context.Context, id string) (*Tenant, error) {
	var t Tenant
	const q = `SELECT ID, NAME, WORKSPACE, TEAM_ID, CREATED_AT, UPDATED_AT, ACTIVE
		FROM TENANT WHERE ID = ? AND ACTIVE = TRUE`
	if err := s.db.GetContext(ctx, &t, q, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("tenant: not found: %s", id)
		}
		return nil, fmt.Errorf("tenant: get: %w", err)
	}
	return &t, nil
}

func (s *tenantStore) Deactivate(ctx context.Context, id string) error {
	now := time.Now().UTC()
	const q = `UPDATE TENANT SET ACTIVE = FALSE, UPDATED_AT = ? WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, now, id)
	if err != nil {
		return fmt.Errorf("tenant: deactivate: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant: deactivate: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("tenant: not found: %s", id)
	}
	return nil
}
