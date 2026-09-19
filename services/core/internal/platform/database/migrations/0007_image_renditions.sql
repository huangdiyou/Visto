CREATE TABLE renditions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_storage_object_id TEXT NOT NULL
        REFERENCES storage_objects(id) ON DELETE CASCADE,
    asset_version_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('thumbnail', 'screen_preview')),
    profile_key TEXT NOT NULL,
    profile_version INTEGER NOT NULL CHECK (profile_version > 0),
    source_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('processing', 'ready', 'failed', 'stale')
    ),
    object_key TEXT,
    mime_type TEXT,
    width INTEGER,
    height INTEGER,
    size_bytes INTEGER,
    error_code TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    UNIQUE (
        source_storage_object_id,
        kind,
        profile_key,
        profile_version
    )
);

CREATE INDEX renditions_workspace_source_idx
    ON renditions (workspace_id, source_storage_object_id, updated_at DESC);

CREATE INDEX renditions_workspace_status_idx
    ON renditions (workspace_id, status, updated_at DESC);
