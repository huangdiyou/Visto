-- D2, docs/FREE_TIER_BOUNDARY_DESIGN.md: the Owner must be able to add a storage
-- location from the web, which means a web session has to reach the host
-- management surface that was previously limited to the host token.
--
-- The choice is recorded once by the first-run wizard and is deliberately not
-- writable from the web afterwards: a setting that grants host-level access must
-- not be flippable by the session that benefits from it.
--
-- allow_web_host_paths defaults to 0. An instance that never recorded a choice
-- (for example one upgraded from before this setting existed) therefore keeps the
-- previous restrictive behaviour instead of silently relaxing it.
CREATE TABLE system_host_access_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    allow_web_host_paths INTEGER NOT NULL DEFAULT 0 CHECK (allow_web_host_paths IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT,
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO system_host_access_settings (
    id, allow_web_host_paths, revision, updated_at
) VALUES (1, 0, 1, '1970-01-01T00:00:00Z');
