CREATE TABLE storage_providers (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (
        kind IN ('local', 'webdav', 's3', 'official_cloud')
    ),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('active', 'offline', 'error', 'disabled')
    ),
    config_json TEXT NOT NULL DEFAULT '{}',
    secret_ref TEXT,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE local_path_secrets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    path_text TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE authorized_roots (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    storage_provider_id TEXT NOT NULL REFERENCES storage_providers(id),
    display_name TEXT NOT NULL,
    display_path TEXT NOT NULL,
    path_secret_ref TEXT NOT NULL REFERENCES local_path_secrets(id),
    platform_bookmark BLOB,
    mode TEXT NOT NULL CHECK (mode IN ('referenced', 'managed')),
    scan_enabled INTEGER NOT NULL CHECK (scan_enabled IN (0, 1)),
    status TEXT NOT NULL CHECK (
        status IN ('available', 'missing', 'permission_lost')
    ),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE UNIQUE INDEX storage_providers_workspace_name_active_idx
    ON storage_providers (workspace_id, kind, name)
    WHERE deleted_at IS NULL;

CREATE INDEX authorized_roots_workspace_status_idx
    ON authorized_roots (workspace_id, status, updated_at)
    WHERE deleted_at IS NULL;

CREATE INDEX authorized_roots_provider_idx
    ON authorized_roots (storage_provider_id)
    WHERE deleted_at IS NULL;
