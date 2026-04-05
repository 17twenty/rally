-- name: InsertAEMessage :one
INSERT INTO ae_messages (id, company_id, from_id, to_id, message)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at;

-- name: ListUnreadMessages :many
SELECT id, from_id, message, created_at
FROM ae_messages
WHERE to_id = $1 AND read = false
ORDER BY created_at ASC
LIMIT 10;

-- name: MarkMessagesAsRead :exec
UPDATE ae_messages SET read = true WHERE to_id = $1 AND read = false;
