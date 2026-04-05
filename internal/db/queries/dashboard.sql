-- name: CountTasks :one
SELECT COUNT(*) FROM tasks;

-- name: CountActiveKBEntries :one
SELECT COUNT(*) FROM knowledgebase WHERE status = 'active';

-- name: ListAEsWithLastActive :many
SELECT e.id, e.company_id, COALESCE(e.name,'') as name, e.role, COALESCE(e.specialties,'') as specialties,
       e.type, e.status, COALESCE(e.slack_user_id,'') as slack_user_id, e.created_at,
       MAX(tl.created_at) as last_active
FROM employees e
LEFT JOIN tool_logs tl ON tl.employee_id = e.id
WHERE e.type = 'ae'
GROUP BY e.id, e.company_id, e.name, e.role, e.specialties, e.type, e.status, e.slack_user_id, e.created_at
ORDER BY e.created_at DESC;

-- name: ListRecentToolLogsWithEmployee :many
SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id) as employee_name,
       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,'') as trace_id,
       COALESCE(tl.task_id,'') as task_id, tl.created_at
FROM tool_logs tl
LEFT JOIN employees e ON e.id = tl.employee_id
ORDER BY tl.created_at DESC
LIMIT $1;

-- name: ListAllEmployeesWithLastActive :many
SELECT e.id, e.company_id, COALESCE(e.name,'') as name, e.role, COALESCE(e.specialties,'') as specialties,
       e.type, e.status, COALESCE(e.slack_user_id,'') as slack_user_id, e.created_at,
       MAX(tl.created_at) as last_active
FROM employees e
LEFT JOIN tool_logs tl ON tl.employee_id = e.id
GROUP BY e.id, e.company_id, e.name, e.role, e.specialties, e.type, e.status, e.slack_user_id, e.created_at
ORDER BY e.type, e.created_at DESC;

-- name: ListActiveTeamWorkItems :many
SELECT COALESCE(e.name, e.role) as owner_name, e.role as owner_role, wi.title, wi.status, wi.priority, wi.updated_at
FROM work_items wi
JOIN employees e ON e.id = wi.owner_id
WHERE wi.status IN ('in_progress', 'todo', 'blocked')
ORDER BY CASE wi.status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END, wi.updated_at DESC
LIMIT 30;

-- name: ListWorkItemsByOwnerNotCancelled :many
SELECT id, title, status, priority, updated_at
FROM work_items
WHERE owner_id = $1 AND status NOT IN ('cancelled')
ORDER BY CASE status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 WHEN 'todo' THEN 2 ELSE 3 END,
         CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
         updated_at DESC
LIMIT 20;

-- name: ListToolLogsWithEmployeeByEmployee :many
SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id) as employee_name,
       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,'') as trace_id,
       COALESCE(tl.task_id,'') as task_id, tl.created_at
FROM tool_logs tl
LEFT JOIN employees e ON e.id = tl.employee_id
WHERE tl.employee_id = $1
ORDER BY tl.created_at DESC
LIMIT 200;

-- name: ListToolLogsWithEmployeeByTool :many
SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id) as employee_name,
       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,'') as trace_id,
       COALESCE(tl.task_id,'') as task_id, tl.created_at
FROM tool_logs tl
LEFT JOIN employees e ON e.id = tl.employee_id
WHERE tl.tool ILIKE $1
ORDER BY tl.created_at DESC
LIMIT 200;

-- name: ListToolLogsWithEmployeeByEmployeeAndTool :many
SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id) as employee_name,
       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,'') as trace_id,
       COALESCE(tl.task_id,'') as task_id, tl.created_at
FROM tool_logs tl
LEFT JOIN employees e ON e.id = tl.employee_id
WHERE tl.employee_id = $1 AND tl.tool ILIKE $2
ORDER BY tl.created_at DESC
LIMIT 200;

-- name: ListToolLogsWithEmployeeAll :many
SELECT tl.id, tl.employee_id, COALESCE(e.name, e.role, tl.employee_id) as employee_name,
       tl.tool, tl.action, tl.success, COALESCE(tl.trace_id,'') as trace_id,
       COALESCE(tl.task_id,'') as task_id, tl.created_at
FROM tool_logs tl
LEFT JOIN employees e ON e.id = tl.employee_id
ORDER BY tl.created_at DESC
LIMIT 200;
