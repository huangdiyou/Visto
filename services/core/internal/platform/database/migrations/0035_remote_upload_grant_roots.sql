INSERT INTO local_path_secrets (
    id,
    workspace_id,
    path_text,
    created_at,
    updated_at
)
SELECT
    'remote-root-path-' || project_storage_grants.id,
    project_storage_grants.workspace_id,
    '',
    project_storage_grants.updated_at,
    project_storage_grants.updated_at
FROM project_storage_grants
JOIN storage_providers
    ON storage_providers.id = project_storage_grants.storage_provider_id
    AND storage_providers.workspace_id = project_storage_grants.workspace_id
    AND storage_providers.deleted_at IS NULL
WHERE project_storage_grants.status = 'active'
  AND project_storage_grants.authorized_root_id IS NULL
  AND storage_providers.kind IN ('webdav', 's3')
  AND NOT EXISTS (
      SELECT 1
      FROM local_path_secrets existing
      WHERE existing.id = 'remote-root-path-' || project_storage_grants.id
  );

INSERT INTO authorized_roots (
    id,
    workspace_id,
    storage_provider_id,
    display_name,
    display_path,
    path_secret_ref,
    mode,
    scan_enabled,
    status,
    revision,
    created_at,
    updated_at
)
SELECT
    'remote-root-' || project_storage_grants.id,
    project_storage_grants.workspace_id,
    project_storage_grants.storage_provider_id,
    storage_providers.name,
    '/',
    'remote-root-path-' || project_storage_grants.id,
    'managed',
    1,
    'available',
    1,
    project_storage_grants.updated_at,
    project_storage_grants.updated_at
FROM project_storage_grants
JOIN storage_providers
    ON storage_providers.id = project_storage_grants.storage_provider_id
    AND storage_providers.workspace_id = project_storage_grants.workspace_id
    AND storage_providers.deleted_at IS NULL
WHERE project_storage_grants.status = 'active'
  AND project_storage_grants.authorized_root_id IS NULL
  AND storage_providers.kind IN ('webdav', 's3')
  AND EXISTS (
      SELECT 1
      FROM local_path_secrets secrets
      WHERE secrets.id = 'remote-root-path-' || project_storage_grants.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM authorized_roots existing
      WHERE existing.id = 'remote-root-' || project_storage_grants.id
  );

UPDATE project_storage_grants
SET
    authorized_root_id = 'remote-root-' || id,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'active'
  AND authorized_root_id IS NULL
  AND EXISTS (
      SELECT 1
      FROM storage_providers
      WHERE storage_providers.id = project_storage_grants.storage_provider_id
        AND storage_providers.workspace_id = project_storage_grants.workspace_id
        AND storage_providers.kind IN ('webdav', 's3')
        AND storage_providers.deleted_at IS NULL
  )
  AND EXISTS (
      SELECT 1
      FROM authorized_roots
      WHERE authorized_roots.id = 'remote-root-' || project_storage_grants.id
        AND authorized_roots.workspace_id = project_storage_grants.workspace_id
        AND authorized_roots.deleted_at IS NULL
  )
  AND NOT EXISTS (
      SELECT 1
      FROM project_storage_grants existing
      WHERE existing.workspace_id = project_storage_grants.workspace_id
        AND existing.storage_provider_id = project_storage_grants.storage_provider_id
        AND existing.authorized_root_id = 'remote-root-' || project_storage_grants.id
        AND existing.status = 'active'
  );
