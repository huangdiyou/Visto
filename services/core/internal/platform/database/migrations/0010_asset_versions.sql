CREATE TABLE asset_versions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    label TEXT,
    note TEXT,
    processing_status TEXT NOT NULL CHECK (
        processing_status IN ('pending', 'processing', 'ready', 'failed', 'partial')
    ),
    source_filename TEXT NOT NULL,
    source_mime TEXT,
    source_size_bytes INTEGER NOT NULL,
    source_fingerprint TEXT NOT NULL,
    media_metadata_json TEXT NOT NULL DEFAULT '{}',
    probe_raw_json TEXT,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    UNIQUE (asset_id, version_number)
);

CREATE TABLE version_files (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    asset_version_id TEXT NOT NULL REFERENCES asset_versions(id) ON DELETE CASCADE,
    storage_object_id TEXT NOT NULL REFERENCES storage_objects(id),
    role TEXT NOT NULL CHECK (role IN ('primary', 'source_attachment')),
    created_at TEXT NOT NULL,
    UNIQUE (asset_version_id, role)
);

CREATE INDEX asset_versions_workspace_asset_idx
    ON asset_versions (workspace_id, asset_id, version_number DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX version_files_workspace_object_idx
    ON version_files (workspace_id, storage_object_id);

INSERT INTO assets (
    id, workspace_id, type, name, status, created_by,
    revision, created_at, updated_at
)
SELECT
    'asset:' || storage_objects.id,
    storage_objects.workspace_id,
    CASE
        WHEN storage_objects.mime_type LIKE 'video/%' THEN 'video'
        WHEN storage_objects.mime_type LIKE 'image/%' THEN 'image'
        WHEN storage_objects.mime_type LIKE 'audio/%' THEN 'audio'
        WHEN storage_objects.mime_type = 'application/pdf' THEN 'pdf'
        ELSE 'other'
    END,
    storage_objects.object_key,
    'active',
    (
        SELECT memberships.user_id
        FROM memberships
        WHERE memberships.workspace_id = storage_objects.workspace_id
            AND memberships.role_key = 'owner'
            AND memberships.status = 'active'
        ORDER BY memberships.created_at
        LIMIT 1
    ),
    1,
    storage_objects.created_at,
    storage_objects.updated_at
FROM storage_objects
WHERE storage_objects.kind = 'source'
    AND storage_objects.deleted_at IS NULL
    AND NOT EXISTS (
        SELECT 1
        FROM version_files
        WHERE version_files.storage_object_id = storage_objects.id
    );

INSERT INTO asset_versions (
    id, workspace_id, asset_id, version_number, processing_status,
    source_filename, source_mime, source_size_bytes, source_fingerprint,
    media_metadata_json, created_by, created_at
)
SELECT
    'version:' || storage_objects.id || ':1',
    storage_objects.workspace_id,
    'asset:' || storage_objects.id,
    1,
    'pending',
    storage_objects.object_key,
    storage_objects.mime_type,
    storage_objects.size_bytes,
    storage_objects.quick_fingerprint,
    '{}',
    assets.created_by,
    storage_objects.created_at
FROM storage_objects
JOIN assets ON assets.id = 'asset:' || storage_objects.id
WHERE storage_objects.kind = 'source'
    AND storage_objects.deleted_at IS NULL;

INSERT INTO version_files (
    id, workspace_id, asset_version_id, storage_object_id, role, created_at
)
SELECT
    'version-file:' || storage_objects.id || ':1',
    storage_objects.workspace_id,
    'version:' || storage_objects.id || ':1',
    storage_objects.id,
    'primary',
    storage_objects.created_at
FROM storage_objects
JOIN asset_versions
    ON asset_versions.id = 'version:' || storage_objects.id || ':1'
WHERE storage_objects.kind = 'source'
    AND storage_objects.deleted_at IS NULL;

UPDATE assets
SET current_version_id = (
    SELECT asset_versions.id
    FROM asset_versions
    WHERE asset_versions.asset_id = assets.id
        AND asset_versions.deleted_at IS NULL
    ORDER BY asset_versions.version_number DESC
    LIMIT 1
)
WHERE current_version_id IS NULL
    AND EXISTS (
        SELECT 1 FROM asset_versions WHERE asset_versions.asset_id = assets.id
    );
