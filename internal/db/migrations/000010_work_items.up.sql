-- Work items: persistent backlog for AE agents.
-- Each AE tracks their own work items across cycles.
CREATE TABLE IF NOT EXISTS work_items (
    id             TEXT PRIMARY KEY,
    parent_id      TEXT REFERENCES work_items(id),
    company_id     TEXT NOT NULL,
    owner_id       TEXT NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT,
    status         TEXT NOT NULL DEFAULT 'todo',
    priority       TEXT NOT NULL DEFAULT 'medium',
    source_task_id TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_work_items_owner ON work_items(owner_id, status);
CREATE INDEX IF NOT EXISTS idx_work_items_company ON work_items(company_id, status);

-- Audit trail for work item changes.
CREATE TABLE IF NOT EXISTS work_item_history (
    id             TEXT PRIMARY KEY,
    work_item_id   TEXT NOT NULL REFERENCES work_items(id),
    change_type    TEXT NOT NULL,
    content        TEXT,
    metadata       JSONB DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_work_item_history_item ON work_item_history(work_item_id);

-- Inter-AE messages for lightweight collaboration.
CREATE TABLE IF NOT EXISTS ae_messages (
    id             TEXT PRIMARY KEY,
    company_id     TEXT NOT NULL,
    from_id        TEXT NOT NULL,
    to_id          TEXT NOT NULL,
    message        TEXT NOT NULL,
    read           BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ae_messages_to ON ae_messages(to_id, read);
