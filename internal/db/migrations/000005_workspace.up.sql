CREATE TABLE workspace_files (
    id          TEXT PRIMARY KEY,
    company_id  TEXT,
    path        TEXT NOT NULL,
    title       TEXT,
    content     TEXT,
    mime_type   TEXT DEFAULT 'text/plain',
    version     INT DEFAULT 1,
    status      TEXT DEFAULT 'pending' CHECK (status IN ('pending','active','archived')),
    metadata    JSONB DEFAULT '{}',
    created_by  TEXT,
    approved_by TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE workspace_comments (
    id         TEXT PRIMARY KEY,
    file_id    TEXT REFERENCES workspace_files(id),
    author_id  TEXT,
    body       TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE workspace_versions (
    id         TEXT PRIMARY KEY,
    file_id    TEXT REFERENCES workspace_files(id),
    version    INT,
    content    TEXT,
    created_by TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
