-- name: InsertToolLog :one
INSERT INTO tool_logs (id, employee_id, company_id, tool, action, input, output, success, trace_id, task_id, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetToolLogsByEmployee :many
SELECT * FROM tool_logs WHERE employee_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: GetRecentToolLogs :many
SELECT * FROM tool_logs WHERE company_id IS NOT NULL ORDER BY created_at DESC LIMIT $1;
