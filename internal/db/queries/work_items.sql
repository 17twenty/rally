-- name: CreateWorkItem :one
INSERT INTO work_items (id, parent_id, company_id, owner_id, title, description, status, priority, source_task_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, title, status, priority, created_at;

-- name: GetWorkItem :one
SELECT id, COALESCE(parent_id,'') as parent_id, company_id, owner_id, title, COALESCE(description,'') as description, status, priority, COALESCE(source_task_id,'') as source_task_id, created_at, updated_at
FROM work_items WHERE id = $1;

-- name: ListWorkItemsByOwner :many
SELECT id, COALESCE(parent_id,'') as parent_id, title, COALESCE(description,'') as description, status, priority, updated_at
FROM work_items
WHERE owner_id = $1 AND status NOT IN ('done', 'cancelled')
ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at ASC
LIMIT 20;

-- name: ListWorkItemsByCompany :many
SELECT wi.id, COALESCE(e.name, e.role) as owner_name, e.role as owner_role, wi.title, wi.status, wi.priority, wi.updated_at
FROM work_items wi
JOIN employees e ON e.id = wi.owner_id
WHERE wi.company_id = $1 AND wi.status IN ('in_progress', 'todo', 'blocked')
ORDER BY CASE wi.status WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END, wi.updated_at DESC
LIMIT 30;

-- name: UpdateWorkItemStatus :exec
UPDATE work_items SET status = $2, updated_at = now() WHERE id = $1 AND owner_id = $3;

-- name: TouchWorkItem :exec
UPDATE work_items SET updated_at = now() WHERE id = $1;

-- name: AddWorkItemHistory :exec
INSERT INTO work_item_history (id, work_item_id, change_type, content, metadata)
VALUES ($1, $2, $3, $4, $5);

-- name: CheckDuplicateWorkItem :many
SELECT id, title, status FROM work_items
WHERE owner_id = $1 AND company_id = $2 AND title = $3 AND status NOT IN ('done', 'cancelled')
LIMIT 1;

-- name: CompleteWorkItemsBySourceTask :exec
UPDATE work_items SET status = 'done', updated_at = now()
WHERE source_task_id = $1 AND status != 'done';
