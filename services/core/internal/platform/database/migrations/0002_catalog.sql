CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    cover_asset_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'archived')),
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    archived_at TEXT,
    deleted_at TEXT
);

CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    description TEXT,
    kind TEXT NOT NULL CHECK (
        kind IN ('delivery', 'playlist', 'portfolio_section')
    ),
    position INTEGER NOT NULL CHECK (position >= 0),
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE assets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id),
    type TEXT NOT NULL CHECK (
        type IN (
            'video', 'image', 'audio', 'pdf',
            'design', 'document', 'other'
        )
    ),
    name TEXT NOT NULL,
    description TEXT,
    current_version_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'archived')),
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    archived_at TEXT,
    deleted_at TEXT
);

CREATE TABLE collection_items (
    id TEXT PRIMARY KEY,
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES assets(id),
    pinned_version_id TEXT,
    position INTEGER NOT NULL CHECK (position >= 0),
    caption TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (collection_id, asset_id),
    UNIQUE (collection_id, position)
);

CREATE INDEX projects_workspace_status_idx
    ON projects (workspace_id, status, updated_at)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX collections_project_position_active_idx
    ON collections (project_id, position)
    WHERE deleted_at IS NULL;

CREATE INDEX collections_workspace_project_idx
    ON collections (workspace_id, project_id, position)
    WHERE deleted_at IS NULL;

CREATE INDEX assets_workspace_project_idx
    ON assets (workspace_id, project_id, type, updated_at)
    WHERE deleted_at IS NULL;

CREATE INDEX collection_items_collection_idx
    ON collection_items (collection_id, position);
