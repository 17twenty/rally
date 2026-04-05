-- name: InsertKBEntry :one
INSERT INTO knowledgebase (id, company_id, title, content, tags, status, approved_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ApproveKBEntry :exec
UPDATE knowledgebase SET status = 'active', approved_by = $2 WHERE id = $1;

-- name: GetAllKBEntries :many
SELECT * FROM knowledgebase WHERE company_id = $1 AND status = 'active' ORDER BY created_at DESC;

-- name: SearchKBEntries :many
SELECT * FROM knowledgebase WHERE company_id = $1 AND (title ILIKE $2 OR content ILIKE $2);
