CREATE TABLE IF NOT EXISTS companies (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    mission    TEXT,
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS employees (
    id            TEXT PRIMARY KEY,
    company_id    TEXT NOT NULL,
    role          TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('ae', 'human')),
    status        TEXT NOT NULL,
    slack_user_id TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS employee_configs (
    id          TEXT PRIMARY KEY,
    employee_id TEXT NOT NULL REFERENCES employees(id),
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS org_structure (
    id          TEXT PRIMARY KEY,
    company_id  TEXT NOT NULL,
    employee_id TEXT NOT NULL,
    reports_to  TEXT,
    department  TEXT
);

CREATE TABLE IF NOT EXISTS memory_events (
    id          TEXT PRIMARY KEY,
    employee_id TEXT NOT NULL,
    type        TEXT NOT NULL,
    content     TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledgebase (
    id          TEXT PRIMARY KEY,
    company_id  TEXT NOT NULL,
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    tags        TEXT[],
    status      TEXT NOT NULL DEFAULT 'active',
    approved_by TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tasks (
    id               TEXT PRIMARY KEY,
    company_id       TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT,
    assignee_id      TEXT,
    status           TEXT NOT NULL,
    slack_thread_ts  TEXT,
    slack_channel    TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS slack_events (
    id           TEXT PRIMARY KEY,
    company_id   TEXT NOT NULL,
    event_type   TEXT NOT NULL,
    channel      TEXT,
    user_id      TEXT,
    thread_ts    TEXT,
    message_ts   TEXT,
    payload      JSONB NOT NULL DEFAULT '{}',
    processed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tool_logs (
    id          TEXT PRIMARY KEY,
    employee_id TEXT NOT NULL,
    tool        TEXT NOT NULL,
    action      TEXT NOT NULL,
    input       JSONB NOT NULL DEFAULT '{}',
    output      JSONB NOT NULL DEFAULT '{}',
    success     BOOL NOT NULL,
    trace_id    TEXT,
    task_id     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS provider_registry (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    api_style   TEXT NOT NULL CHECK (api_style IN ('openai', 'anthropic')),
    base_url    TEXT NOT NULL,
    api_key_env TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_registry (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    provider_id    TEXT NOT NULL,
    context_window INT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
