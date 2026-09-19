ALTER TABLE authorized_roots ADD COLUMN last_scan_at TEXT;
ALTER TABLE authorized_roots ADD COLUMN last_scan_status TEXT CHECK (
    last_scan_status IN ('running', 'succeeded', 'failed')
);
ALTER TABLE authorized_roots ADD COLUMN last_scan_summary_json TEXT;

CREATE TABLE directory_scans (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    authorized_root_id TEXT NOT NULL REFERENCES authorized_roots(id),
    status TEXT NOT NULL CHECK (
        status IN ('running', 'succeeded', 'failed')
    ),
    discovered_count INTEGER NOT NULL DEFAULT 0,
    new_count INTEGER NOT NULL DEFAULT 0,
    modified_count INTEGER NOT NULL DEFAULT 0,
    moved_count INTEGER NOT NULL DEFAULT 0,
    missing_count INTEGER NOT NULL DEFAULT 0,
    recovered_count INTEGER NOT NULL DEFAULT 0,
    unchanged_count INTEGER NOT NULL DEFAULT 0,
    error_code TEXT,
    error_message TEXT,
    started_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE storage_objects (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    storage_provider_id TEXT NOT NULL REFERENCES storage_providers(id),
    authorized_root_id TEXT REFERENCES authorized_roots(id),
    object_key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (
        kind IN ('source', 'rendition', 'temporary', 'avatar', 'attachment')
    ),
    status TEXT NOT NULL CHECK (
        status IN ('available', 'missing', 'deleting', 'deleted', 'error')
    ),
    size_bytes INTEGER,
    modified_at TEXT,
    etag TEXT,
    quick_fingerprint TEXT,
    content_hash TEXT,
    content_hash_algorithm TEXT,
    mime_type TEXT,
    first_discovered_at TEXT NOT NULL,
    last_seen_at TEXT,
    last_seen_scan_id TEXT REFERENCES directory_scans(id),
    missing_since TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE UNIQUE INDEX storage_objects_root_key_active_idx
    ON storage_objects (authorized_root_id, object_key)
    WHERE deleted_at IS NULL;

CREATE INDEX storage_objects_workspace_root_status_idx
    ON storage_objects (
        workspace_id, authorized_root_id, status, updated_at
    )
    WHERE deleted_at IS NULL;

CREATE INDEX storage_objects_root_fingerprint_idx
    ON storage_objects (authorized_root_id, quick_fingerprint)
    WHERE deleted_at IS NULL;

CREATE INDEX directory_scans_root_started_idx
    ON directory_scans (authorized_root_id, started_at DESC);
