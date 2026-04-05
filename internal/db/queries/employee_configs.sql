-- name: GetEmployeeConfig :one
SELECT id, employee_id, config, created_at
FROM employee_configs WHERE employee_id = $1
ORDER BY created_at DESC LIMIT 1;

-- name: InsertEmployeeConfig :exec
INSERT INTO employee_configs (id, employee_id, config)
VALUES ($1, $2, $3);
