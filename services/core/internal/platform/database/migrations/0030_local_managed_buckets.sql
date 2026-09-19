CREATE TABLE local_managed_buckets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    authorized_root_id TEXT NOT NULL REFERENCES authorized_roots(id),
    display_name TEXT NOT NULL,
    purpose TEXT NOT NULL CHECK (
        purpose IN ('upload', 'source_archive', 'review_upload')
    ),
    quota_bytes INTEGER CHECK (quota_bytes IS NULL OR quota_bytes >= 0),
    used_bytes_estimate INTEGER CHECK (
        used_bytes_estimate IS NULL OR used_bytes_estimate >= 0
    ),
    upload_security_policy TEXT NOT NULL CHECK (
        upload_security_policy IN ('quick', 'standard', 'enhanced')
    ),
    project_available INTEGER NOT NULL CHECK (project_available IN (0, 1)),
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled', 'error')),
    created_by TEXT REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE UNIQUE INDEX local_managed_buckets_root_active_idx
    ON local_managed_buckets (workspace_id, authorized_root_id)
    WHERE deleted_at IS NULL;

CREATE INDEX local_managed_buckets_workspace_status_idx
    ON local_managed_buckets (workspace_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;
