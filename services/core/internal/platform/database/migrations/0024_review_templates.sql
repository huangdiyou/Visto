CREATE TABLE review_templates (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    participant_roles_json TEXT NOT NULL,
    allow_download INTEGER NOT NULL DEFAULT 0 CHECK (allow_download IN (0, 1)),
    due_days INTEGER CHECK (due_days IS NULL OR (due_days >= 1 AND due_days <= 365)),
    decision_rule TEXT NOT NULL CHECK (
        decision_rule IN ('any_reviewer', 'all_reviewers', 'responsible_only')
    ),
    created_by TEXT NOT NULL REFERENCES users(id),
    revision INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE UNIQUE INDEX review_templates_name_idx
    ON review_templates (workspace_id, lower(name))
    WHERE deleted_at IS NULL;

CREATE INDEX review_templates_workspace_idx
    ON review_templates (workspace_id, updated_at DESC)
    WHERE deleted_at IS NULL;

ALTER TABLE review_sessions ADD COLUMN template_id TEXT;
ALTER TABLE review_sessions ADD COLUMN template_revision INTEGER;
ALTER TABLE review_sessions ADD COLUMN responsible_user_id TEXT REFERENCES users(id);
ALTER TABLE review_sessions ADD COLUMN allow_download INTEGER NOT NULL DEFAULT 0
    CHECK (allow_download IN (0, 1));
ALTER TABLE review_sessions ADD COLUMN decision_rule TEXT NOT NULL DEFAULT 'any_reviewer'
    CHECK (decision_rule IN ('any_reviewer', 'all_reviewers', 'responsible_only'));

CREATE INDEX review_sessions_responsible_idx
    ON review_sessions (workspace_id, responsible_user_id, updated_at DESC);
