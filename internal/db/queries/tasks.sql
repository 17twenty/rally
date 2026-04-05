-- name: CreateTask :one
INSERT INTO tasks (id, company_id, title, description, assignee_id, status, slack_channel)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, title, status, created_at;

-- name: GetTask :one
SELECT id, company_id, title, COALESCE(description,'') as description, COALESCE(assignee_id,'') as assignee_id, status, COALESCE(slack_thread_ts,'') as slack_thread_ts, COALESCE(slack_channel,'') as slack_channel, created_at
FROM tasks WHERE id = $1;

-- name: UpdateTaskStatus :exec
UPDATE tasks SET status = $2 WHERE id = $1;

-- name: ListTasksByAssignee :many
SELECT id, title, COALESCE(description,'') as description, status
FROM tasks
WHERE assignee_id = $1 AND status NOT IN ('done', 'cancelled')
ORDER BY created_at DESC
LIMIT 10;

-- name: ListActiveTasks :many
SELECT t.id, t.company_id, t.title, COALESCE(t.description,'') as description, COALESCE(t.assignee_id,'') as assignee_id, t.status, t.created_at,
       COALESCE(e.name, e.role, '') as assignee_name,
       COALESCE(c.name, '') as company_name
FROM tasks t
LEFT JOIN employees e ON e.id = t.assignee_id
LEFT JOIN companies c ON c.id = t.company_id
ORDER BY t.created_at DESC
LIMIT 50;
