package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WorkspaceFile represents a versioned artifact stored in the workspace.
type WorkspaceFile struct {
	ID         string
	CompanyID  string
	Path       string
	Title      string
	Content    string
	MimeType   string
	Version    int
	Status     string
	CreatedBy  string
	ApprovedBy string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WorkspaceComment is a comment thread entry on a workspace file.
type WorkspaceComment struct {
	ID        string
	FileID    string
	AuthorID  string
	Body      string
	CreatedAt time.Time
}

// WorkspaceVersion is a historical snapshot of a workspace file.
type WorkspaceVersion struct {
	ID        string
	FileID    string
	Version   int
	Content   string
	CreatedBy string
	CreatedAt time.Time
}

// WorkspaceStore provides CRUD access to workspace tables.
type WorkspaceStore struct {
	DB *pgxpool.Pool
}

// SaveFile inserts a new file or upserts an existing one (matched by company_id+path),
// bumping the version and archiving the previous content as a workspace_version row.
func (s *WorkspaceStore) SaveFile(ctx context.Context, file WorkspaceFile) error {
	// Check if file exists at this path for the company.
	var existingID string
	var existingVersion int
	var existingContent string
	err := s.DB.QueryRow(ctx,
		`SELECT id, version, content FROM workspace_files WHERE company_id=$1 AND path=$2`,
		file.CompanyID, file.Path,
	).Scan(&existingID, &existingVersion, &existingContent)

	if err == nil {
		// File exists — archive current version then update.
		_, err = s.DB.Exec(ctx,
			`INSERT INTO workspace_versions (id, file_id, version, content, created_by, created_at)
			 VALUES ($1, $2, $3, $4, $5, NOW())`,
			newID(), existingID, existingVersion, existingContent, file.CreatedBy,
		)
		if err != nil {
			return err
		}
		_, err = s.DB.Exec(ctx,
			`UPDATE workspace_files
			 SET title=$1, content=$2, mime_type=$3, version=version+1,
			     status='pending', created_by=$4, approved_by=NULL, updated_at=NOW()
			 WHERE id=$5`,
			file.Title, file.Content, file.MimeType, file.CreatedBy, existingID,
		)
		return err
	}

	// New file — insert.
	_, err = s.DB.Exec(ctx,
		`INSERT INTO workspace_files
		 (id, company_id, path, title, content, mime_type, version, status, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 1, 'pending', $7, NOW(), NOW())`,
		file.ID, file.CompanyID, file.Path, file.Title, file.Content, file.MimeType, file.CreatedBy,
	)
	return err
}

// GetFile returns a single workspace file by ID.
func (s *WorkspaceStore) GetFile(ctx context.Context, fileID string) (*WorkspaceFile, error) {
	var f WorkspaceFile
	err := s.DB.QueryRow(ctx,
		`SELECT id, company_id, path, title, content, mime_type, version, status,
		        COALESCE(created_by,''), COALESCE(approved_by,''), created_at, updated_at
		 FROM workspace_files WHERE id=$1`,
		fileID,
	).Scan(&f.ID, &f.CompanyID, &f.Path, &f.Title, &f.Content, &f.MimeType,
		&f.Version, &f.Status, &f.CreatedBy, &f.ApprovedBy, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFiles returns all files for a company, optionally filtered by path prefix.
func (s *WorkspaceStore) ListFiles(ctx context.Context, companyID string, pathPrefix string) ([]WorkspaceFile, error) {
	query := `SELECT id, company_id, path, title, content, mime_type, version, status,
	                 COALESCE(created_by,''), COALESCE(approved_by,''), created_at, updated_at
	          FROM workspace_files WHERE company_id=$1`
	args := []any{companyID}
	if pathPrefix != "" {
		query += ` AND path LIKE $2`
		args = append(args, pathPrefix+"%")
	}
	query += ` ORDER BY path ASC`

	rows, err := s.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []WorkspaceFile
	for rows.Next() {
		var f WorkspaceFile
		if err := rows.Scan(&f.ID, &f.CompanyID, &f.Path, &f.Title, &f.Content, &f.MimeType,
			&f.Version, &f.Status, &f.CreatedBy, &f.ApprovedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// ApproveFile sets a file's status to 'active' and records who approved it.
func (s *WorkspaceStore) ApproveFile(ctx context.Context, fileID, approvedBy string) error {
	_, err := s.DB.Exec(ctx,
		`UPDATE workspace_files SET status='active', approved_by=$1, updated_at=NOW() WHERE id=$2`,
		approvedBy, fileID,
	)
	return err
}

// AddComment adds a comment to a workspace file.
func (s *WorkspaceStore) AddComment(ctx context.Context, comment WorkspaceComment) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO workspace_comments (id, file_id, author_id, body, created_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		comment.ID, comment.FileID, comment.AuthorID, comment.Body,
	)
	return err
}

// GetComments returns all comments for a file, oldest first.
func (s *WorkspaceStore) GetComments(ctx context.Context, fileID string) ([]WorkspaceComment, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, file_id, COALESCE(author_id,''), body, created_at
		 FROM workspace_comments WHERE file_id=$1 ORDER BY created_at ASC`,
		fileID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []WorkspaceComment
	for rows.Next() {
		var c WorkspaceComment
		if err := rows.Scan(&c.ID, &c.FileID, &c.AuthorID, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// GetVersions returns version history for a file, newest first.
func (s *WorkspaceStore) GetVersions(ctx context.Context, fileID string) ([]WorkspaceVersion, error) {
	rows, err := s.DB.Query(ctx,
		`SELECT id, file_id, version, content, COALESCE(created_by,''), created_at
		 FROM workspace_versions WHERE file_id=$1 ORDER BY version DESC`,
		fileID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []WorkspaceVersion
	for rows.Next() {
		var v WorkspaceVersion
		if err := rows.Scan(&v.ID, &v.FileID, &v.Version, &v.Content, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// SearchFiles searches for files matching query via ILIKE on title and content.
func (s *WorkspaceStore) SearchFiles(ctx context.Context, companyID, query string) ([]WorkspaceFile, error) {
	like := "%" + query + "%"
	rows, err := s.DB.Query(ctx,
		`SELECT id, company_id, path, title, content, mime_type, version, status,
		        COALESCE(created_by,''), COALESCE(approved_by,''), created_at, updated_at
		 FROM workspace_files
		 WHERE company_id=$1 AND (title ILIKE $2 OR content ILIKE $2)
		 ORDER BY updated_at DESC`,
		companyID, like,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []WorkspaceFile
	for rows.Next() {
		var f WorkspaceFile
		if err := rows.Scan(&f.ID, &f.CompanyID, &f.Path, &f.Title, &f.Content, &f.MimeType,
			&f.Version, &f.Status, &f.CreatedBy, &f.ApprovedBy, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
