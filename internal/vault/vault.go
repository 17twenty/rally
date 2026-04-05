package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a credential is not found in the vault.
var ErrNotFound = errors.New("vault: credential not found")

// AccessProvider represents a stored access credential (token not included).
type AccessProvider struct {
	ID           string
	CompanyID    string
	EmployeeID   string
	ProviderName string
	AccessType   string
	Scopes       []string
	Status       string
	CreatedAt    time.Time
}

// CredentialVault stores and retrieves credentials in the database.
// Tokens are stored as plaintext — the database itself is the security boundary.
type CredentialVault struct {
	db *pgxpool.Pool
}

// NewVault creates a CredentialVault backed by the given connection pool.
func NewVault(db *pgxpool.Pool) *CredentialVault {
	return &CredentialVault{db: db}
}

// Store upserts a credential for the given employee and provider.
func (v *CredentialVault) Store(ctx context.Context, companyID, employeeID, provider, token string, accessType string, scopes []string) error {
	id := newID()
	_, err := v.db.Exec(ctx, `
		INSERT INTO access_providers (id, company_id, employee_id, provider_name, access_type, encrypted_token, scopes, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', NOW(), NOW())
		ON CONFLICT (employee_id, provider_name) DO UPDATE SET
			company_id      = EXCLUDED.company_id,
			access_type     = EXCLUDED.access_type,
			encrypted_token = EXCLUDED.encrypted_token,
			scopes          = EXCLUDED.scopes,
			status          = 'active',
			updated_at      = NOW()
	`, id, companyID, employeeID, provider, accessType, token, scopes)
	return err
}

// Get retrieves the token for a given employee+provider.
// Returns ErrNotFound if no active credential exists.
func (v *CredentialVault) Get(ctx context.Context, employeeID, provider string) (string, error) {
	var token string
	err := v.db.QueryRow(ctx,
		`SELECT encrypted_token FROM access_providers WHERE employee_id = $1 AND provider_name = $2 AND status = 'active'`,
		employeeID, provider,
	).Scan(&token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("vault.Get: %w", err)
	}
	return token, nil
}

// Revoke marks a credential as revoked.
func (v *CredentialVault) Revoke(ctx context.Context, employeeID, provider string) error {
	_, err := v.db.Exec(ctx,
		`UPDATE access_providers SET status = 'revoked', updated_at = NOW() WHERE employee_id = $1 AND provider_name = $2`,
		employeeID, provider,
	)
	return err
}

// List returns all access providers for an employee (tokens are not returned).
func (v *CredentialVault) List(ctx context.Context, employeeID string) ([]AccessProvider, error) {
	rows, err := v.db.Query(ctx,
		`SELECT id, COALESCE(company_id,''), employee_id, provider_name, COALESCE(access_type,''), COALESCE(scopes, '{}'), status, created_at
		 FROM access_providers WHERE employee_id = $1 ORDER BY created_at DESC`,
		employeeID,
	)
	if err != nil {
		return nil, fmt.Errorf("vault.List: %w", err)
	}
	defer rows.Close()

	var providers []AccessProvider
	for rows.Next() {
		var p AccessProvider
		if err := rows.Scan(&p.ID, &p.CompanyID, &p.EmployeeID, &p.ProviderName, &p.AccessType, &p.Scopes, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("vault.List scan: %w", err)
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// ListByCompany returns all access providers for a company (tokens are not returned).
func (v *CredentialVault) ListByCompany(ctx context.Context, companyID string) ([]AccessProvider, error) {
	rows, err := v.db.Query(ctx,
		`SELECT id, COALESCE(company_id,''), employee_id, provider_name, COALESCE(access_type,''), COALESCE(scopes, '{}'), status, created_at
		 FROM access_providers WHERE company_id = $1 ORDER BY created_at DESC`,
		companyID,
	)
	if err != nil {
		return nil, fmt.Errorf("vault.ListByCompany: %w", err)
	}
	defer rows.Close()

	var providers []AccessProvider
	for rows.Next() {
		var p AccessProvider
		if err := rows.Scan(&p.ID, &p.CompanyID, &p.EmployeeID, &p.ProviderName, &p.AccessType, &p.Scopes, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("vault.ListByCompany scan: %w", err)
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// RevokeByID revokes a credential by its primary key id.
func (v *CredentialVault) RevokeByID(ctx context.Context, id string) error {
	_, err := v.db.Exec(ctx,
		`UPDATE access_providers SET status = 'revoked', updated_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
