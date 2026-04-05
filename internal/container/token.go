package container

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GenerateAPIToken creates a random 32-byte hex token and its SHA-256 hash.
func GenerateAPIToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plaintext = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return plaintext, hash, nil
}

// StoreToken inserts an API token hash into ae_api_tokens.
func StoreToken(ctx context.Context, db *pgxpool.Pool, tokenID, employeeID, companyID, hash string) error {
	_, err := db.Exec(ctx,
		`INSERT INTO ae_api_tokens (id, employee_id, company_id, token_hash) VALUES ($1, $2, $3, $4)`,
		tokenID, employeeID, companyID, hash,
	)
	return err
}

// ValidateToken looks up a plaintext token by its hash and returns the
// associated employee_id and company_id. Returns an error if not found or revoked.
func ValidateToken(ctx context.Context, db *pgxpool.Pool, plaintext string) (employeeID, companyID string, err error) {
	h := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(h[:])
	err = db.QueryRow(ctx,
		`SELECT employee_id, company_id FROM ae_api_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
		hash,
	).Scan(&employeeID, &companyID)
	if err != nil {
		return "", "", fmt.Errorf("invalid or revoked token")
	}
	return employeeID, companyID, nil
}
