-- name: InsertSlackEvent :one
INSERT INTO slack_events (id, company_id, event_type, channel, user_id, thread_ts, message_ts, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetUnprocessedSlackEvents :many
SELECT * FROM slack_events WHERE processed_at IS NULL AND company_id = $1 ORDER BY created_at LIMIT $2;

-- name: MarkSlackEventProcessed :exec
UPDATE slack_events SET processed_at = NOW() WHERE id = $1;
