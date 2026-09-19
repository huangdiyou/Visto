CREATE TABLE notification_channels (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('email', 'feishu', 'wechat_work')),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled', 'error')),
    config_json TEXT NOT NULL DEFAULT '{}',
    secret_ref TEXT REFERENCES secret_values(id) ON DELETE SET NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    last_test_at TEXT,
    last_test_status TEXT CHECK (last_test_status IN ('succeeded', 'failed')),
    last_error_code TEXT,
    last_error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE UNIQUE INDEX notification_channels_name_idx
    ON notification_channels (workspace_id, kind, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX notification_channels_workspace_idx
    ON notification_channels (workspace_id, kind, status, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE notification_preferences (
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_kind TEXT NOT NULL CHECK (
        channel_kind IN ('email', 'feishu', 'wechat_work')
    ),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, user_id, channel_kind)
);

CREATE TABLE notification_deliveries (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    outbox_id TEXT NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    notification_id TEXT REFERENCES notifications(id) ON DELETE SET NULL,
    recipient_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    channel_id TEXT REFERENCES notification_channels(id) ON DELETE SET NULL,
    channel_kind TEXT NOT NULL CHECK (
        channel_kind IN ('email', 'feishu', 'wechat_work')
    ),
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('queued', 'sending', 'succeeded', 'failed', 'skipped')
    ),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    last_error_code TEXT,
    last_error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    delivered_at TEXT,
    UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX notification_deliveries_outbox_idx
    ON notification_deliveries (outbox_id, status, updated_at DESC);

CREATE INDEX notification_deliveries_workspace_idx
    ON notification_deliveries (workspace_id, status, updated_at DESC);
