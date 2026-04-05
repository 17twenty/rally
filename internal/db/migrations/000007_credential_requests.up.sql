CREATE TABLE IF NOT EXISTS credential_requests (
    id TEXT PRIMARY KEY,
    employee_id TEXT NOT NULL,
    company_id TEXT NOT NULL,
    provider_name TEXT NOT NULL,
    reason TEXT,
    status TEXT DEFAULT 'pending' CHECK (status IN ('pending','fulfilled','rejected')),
    requested_at TIMESTAMPTZ DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);
