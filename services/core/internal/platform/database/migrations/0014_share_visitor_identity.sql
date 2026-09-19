CREATE TABLE share_visitors (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    display_name TEXT,
    identity_method TEXT NOT NULL CHECK (
        identity_method IN ('anonymous', 'nickname', 'verification_code')
    ),
    verified_at TEXT,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);

ALTER TABLE share_visitor_sessions
    ADD COLUMN share_visitor_id TEXT REFERENCES share_visitors(id);

CREATE TABLE share_visitor_codes (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    code_digest TEXT NOT NULL UNIQUE,
    code_prefix TEXT NOT NULL,
    display_name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    expires_at TEXT,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);

CREATE INDEX share_visitors_share_idx
    ON share_visitors (share_id, last_seen_at DESC);

CREATE INDEX share_visitor_codes_share_status_idx
    ON share_visitor_codes (share_id, status, created_at DESC);

CREATE INDEX share_visitor_codes_digest_idx
    ON share_visitor_codes (code_digest)
    WHERE status = 'active';
