CREATE TABLE storage_copy_tasks (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_storage_object_id TEXT NOT NULL REFERENCES storage_objects(id),
    source_root_id TEXT NOT NULL REFERENCES authorized_roots(id),
    source_object_key TEXT NOT NULL,
    target_root_id TEXT NOT NULL REFERENCES authorized_roots(id),
    target_object_key TEXT NOT NULL,
    target_storage_object_id TEXT REFERENCES storage_objects(id),
    job_id TEXT REFERENCES jobs(id),
    status TEXT NOT NULL CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')
    ),
    total_bytes INTEGER NOT NULL DEFAULT 0 CHECK (total_bytes >= 0),
    copied_bytes INTEGER NOT NULL DEFAULT 0 CHECK (copied_bytes >= 0),
    content_hash TEXT,
    content_hash_algorithm TEXT,
    temp_file_name TEXT NOT NULL,
    error_code TEXT,
    error_message TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE INDEX storage_copy_tasks_workspace_status_idx
    ON storage_copy_tasks (workspace_id, status, updated_at DESC);

CREATE INDEX storage_copy_tasks_source_idx
    ON storage_copy_tasks (source_storage_object_id, created_at DESC);

CREATE INDEX storage_copy_tasks_target_idx
    ON storage_copy_tasks (target_root_id, target_object_key);
