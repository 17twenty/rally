-- name: GetCompany :one
SELECT * FROM companies WHERE id = $1;

-- name: ListCompanies :many
SELECT * FROM companies ORDER BY created_at DESC;

-- name: InsertCompany :one
INSERT INTO companies (id, name, mission, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateCompanyStatus :exec
UPDATE companies SET status = $2 WHERE id = $1;

-- name: UpdateCompanyPolicy :exec
UPDATE companies SET policy_doc = $2 WHERE id = $1;

-- name: CountCompanies :one
SELECT COUNT(*) FROM companies;

-- name: GetCompanyPolicy :one
SELECT COALESCE(policy_doc, '') as policy_doc FROM companies WHERE id = $1;

-- name: UpdateSlackTeam :exec
UPDATE companies SET slack_team_id = $2, slack_team_name = $3, slack_bot_user_id = $4 WHERE id = $1;

-- name: ListCompaniesByName :many
SELECT * FROM companies ORDER BY name;

-- name: GetFirstActiveCompany :one
SELECT * FROM companies WHERE status = 'active' ORDER BY created_at LIMIT 1;
