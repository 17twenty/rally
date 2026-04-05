package vault

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditEntry represents a credential access audit log entry.
type AuditEntry struct {
	ID           string
	EmployeeID   string
	ProviderName string
	Action       string
	IPAddress    string
	CreatedAt    time.Time
}

// LogAccess inserts an entry into credential_audit_log.
func LogAccess(ctx context.Context, db *pgxpool.Pool, employeeID, provider, action string) error {
	id := newID()
	_, err := db.Exec(ctx,
		`INSERT INTO credential_audit_log (id, employee_id, provider_name, action, created_at) VALUES ($1, $2, $3, $4, NOW())`,
		id, employeeID, provider, action,
	)
	if err != nil {
		return fmt.Errorf("vault.LogAccess: %w", err)
	}
	return nil
}

// GetAuditLog retrieves recent audit log entries, optionally filtered by employeeID.
func GetAuditLog(ctx context.Context, db *pgxpool.Pool, employeeID string, limit int) ([]AuditEntry, error) {
	var (
		query string
		args  []any
	)
	if employeeID != "" {
		query = `SELECT id, COALESCE(employee_id,''), COALESCE(provider_name,''), COALESCE(action,''), COALESCE(ip_address,''), created_at
		          FROM credential_audit_log WHERE employee_id = $1 ORDER BY created_at DESC LIMIT $2`
		args = []any{employeeID, limit}
	} else {
		query = `SELECT id, COALESCE(employee_id,''), COALESCE(provider_name,''), COALESCE(action,''), COALESCE(ip_address,''), created_at
		          FROM credential_audit_log ORDER BY created_at DESC LIMIT $1`
		args = []any{limit}
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vault.GetAuditLog: %w", err)
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if scanErr := rows.Scan(&e.ID, &e.EmployeeID, &e.ProviderName, &e.Action, &e.IPAddress, &e.CreatedAt); scanErr != nil {
			return nil, fmt.Errorf("vault.GetAuditLog scan: %w", scanErr)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
