CREATE TABLE upload_checks (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    asset_version_id TEXT NOT NULL REFERENCES asset_versions(id) ON DELETE CASCADE,
    storage_object_id TEXT NOT NULL REFERENCES storage_objects(id) ON DELETE CASCADE,
    uploaded_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    share_visitor_id TEXT REFERENCES share_visitors(id) ON DELETE SET NULL,
    source_type TEXT NOT NULL CHECK (
        source_type IN ('user', 'visitor', 'system')
    ),
    upload_security_policy TEXT NOT NULL CHECK (
        upload_security_policy IN ('quick', 'standard', 'enhanced')
    ),
    status TEXT NOT NULL CHECK (
        status IN (
            'uploaded',
            'checking',
            'processing',
            'ready',
            'quarantined',
            'rejected'
        )
    ),
    result_code TEXT,
    message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    quarantined_at TEXT,
    rejected_at TEXT
);

CREATE UNIQUE INDEX upload_checks_asset_version_idx
    ON upload_checks (workspace_id, asset_version_id);

CREATE INDEX upload_checks_project_status_idx
    ON upload_checks (workspace_id, project_id, status, updated_at DESC);

CREATE INDEX upload_checks_storage_object_idx
    ON upload_checks (workspace_id, storage_object_id);

CREATE INDEX upload_checks_visitor_idx
    ON upload_checks (workspace_id, share_visitor_id, status, updated_at DESC)
    WHERE share_visitor_id IS NOT NULL;
