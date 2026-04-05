package kb

import (
	"context"

	"github.com/17twenty/rally/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// KBStore provides CRUD access to the knowledgebase table.
type KBStore struct {
	DB *pgxpool.Pool
}

// Save inserts a new KB entry. Sets status='pending' if no ApprovedBy is set.
func (s *KBStore) Save(ctx context.Context, entry domain.KnowledgebaseEntry) error {
	status := entry.Status
	if status == "" || entry.ApprovedBy == "" {
		status = "pending"
	}
	_, err := s.DB.Exec(ctx,
		`INSERT INTO knowledgebase (id, company_id, title, content, tags, status, approved_by)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))`,
		entry.ID, entry.CompanyID, entry.Title, entry.Content, entry.Tags, status, entry.ApprovedBy,
	)
	return err
}

// Approve sets an entry's status to 'active' and records who approved it.
func (s *KBStore) Approve(ctx context.Context, entryID, approvedBy string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE knowledgebase SET status='active', approved_by=$1 WHERE id=$2`,
		approvedBy, entryID,
	)
	return err
}

// GetByTags returns active entries for a company whose tags overlap with the given list.
func (s *KBStore) GetByTags(ctx context.Context, companyID string, tags []string) ([]domain.KnowledgebaseEntry, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, company_id, title, content, COALESCE(tags, '{}'), status, COALESCE(approved_by,''), created_at
		 FROM knowledgebase
		 WHERE company_id=$1 AND status='active' AND tags && $2::text[]
		 ORDER BY created_at DESC`,
		companyID, tags,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

// Search returns active entries matching query via ILIKE on title or content.
func (s *KBStore) Search(ctx context.Context, companyID, query string) ([]domain.KnowledgebaseEntry, error) {
	like := "%" + query + "%"
	rows, err := s.DB.Query(ctx,
		`SELECT id, company_id, title, content, COALESCE(tags, '{}'), status, COALESCE(approved_by,''), created_at
		 FROM knowledgebase
		 WHERE company_id=$1 AND status='active' AND (title ILIKE $2 OR content ILIKE $2)
		 ORDER BY created_at DESC`,
		companyID, like,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

// GetAll returns all active entries for a company.
func (s *KBStore) GetAll(ctx context.Context, companyID string) ([]domain.KnowledgebaseEntry, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, company_id, title, content, COALESCE(tags, '{}'), status, COALESCE(approved_by,''), created_at
		 FROM knowledgebase
		 WHERE company_id=$1 AND status='active'
		 ORDER BY created_at DESC`,
		companyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

// ProposeUpdate saves an entry with status='pending', clearing any prior approval.
func (s *KBStore) ProposeUpdate(ctx context.Context, entry domain.KnowledgebaseEntry, proposedBy string) error {
	entry.Status = "pending"
	entry.ApprovedBy = ""
	return s.Save(ctx, entry)
}

func scanEntries(rows pgx.Rows) ([]domain.KnowledgebaseEntry, error) {
	var entries []domain.KnowledgebaseEntry
	for rows.Next() {
		var e domain.KnowledgebaseEntry
		if err := rows.Scan(
			&e.ID, &e.CompanyID, &e.Title, &e.Content, &e.Tags,
			&e.Status, &e.ApprovedBy, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
