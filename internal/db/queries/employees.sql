-- name: GetEmployee :one
SELECT * FROM employees WHERE id = $1;

-- name: ListEmployeesByCompany :many
SELECT * FROM employees WHERE company_id = $1 ORDER BY created_at;

-- name: InsertEmployee :one
INSERT INTO employees (id, company_id, role, type, status, slack_user_id, name, specialties)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateEmployeeStatus :exec
UPDATE employees SET status = $2 WHERE id = $1;

-- name: ListHumanEmployeesByCompany :many
SELECT * FROM employees WHERE company_id=$1 AND type='human' ORDER BY created_at;

-- name: GetEmployeeByRole :one
SELECT * FROM employees WHERE company_id = $1 AND role = $2 AND type = 'ae' LIMIT 1;

-- name: UpdateEmployeeContainerStatus :exec
UPDATE employees SET container_status = $2 WHERE id = $1;

-- name: ListAEsByCompany :many
SELECT * FROM employees WHERE company_id = $1 AND type = 'ae' ORDER BY created_at;

-- name: CountAEs :one
SELECT COUNT(*) FROM employees WHERE type = 'ae';

-- name: ListAllEmployees :many
SELECT * FROM employees ORDER BY type, role;

-- name: ListAllEmployeesByCreatedAt :many
SELECT * FROM employees ORDER BY created_at ASC;

-- name: GetEmployeeContainerID :one
SELECT COALESCE(container_id,'') as container_id FROM employees WHERE id = $1;

-- name: ListActiveAEsByCompany :many
SELECT * FROM employees WHERE company_id = $1 AND type = 'ae' AND status = 'active' ORDER BY created_at;

-- name: UpsertHumanEmployee :exec
INSERT INTO employees (id, company_id, name, role, type, status, slack_user_id)
VALUES ($1, $2, $3, 'Human', 'human', 'active', $4)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, status = 'active';

-- name: GetAEWithConfig :one
SELECT e.id, COALESCE(e.name, e.role) as name, e.role, ec.config
FROM employees e
JOIN employee_configs ec ON ec.employee_id = e.id
WHERE e.company_id = $1 AND e.type = 'ae' AND e.status = 'active'
ORDER BY CASE WHEN LOWER(e.role) LIKE '%ceo%' THEN 0 ELSE 1 END, e.created_at
LIMIT 1;
