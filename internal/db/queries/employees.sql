-- name: GetEmployee :one
SELECT * FROM employees WHERE id = $1;

-- name: ListEmployeesByCompany :many
SELECT * FROM employees WHERE company_id = $1 ORDER BY created_at;

-- name: InsertEmployee :one
INSERT INTO employees (id, company_id, role, type, status, slack_user_id, name, specialties, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateEmployeeStatus :exec
UPDATE employees SET status = $2 WHERE id = $1;

-- name: ListHumanEmployeesByCompany :many
SELECT * FROM employees WHERE company_id=$1 AND type='human' ORDER BY created_at;
