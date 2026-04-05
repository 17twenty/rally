ALTER TABLE employees ADD COLUMN IF NOT EXISTS container_id TEXT;
ALTER TABLE employees ADD COLUMN IF NOT EXISTS container_status TEXT DEFAULT 'none';

CREATE TABLE IF NOT EXISTS ae_api_tokens (
    id          TEXT PRIMARY KEY,
    employee_id TEXT NOT NULL,
    company_id  TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ae_api_tokens_hash ON ae_api_tokens(token_hash);
