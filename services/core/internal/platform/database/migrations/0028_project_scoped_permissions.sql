ALTER TABLE projects ADD COLUMN primary_owner_user_id TEXT REFERENCES users(id);

UPDATE projects
SET primary_owner_user_id = created_by
WHERE primary_owner_user_id IS NULL
  AND deleted_at IS NULL;

CREATE TABLE project_memberships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_key TEXT NOT NULL CHECK (
        role_key IN ('primary_owner', 'supervisor', 'member', 'guest')
    ),
    permissions_json TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (
        status IN ('active', 'disabled', 'expired', 'removed')
    ),
    created_by TEXT NOT NULL REFERENCES users(id),
    expires_at TEXT,
    joined_at TEXT,
    removed_at TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX project_memberships_live_user_idx
    ON project_memberships (project_id, user_id)
    WHERE status IN ('active', 'disabled', 'expired');

CREATE UNIQUE INDEX project_memberships_primary_owner_idx
    ON project_memberships (project_id)
    WHERE role_key = 'primary_owner' AND status = 'active';

CREATE INDEX project_memberships_project_idx
    ON project_memberships (project_id, role_key, status, updated_at DESC);

CREATE INDEX project_memberships_user_idx
    ON project_memberships (workspace_id, user_id, status, updated_at DESC);

INSERT INTO project_memberships (
    id, workspace_id, project_id, user_id, role_key, permissions_json,
    status, created_by, joined_at, created_at, updated_at
)
SELECT
    'pmem_' || projects.id || '_' || projects.created_by,
    projects.workspace_id,
    projects.id,
    projects.created_by,
    'primary_owner',
    '{"project.manage":true,"project.members.manage":true,"assets.add":true,"assets.upload":true,"assets.remove":true,"reviews.create":true,"reviews.comment":true,"reviews.decide":true,"reviews.delete":true,"shares.create":true}',
    'active',
    projects.created_by,
    projects.created_at,
    projects.created_at,
    projects.updated_at
FROM projects
WHERE projects.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM project_memberships existing
      WHERE existing.project_id = projects.id
        AND existing.user_id = projects.created_by
        AND existing.status IN ('active', 'disabled', 'expired')
  );

CREATE TABLE project_invitations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    email TEXT,
    role_key TEXT NOT NULL CHECK (role_key IN ('supervisor', 'member', 'guest')),
    permissions_json TEXT NOT NULL DEFAULT '{}',
    token_digest TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'accepted', 'revoked', 'expired')
    ),
    expires_at TEXT NOT NULL,
    invited_by TEXT NOT NULL REFERENCES users(id),
    accepted_by_user_id TEXT REFERENCES users(id),
    send_count INTEGER NOT NULL DEFAULT 1 CHECK (send_count > 0),
    last_sent_at TEXT NOT NULL,
    accepted_at TEXT,
    revoked_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX project_invitations_pending_email_idx
    ON project_invitations (project_id, lower(email))
    WHERE status = 'pending' AND email IS NOT NULL;

CREATE INDEX project_invitations_project_status_idx
    ON project_invitations (project_id, status, created_at DESC);

CREATE INDEX project_invitations_token_idx
    ON project_invitations (token_digest, status, expires_at);

CREATE TABLE project_assets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    added_by TEXT NOT NULL REFERENCES users(id),
    position INTEGER CHECK (position IS NULL OR position >= 0),
    status TEXT NOT NULL CHECK (status IN ('active', 'trashed')),
    trashed_by TEXT REFERENCES users(id),
    trashed_at TEXT,
    trash_expires_at TEXT,
    restored_by TEXT REFERENCES users(id),
    restored_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX project_assets_project_asset_idx
    ON project_assets (project_id, asset_id);

CREATE INDEX project_assets_project_status_idx
    ON project_assets (project_id, status, updated_at DESC);

CREATE INDEX project_assets_asset_idx
    ON project_assets (workspace_id, asset_id, project_id);

INSERT INTO project_assets (
    id, workspace_id, project_id, asset_id, added_by, status,
    created_at, updated_at
)
SELECT
    'past_' || assets.project_id || '_' || assets.id,
    assets.workspace_id,
    assets.project_id,
    assets.id,
    assets.created_by,
    'active',
    assets.created_at,
    assets.updated_at
FROM assets
JOIN projects ON projects.id = assets.project_id
WHERE assets.project_id IS NOT NULL
  AND assets.deleted_at IS NULL
  AND projects.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM project_assets existing
      WHERE existing.project_id = assets.project_id
        AND existing.asset_id = assets.id
  );

CREATE TABLE project_storage_grants (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    storage_provider_id TEXT NOT NULL REFERENCES storage_providers(id) ON DELETE CASCADE,
    authorized_root_id TEXT REFERENCES authorized_roots(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    granted_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX project_storage_grants_active_idx
    ON project_storage_grants (
        workspace_id,
        storage_provider_id,
        COALESCE(authorized_root_id, '')
    )
    WHERE status = 'active';

CREATE INDEX project_storage_grants_workspace_idx
    ON project_storage_grants (workspace_id, status, updated_at DESC);

CREATE TABLE project_storage_selections (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    grant_id TEXT NOT NULL REFERENCES project_storage_grants(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (
        purpose IN ('upload', 'default_rendition', 'archive')
    ),
    selected_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (project_id, purpose)
);

CREATE INDEX project_storage_selections_project_idx
    ON project_storage_selections (project_id, purpose);

CREATE TABLE workspace_registration_settings (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    registration_enabled INTEGER NOT NULL DEFAULT 0 CHECK (
        registration_enabled IN (0, 1)
    ),
    email_verification_required INTEGER NOT NULL DEFAULT 0 CHECK (
        email_verification_required IN (0, 1)
    ),
    default_workspace_role TEXT NOT NULL DEFAULT 'member' CHECK (
        default_workspace_role = 'member'
    ),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT REFERENCES users(id),
    updated_at TEXT NOT NULL
);

INSERT INTO workspace_registration_settings (
    workspace_id, registration_enabled, email_verification_required,
    default_workspace_role, updated_at
)
SELECT
    workspaces.id,
    0,
    0,
    'member',
    workspaces.updated_at
FROM workspaces
WHERE NOT EXISTS (
    SELECT 1
    FROM workspace_registration_settings settings
    WHERE settings.workspace_id = workspaces.id
);
