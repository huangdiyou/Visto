-- inverts the remote transport policy into a positive setting.
--
-- 0039 stored the negative `allow_plaintext_http`, which defaulted to 0 and so
-- enforced HTTPS on every fresh install. That default blocked LAN HTTP before
-- an Owner could configure TLS, which does not fit a local-first self-hosted
-- product. The policy is now the positive `require_remote_https`, default 0.
--
-- Both old values map to 0: `allow_plaintext_http = 0` was the shipped default
-- that nobody chose, and `allow_plaintext_http = 1` was an explicit decision to
-- accept plaintext. Neither is an explicit request to enforce HTTPS, so no
-- deployment is silently locked out by this migration. `revision` is carried
-- over so open Owner pages fail with a revision conflict instead of writing a
-- value against the old semantics.
CREATE TABLE system_network_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    require_remote_https INTEGER NOT NULL DEFAULT 0 CHECK (require_remote_https IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT,
    updated_at TEXT NOT NULL
);

INSERT INTO system_network_settings (
    id, require_remote_https, revision, updated_by, updated_at
)
SELECT 1, 0, revision, updated_by, updated_at
FROM deployment_network_settings
WHERE id = 1;

INSERT OR IGNORE INTO system_network_settings (
    id, require_remote_https, revision, updated_at
) VALUES (1, 0, 1, '1970-01-01T00:00:00Z');

DROP TABLE deployment_network_settings;
