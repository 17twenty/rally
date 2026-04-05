-- name: GetCompanyFinancials :one
SELECT * FROM company_financials WHERE company_id = $1;

-- name: UpsertCompanyFinancials :exec
INSERT INTO company_financials
  (id, company_id, bank_name, account_name, bsb, account_number,
   swift_code, payment_provider, invoice_prefix, currency)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (company_id) DO UPDATE SET
  bank_name = $3, account_name = $4, bsb = $5, account_number = $6,
  swift_code = $7, payment_provider = $8, invoice_prefix = $9, currency = $10,
  updated_at = NOW();
