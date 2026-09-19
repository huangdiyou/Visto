ALTER TABLE memberships
    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0);

ALTER TABLE memberships
    ADD COLUMN disabled_at TEXT;

CREATE INDEX memberships_workspace_role_status_idx
    ON memberships (workspace_id, role_key, status, created_at);
