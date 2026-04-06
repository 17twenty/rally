-- name: InsertProposedHire :one
INSERT INTO proposed_hires (id, company_id, proposed_by, role, department, rationale, reports_to, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
RETURNING *;

-- name: ListProposedHiresByCompany :many
SELECT ph.*, COALESCE(e.name, e.role, ph.proposed_by) as proposer_name
FROM proposed_hires ph
LEFT JOIN employees e ON e.id = ph.proposed_by
WHERE ph.company_id = $1
ORDER BY ph.created_at DESC;

-- name: ListPendingHiresByCompany :many
SELECT ph.*, COALESCE(e.name, e.role, ph.proposed_by) as proposer_name
FROM proposed_hires ph
LEFT JOIN employees e ON e.id = ph.proposed_by
WHERE ph.company_id = $1 AND ph.status = 'pending'
ORDER BY ph.created_at ASC;

-- name: GetProposedHire :one
SELECT * FROM proposed_hires WHERE id = $1;

-- name: ApproveProposedHire :exec
UPDATE proposed_hires SET status = 'approved', reviewed_by = $2 WHERE id = $1;

-- name: RejectProposedHire :exec
UPDATE proposed_hires SET status = 'rejected', reviewed_by = $2 WHERE id = $1;

-- name: MarkProposedHireComplete :exec
UPDATE proposed_hires SET status = 'hired' WHERE company_id = $1 AND role = $2 AND status = 'approved';
