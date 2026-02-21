package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/jmoiron/sqlx"
)

type credentialStore struct {
	db *sqlx.DB
}

func (s *credentialStore) Upsert(ctx context.Context, c *Credential) error {
	now := time.Now().UTC()
	c.UpdatedAt = now

	const q = `INSERT INTO CREDENTIAL (ID, TENANT_ID, TOKEN_ENC, COOKIE_ENC, BOT_TOKEN_ENC, CREATED_AT, UPDATED_AT)
		VALUES (:ID, :TENANT_ID, :TOKEN_ENC, :COOKIE_ENC, :BOT_TOKEN_ENC, :CREATED_AT, :UPDATED_AT)
		ON CONFLICT(TENANT_ID) DO UPDATE SET
			TOKEN_ENC = excluded.TOKEN_ENC,
			COOKIE_ENC = excluded.COOKIE_ENC,
			BOT_TOKEN_ENC = excluded.BOT_TOKEN_ENC,
			UPDATED_AT = excluded.UPDATED_AT`
	if _, err := s.db.NamedExecContext(ctx, q, c); err != nil {
		return fmt.Errorf("credential: upsert: %w", err)
	}
	return nil
}

func (s *credentialStore) GetByTenant(ctx context.Context, tenantID string) (*Credential, error) {
	var c Credential
	const q = `SELECT ID, TENANT_ID, TOKEN_ENC, COOKIE_ENC, BOT_TOKEN_ENC, CREATED_AT, UPDATED_AT
		FROM CREDENTIAL WHERE TENANT_ID = ?`
	if err := s.db.GetContext(ctx, &c, q, tenantID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("credential: not found for tenant: %s", tenantID)
		}
		return nil, fmt.Errorf("credential: get: %w", err)
	}
	return &c, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with the given 32-byte key.
// The output is nonce(12 bytes) || ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts data produced by Encrypt using AES-256-GCM.
// It expects the input to be nonce(12 bytes) || ciphertext.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("decrypt: ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}
