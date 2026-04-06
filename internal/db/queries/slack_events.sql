-- name: InsertSlackEvent :one
INSERT INTO slack_events (id, company_id, event_type, channel, user_id, thread_ts, message_ts, payload, text)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetUnprocessedSlackEvents :many
SELECT * FROM slack_events WHERE processed_at IS NULL AND company_id = $1 ORDER BY created_at LIMIT $2;

-- name: GetRecentSlackEvents :many
SELECT * FROM slack_events WHERE company_id = $1 AND created_at > now() - interval '10 minutes' ORDER BY created_at DESC LIMIT $2;

-- name: GetRecentSlackMessagesExcludingBot :many
SELECT id, event_type, COALESCE(channel,'') as channel, COALESCE(user_id,'') as user_id,
       COALESCE(text,'') as text, COALESCE(thread_ts,'') as thread_ts, COALESCE(message_ts,'') as message_ts
FROM slack_events
WHERE company_id = $1
  AND created_at > now() - interval '5 minutes'
  AND event_type IN ('message', 'app_mention')
  AND (user_id IS NULL OR user_id != $2)
ORDER BY created_at ASC
LIMIT $3;

-- name: MarkSlackEventProcessed :exec
UPDATE slack_events SET processed_at = NOW() WHERE id = $1;
