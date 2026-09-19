ALTER TABLE authorized_roots ADD COLUMN purpose TEXT NOT NULL DEFAULT 'library'
    CHECK (purpose IN ('library', 'managed_versions'));

ALTER TABLE assets ADD COLUMN origin_storage_object_id TEXT;

UPDATE assets
SET origin_storage_object_id = (
    SELECT version_files.storage_object_id
    FROM asset_versions
    JOIN version_files
        ON version_files.asset_version_id = asset_versions.id
        AND version_files.role = 'primary'
    WHERE asset_versions.asset_id = assets.id
        AND asset_versions.deleted_at IS NULL
    ORDER BY asset_versions.version_number
    LIMIT 1
)
WHERE origin_storage_object_id IS NULL;

CREATE UNIQUE INDEX assets_origin_object_active_idx
    ON assets (origin_storage_object_id)
    WHERE origin_storage_object_id IS NOT NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX authorized_roots_managed_versions_idx
    ON authorized_roots (workspace_id, purpose)
    WHERE purpose = 'managed_versions' AND deleted_at IS NULL;
