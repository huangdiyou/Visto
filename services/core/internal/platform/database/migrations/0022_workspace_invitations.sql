CREATE TABLE workspace_invitations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role_key TEXT NOT NULL CHECK (role_key IN ('admin', 'member', 'guest')),
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

CREATE UNIQUE INDEX workspace_invitations_pending_email_idx
    ON workspace_invitations (workspace_id, lower(email))
    WHERE status = 'pending';

CREATE INDEX workspace_invitations_workspace_status_idx
    ON workspace_invitations (workspace_id, status, created_at DESC);

CREATE INDEX workspace_invitations_token_idx
    ON workspace_invitations (token_digest, status, expires_at);
