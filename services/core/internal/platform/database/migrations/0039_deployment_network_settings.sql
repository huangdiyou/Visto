CREATE TABLE deployment_network_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    allow_plaintext_http INTEGER NOT NULL DEFAULT 0 CHECK (allow_plaintext_http IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT,
    updated_at TEXT NOT NULL
);

INSERT INTO deployment_network_settings (
    id, allow_plaintext_http, updated_at
) VALUES (1, 0, '1970-01-01T00:00:00Z');
