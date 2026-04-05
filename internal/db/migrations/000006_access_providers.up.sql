CREATE TABLE IF NOT EXISTS access_providers (
    id           TEXT PRIMARY KEY,
    company_id   TEXT,
    employee_id  TEXT,
    provider_name TEXT NOT NULL,
    access_type  TEXT CHECK (access_type IN ('api', 'oauth', 'browser_session')),
    encrypted_token TEXT,
    scopes       TEXT[] DEFAULT '{}',
    status       TEXT DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    metadata     JSONB DEFAULT '{}',
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (employee_id, provider_name)
);

CREATE TABLE IF NOT EXISTS credential_audit_log (
    id           TEXT PRIMARY KEY,
    employee_id  TEXT,
    provider_name TEXT,
    action       TEXT,
    ip_address   TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
