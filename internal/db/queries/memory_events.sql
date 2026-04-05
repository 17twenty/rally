-- name: InsertMemoryEvent :one
INSERT INTO memory_events (id, employee_id, type, content, metadata, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetRecentMemoryEvents :many
SELECT * FROM memory_events WHERE employee_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: GetMemoryEventsByType :many
SELECT * FROM memory_events WHERE employee_id = $1 AND type = $2 ORDER BY created_at DESC LIMIT $3;
