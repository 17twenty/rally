-- name: GetActiveProviderToken :one
SELECT encrypted_token FROM access_providers
WHERE company_id = $1 AND provider_name = $2 AND status = 'active'
LIMIT 1;
