-- name: GetCompany :one
SELECT * FROM companies WHERE id = $1;

-- name: ListCompanies :many
SELECT * FROM companies ORDER BY created_at DESC;

-- name: InsertCompany :one
INSERT INTO companies (id, name, mission, status, created_at, policy_doc)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateCompanyStatus :exec
UPDATE companies SET status = $2 WHERE id = $1;

-- name: UpdateCompanyPolicy :exec
UPDATE companies SET policy_doc = $2 WHERE id = $1;
