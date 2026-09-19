CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    locale TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended', 'deleting')),
    default_locale TEXT NOT NULL,
    timezone TEXT NOT NULL,
    settings_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE memberships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_key TEXT NOT NULL CHECK (role_key IN ('owner', 'admin', 'member', 'guest')),
    status TEXT NOT NULL CHECK (status IN ('invited', 'active', 'disabled')),
    joined_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (workspace_id, user_id)
);

CREATE TABLE user_credentials (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token_digest TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE INDEX sessions_active_token_idx
    ON sessions (token_digest, expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX memberships_workspace_idx
    ON memberships (workspace_id, status);

