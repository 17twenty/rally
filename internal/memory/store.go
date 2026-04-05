package memory

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/17twenty/rally/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newID generates a random UUID v4 string.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// MemoryStore persists and retrieves episodic memory events for AEs.
type MemoryStore struct {
	DB *pgxpool.Pool
}

// NewMemoryStore creates a MemoryStore backed by the given pool.
func NewMemoryStore(db *pgxpool.Pool) *MemoryStore {
	return &MemoryStore{DB: db}
}

// Save inserts a MemoryEvent into the memory_events table.
// The event's ID must be set by the caller (use a UUID).
func (s *MemoryStore) Save(ctx context.Context, event domain.MemoryEvent) error {
	metaJSON, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("memory: marshal metadata: %w", err)
	}

	const q = `
		INSERT INTO memory_events (id, employee_id, type, content, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err = s.DB.Exec(ctx, q,
		event.ID,
		event.EmployeeID,
		event.Type,
		event.Content,
		metaJSON,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("memory: save event: %w", err)
	}
	return nil
}

// GetRecent returns the most recent N memory events for an employee, newest first.
func (s *MemoryStore) GetRecent(ctx context.Context, employeeID string, limit int) ([]domain.MemoryEvent, error) {
	const q = `
		SELECT id, employee_id, type, content, metadata, created_at
		FROM memory_events
		WHERE employee_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	return s.query(ctx, q, employeeID, limit)
}

// GetByType returns up to limit events for an employee filtered by type (episodic|reflection|heuristic).
func (s *MemoryStore) GetByType(ctx context.Context, employeeID, memType string, limit int) ([]domain.MemoryEvent, error) {
	const q = `
		SELECT id, employee_id, type, content, metadata, created_at
		FROM memory_events
		WHERE employee_id = $1 AND type = $2
		ORDER BY created_at DESC
		LIMIT $3
	`
	return s.query(ctx, q, employeeID, memType, limit)
}

// SaveEpisodic is a convenience wrapper to save an episodic memory event.
func (s *MemoryStore) SaveEpisodic(ctx context.Context, employeeID, content string, metadata map[string]any) error {
	return s.Save(ctx, domain.MemoryEvent{
		ID:         newID(),
		EmployeeID: employeeID,
		Type:       "episodic",
		Content:    content,
		Metadata:   metadata,
		CreatedAt:  time.Now().UTC(),
	})
}

// SaveReflection saves a reflection-type memory event.
func (s *MemoryStore) SaveReflection(ctx context.Context, employeeID, content string) error {
	return s.Save(ctx, domain.MemoryEvent{
		ID:         newID(),
		EmployeeID: employeeID,
		Type:       "reflection",
		Content:    content,
		Metadata:   map[string]any{},
		CreatedAt:  time.Now().UTC(),
	})
}

// SaveHeuristic saves a heuristic-type memory event.
func (s *MemoryStore) SaveHeuristic(ctx context.Context, employeeID, heuristic string) error {
	return s.Save(ctx, domain.MemoryEvent{
		ID:         newID(),
		EmployeeID: employeeID,
		Type:       "heuristic",
		Content:    heuristic,
		Metadata:   map[string]any{},
		CreatedAt:  time.Now().UTC(),
	})
}

// query executes a SELECT and scans rows into MemoryEvent slice.
func (s *MemoryStore) query(ctx context.Context, sql string, args ...any) ([]domain.MemoryEvent, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: query: %w", err)
	}
	defer rows.Close()

	var events []domain.MemoryEvent
	for rows.Next() {
		var e domain.MemoryEvent
		var metaRaw []byte
		if err := rows.Scan(&e.ID, &e.EmployeeID, &e.Type, &e.Content, &metaRaw, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("memory: scan: %w", err)
		}
		if len(metaRaw) > 0 {
			if err := json.Unmarshal(metaRaw, &e.Metadata); err != nil {
				return nil, fmt.Errorf("memory: unmarshal metadata: %w", err)
			}
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: rows: %w", err)
	}
	return events, nil
}
