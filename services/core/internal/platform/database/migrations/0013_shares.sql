CREATE TABLE shares (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT REFERENCES review_sessions(id),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    allow_comment INTEGER NOT NULL CHECK (allow_comment IN (0, 1)),
    allow_download INTEGER NOT NULL CHECK (allow_download IN (0, 1)),
    require_nickname INTEGER NOT NULL CHECK (require_nickname IN (0, 1)),
    expires_at TEXT,
    max_visits INTEGER CHECK (max_visits IS NULL OR max_visits > 0),
    password_hash TEXT,
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE TABLE share_links (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    token_digest TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);

CREATE TABLE share_visitor_sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    share_link_id TEXT NOT NULL REFERENCES share_links(id) ON DELETE CASCADE,
    session_digest TEXT NOT NULL UNIQUE,
    verified_password_at TEXT,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE INDEX shares_workspace_status_idx
    ON shares (workspace_id, status, updated_at DESC);

CREATE INDEX share_links_share_status_idx
    ON share_links (share_id, status, created_at DESC);

CREATE INDEX share_sessions_digest_idx
    ON share_visitor_sessions (session_digest, expires_at)
    WHERE revoked_at IS NULL;
