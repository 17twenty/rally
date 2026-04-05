-- Proposed hires: AEs (typically the CEO) propose new team members.
-- Humans approve/reject via the web UI. Approved hires auto-enqueue a HiringJob.
CREATE TABLE IF NOT EXISTS proposed_hires (
    id          TEXT PRIMARY KEY,
    company_id  TEXT NOT NULL,
    proposed_by TEXT NOT NULL,
    role        TEXT NOT NULL,
    department  TEXT,
    rationale   TEXT,
    reports_to  TEXT,
    status      TEXT NOT NULL DEFAULT 'pending',
    reviewed_by TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_proposed_hires_company ON proposed_hires(company_id, status);
