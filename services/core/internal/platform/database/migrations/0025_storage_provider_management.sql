ALTER TABLE storage_providers ADD COLUMN revision INTEGER NOT NULL DEFAULT 1
    CHECK (revision > 0);
ALTER TABLE storage_providers ADD COLUMN last_test_at TEXT;
ALTER TABLE storage_providers ADD COLUMN last_test_status TEXT CHECK (
    last_test_status IS NULL OR last_test_status IN ('succeeded', 'failed')
);
ALTER TABLE storage_providers ADD COLUMN last_error_code TEXT;
ALTER TABLE storage_providers ADD COLUMN last_error_message TEXT;

DROP INDEX storage_providers_workspace_name_active_idx;
CREATE UNIQUE INDEX storage_providers_workspace_name_active_idx
    ON storage_providers (workspace_id, kind, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX storage_providers_workspace_status_idx
    ON storage_providers (workspace_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;
