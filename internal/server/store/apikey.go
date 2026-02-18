package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type apiKeyStore struct {
	db *sqlx.DB
}

func (s *apiKeyStore) Create(ctx context.Context, k *APIKey) error {
	now := time.Now().UTC()
	k.CreatedAt = now
	k.Active = true

	const q = `INSERT INTO API_KEY (ID, TENANT_ID, KEY_HASH, KEY_PREFIX, NAME, CREATED_AT, EXPIRES_AT, ACTIVE)
		VALUES (:ID, :TENANT_ID, :KEY_HASH, :KEY_PREFIX, :NAME, :CREATED_AT, :EXPIRES_AT, :ACTIVE)`
	if _, err := s.db.NamedExecContext(ctx, q, k); err != nil {
		return fmt.Errorf("apikey: create: %w", err)
	}
	return nil
}

func (s *apiKeyStore) LookupByHash(ctx context.Context, hash string) (*APIKey, error) {
	var k APIKey
	const q = `SELECT ID, TENANT_ID, KEY_HASH, KEY_PREFIX, NAME, CREATED_AT, EXPIRES_AT, ACTIVE
		FROM API_KEY WHERE KEY_HASH = ? AND ACTIVE = TRUE AND (EXPIRES_AT IS NULL OR EXPIRES_AT > datetime('now'))`
	if err := s.db.GetContext(ctx, &k, q, hash); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("apikey: not found")
		}
		return nil, fmt.Errorf("apikey: lookup: %w", err)
	}
	return &k, nil
}

func (s *apiKeyStore) ListByTenant(ctx context.Context, tenantID string) ([]*APIKey, error) {
	var keys []*APIKey
	const q = `SELECT ID, TENANT_ID, KEY_HASH, KEY_PREFIX, NAME, CREATED_AT, EXPIRES_AT, ACTIVE
		FROM API_KEY WHERE TENANT_ID = ?`
	if err := s.db.SelectContext(ctx, &keys, q, tenantID); err != nil {
		return nil, fmt.Errorf("apikey: list: %w", err)
	}
	return keys, nil
}

func (s *apiKeyStore) Revoke(ctx context.Context, id string) error {
	const q = `UPDATE API_KEY SET ACTIVE = FALSE WHERE ID = ?`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("apikey: revoke: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apikey: revoke: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("apikey: not found: %s", id)
	}
	return nil
}

// GenerateAPIKey generates a cryptographically random API key.
// It returns 32 random bytes hex-encoded as a 64-character string.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("apikey: generate: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HashKey returns the SHA-256 hex digest of the given key.
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// KeyPrefix returns the first 8 characters of the given key.
func KeyPrefix(key string) string {
	if len(key) < 8 {
		return key
	}
	return key[:8]
}
