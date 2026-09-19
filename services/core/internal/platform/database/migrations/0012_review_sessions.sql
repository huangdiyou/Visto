CREATE TABLE review_sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id),
    collection_id TEXT REFERENCES collections(id),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('draft', 'open', 'changes_requested', 'approved', 'closed')
    ),
    due_at TEXT,
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT
);

CREATE TABLE review_items (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES assets(id),
    asset_version_id TEXT NOT NULL REFERENCES asset_versions(id),
    position INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'in_review', 'approved', 'changes_requested')
    ),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (review_session_id, asset_version_id),
    UNIQUE (review_session_id, position)
);

CREATE TABLE review_participants (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id),
    display_name TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('reviewer', 'observer')),
    created_at TEXT NOT NULL,
    UNIQUE (review_session_id, display_name)
);

CREATE INDEX review_sessions_workspace_status_idx
    ON review_sessions (workspace_id, status, updated_at DESC);

CREATE INDEX review_items_version_idx
    ON review_items (workspace_id, asset_version_id, review_session_id);

CREATE INDEX review_participants_session_idx
    ON review_participants (review_session_id, created_at);
