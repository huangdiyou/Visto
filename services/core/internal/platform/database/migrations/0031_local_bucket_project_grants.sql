INSERT INTO project_storage_grants (
    id,
    workspace_id,
    storage_provider_id,
    authorized_root_id,
    status,
    granted_by,
    created_at,
    updated_at
)
SELECT
    'lmb_grant_' || local_managed_buckets.id,
    local_managed_buckets.workspace_id,
    authorized_roots.storage_provider_id,
    local_managed_buckets.authorized_root_id,
    CASE
        WHEN local_managed_buckets.project_available = 1
             AND local_managed_buckets.status = 'active'
        THEN 'active'
        ELSE 'disabled'
    END,
    COALESCE(
        local_managed_buckets.created_by,
        (
            SELECT memberships.user_id
            FROM memberships
            WHERE memberships.workspace_id = local_managed_buckets.workspace_id
              AND memberships.role_key = 'owner'
              AND memberships.status = 'active'
            ORDER BY memberships.created_at
            LIMIT 1
        )
    ),
    local_managed_buckets.created_at,
    local_managed_buckets.updated_at
FROM local_managed_buckets
JOIN authorized_roots
    ON authorized_roots.id = local_managed_buckets.authorized_root_id
    AND authorized_roots.workspace_id = local_managed_buckets.workspace_id
    AND authorized_roots.deleted_at IS NULL
WHERE local_managed_buckets.deleted_at IS NULL
  AND COALESCE(
      local_managed_buckets.created_by,
      (
          SELECT memberships.user_id
          FROM memberships
          WHERE memberships.workspace_id = local_managed_buckets.workspace_id
            AND memberships.role_key = 'owner'
            AND memberships.status = 'active'
          ORDER BY memberships.created_at
          LIMIT 1
      )
  ) IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM project_storage_grants existing
      WHERE existing.workspace_id = local_managed_buckets.workspace_id
        AND existing.storage_provider_id = authorized_roots.storage_provider_id
        AND existing.authorized_root_id = local_managed_buckets.authorized_root_id
  );
