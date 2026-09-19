CREATE UNIQUE INDEX users_email_unique_idx
    ON users (lower(email))
    WHERE email IS NOT NULL;

CREATE TABLE secret_values (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1 CHECK (key_version > 0),
    nonce BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX secret_values_workspace_purpose_idx
    ON secret_values (workspace_id, purpose, updated_at DESC);

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    notification_id TEXT REFERENCES notifications(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'processing', 'delivered', 'failed')
    ),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    available_at TEXT NOT NULL,
    locked_at TEXT,
    last_error_code TEXT,
    last_error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    delivered_at TEXT,
    UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX notification_outbox_claim_idx
    ON notification_outbox (status, available_at, created_at)
    WHERE status IN ('pending', 'failed');

CREATE INDEX notification_outbox_workspace_idx
    ON notification_outbox (workspace_id, updated_at DESC);
