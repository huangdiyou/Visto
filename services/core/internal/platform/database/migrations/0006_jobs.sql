CREATE TABLE media_nodes (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (
        kind IN ('embedded', 'desktop', 'docker', 'cloud')
    ),
    status TEXT NOT NULL CHECK (
        status IN ('online', 'offline', 'disabled')
    ),
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    software_version TEXT NOT NULL,
    last_seen_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE jobs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    status TEXT NOT NULL CHECK (
        status IN (
            'queued', 'leased', 'running', 'succeeded', 'failed',
            'cancel_requested', 'cancelled'
        )
    ),
    priority INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    progress_current INTEGER,
    progress_total INTEGER,
    progress_unit TEXT,
    available_at TEXT NOT NULL,
    max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_code TEXT,
    last_error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT
);

CREATE TABLE job_attempts (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    media_node_id TEXT REFERENCES media_nodes(id),
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    lease_token_digest TEXT NOT NULL,
    lease_expires_at TEXT NOT NULL,
    started_at TEXT NOT NULL,
    heartbeat_at TEXT NOT NULL,
    finished_at TEXT,
    status TEXT NOT NULL CHECK (
        status IN ('running', 'succeeded', 'failed', 'abandoned', 'cancelled')
    ),
    error_code TEXT,
    error_message TEXT,
    diagnostics_json TEXT,
    UNIQUE (job_id, attempt_number)
);

CREATE UNIQUE INDEX jobs_active_idempotency_idx
    ON jobs (workspace_id, type, idempotency_key)
    WHERE idempotency_key IS NOT NULL
        AND status IN ('queued', 'leased', 'running', 'cancel_requested');

CREATE INDEX jobs_claim_idx
    ON jobs (status, available_at, priority DESC, created_at);

CREATE INDEX jobs_workspace_updated_idx
    ON jobs (workspace_id, updated_at DESC);

CREATE INDEX jobs_workspace_subject_idx
    ON jobs (workspace_id, subject_type, subject_id, updated_at DESC);

CREATE INDEX job_attempts_lease_idx
    ON job_attempts (status, lease_expires_at);
