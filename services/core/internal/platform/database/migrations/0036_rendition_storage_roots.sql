ALTER TABLE renditions
    ADD COLUMN authorized_root_id TEXT
        REFERENCES authorized_roots(id) ON DELETE SET NULL;

CREATE INDEX renditions_workspace_root_idx
    ON renditions (workspace_id, authorized_root_id, updated_at DESC);
