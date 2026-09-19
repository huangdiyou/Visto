-- adds a token-bucket store for throughput budgets.
--
-- rate_limit_buckets is a fixed window, which is the right shape for
-- brute-force budgets ("5 password attempts, then stop"). It is the wrong shape
-- for a person typing a burst of review comments: everyone is released at the
-- same clock boundary and a user who hits the cap waits for the whole window.
--
-- token_balance and refill_per_second describe a bucket that refills smoothly,
-- so a normal reviewer submitting many change notes in a row is never blocked
-- while a script still cannot exceed the sustained rate. updated_at_epoch keeps
-- elapsed-time arithmetic inside a single atomic UPSERT without relying on
-- SQLite date parsing of RFC 3339 nanosecond strings. last_allowed carries the
-- decision of the most recent attempt back to the caller.
CREATE TABLE rate_limit_token_buckets (
    key_digest TEXT PRIMARY KEY,
    token_balance REAL NOT NULL,
    burst_capacity REAL NOT NULL,
    refill_per_second REAL NOT NULL,
    last_allowed INTEGER NOT NULL CHECK (last_allowed IN (0, 1)),
    updated_at TEXT NOT NULL,
    updated_at_epoch REAL NOT NULL
);

CREATE INDEX idx_rate_limit_token_buckets_updated_at
    ON rate_limit_token_buckets(updated_at);
