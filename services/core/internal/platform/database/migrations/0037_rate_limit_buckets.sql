CREATE TABLE rate_limit_buckets (
    key_digest TEXT PRIMARY KEY,
    attempt_count INTEGER NOT NULL,
    window_started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_rate_limit_buckets_updated_at
    ON rate_limit_buckets(updated_at);
