DROP INDEX renditions_workspace_source_idx;
DROP INDEX renditions_workspace_status_idx;

CREATE TABLE renditions_next (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_storage_object_id TEXT NOT NULL
        REFERENCES storage_objects(id) ON DELETE CASCADE,
    asset_version_id TEXT,
    kind TEXT NOT NULL CHECK (
        kind IN (
            'thumbnail', 'screen_preview', 'poster',
            'proxy', 'hls', 'storyboard'
        )
    ),
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
    duration_us INTEGER,
    size_bytes INTEGER,
    metadata_json TEXT NOT NULL DEFAULT '{}',
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

INSERT INTO renditions_next (
    id, workspace_id, source_storage_object_id, asset_version_id,
    kind, profile_key, profile_version, source_fingerprint, status,
    object_key, mime_type, width, height, size_bytes,
    error_code, error_message, created_at, updated_at, completed_at
)
SELECT
    id, workspace_id, source_storage_object_id, asset_version_id,
    kind, profile_key, profile_version, source_fingerprint, status,
    object_key, mime_type, width, height, size_bytes,
    error_code, error_message, created_at, updated_at, completed_at
FROM renditions;

DROP TABLE renditions;
ALTER TABLE renditions_next RENAME TO renditions;

CREATE INDEX renditions_workspace_source_idx
    ON renditions (workspace_id, source_storage_object_id, updated_at DESC);

CREATE INDEX renditions_workspace_status_idx
    ON renditions (workspace_id, status, updated_at DESC);

CREATE TABLE rendition_segments (
    id TEXT PRIMARY KEY,
    rendition_id TEXT NOT NULL REFERENCES renditions(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (
        role IN ('master', 'playlist', 'init', 'segment')
    ),
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    file_name TEXT NOT NULL,
    object_key TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    duration_us INTEGER,
    created_at TEXT NOT NULL,
    UNIQUE (rendition_id, file_name),
    UNIQUE (rendition_id, role, sequence)
);

CREATE INDEX rendition_segments_rendition_idx
    ON rendition_segments (rendition_id, role, sequence);
