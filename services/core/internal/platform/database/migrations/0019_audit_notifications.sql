CREATE TABLE access_events (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    share_link_id TEXT REFERENCES share_links(id) ON DELETE SET NULL,
    visitor_id TEXT REFERENCES share_visitors(id) ON DELETE SET NULL,
    visitor_session_id TEXT REFERENCES share_visitor_sessions(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL CHECK (
        event_type IN (
            'opened', 'verified', 'identified', 'viewed',
            'downloaded', 'commented', 'decision_submitted'
        )
    ),
    resource_type TEXT,
    resource_id TEXT,
    ip_hash TEXT,
    user_agent_summary TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    occurred_at TEXT NOT NULL
);

CREATE TABLE audit_logs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_type TEXT NOT NULL CHECK (
        actor_type IN ('user', 'visitor', 'system', 'node')
    ),
    actor_id TEXT,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    request_id TEXT NOT NULL DEFAULT '',
    before_json TEXT NOT NULL DEFAULT '{}',
    after_json TEXT NOT NULL DEFAULT '{}',
    ip_hash TEXT,
    occurred_at TEXT NOT NULL
);

CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    recipient_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    read_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX access_events_share_idx
    ON access_events (share_id, occurred_at DESC);

CREATE INDEX access_events_workspace_idx
    ON access_events (workspace_id, occurred_at DESC);

CREATE INDEX audit_logs_workspace_idx
    ON audit_logs (workspace_id, occurred_at DESC);

CREATE INDEX audit_logs_resource_idx
    ON audit_logs (workspace_id, resource_type, resource_id, occurred_at DESC);

CREATE INDEX notifications_recipient_idx
    ON notifications (recipient_user_id, read_at, created_at DESC);
