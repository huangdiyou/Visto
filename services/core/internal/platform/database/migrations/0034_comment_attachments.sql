CREATE TABLE comment_attachments (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
    review_item_id TEXT NOT NULL REFERENCES review_items(id) ON DELETE CASCADE,
    thread_id TEXT REFERENCES comment_threads(id) ON DELETE CASCADE,
    comment_id TEXT REFERENCES comments(id) ON DELETE CASCADE,
    uploaded_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    share_visitor_id TEXT REFERENCES share_visitors(id) ON DELETE SET NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('user', 'share_visitor')),
    authorized_root_id TEXT NOT NULL REFERENCES authorized_roots(id),
    object_key TEXT NOT NULL,
    original_filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    width INTEGER CHECK (width IS NULL OR width > 0),
    height INTEGER CHECK (height IS NULL OR height > 0),
    upload_security_policy TEXT NOT NULL CHECK (
        upload_security_policy IN ('quick', 'standard', 'enhanced')
    ),
    status TEXT NOT NULL CHECK (
        status IN ('ready', 'quarantined', 'rejected')
    ),
    result_code TEXT,
    message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    attached_at TEXT,
    rejected_at TEXT,
    quarantined_at TEXT,
    CHECK (
        (source_type = 'user' AND uploaded_by_user_id IS NOT NULL AND share_visitor_id IS NULL)
        OR
        (source_type = 'share_visitor' AND uploaded_by_user_id IS NULL AND share_visitor_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX comment_attachments_root_key_idx
    ON comment_attachments (workspace_id, authorized_root_id, object_key);

CREATE INDEX comment_attachments_comment_idx
    ON comment_attachments (workspace_id, comment_id, status, created_at);

CREATE INDEX comment_attachments_pending_idx
    ON comment_attachments (
        workspace_id, review_session_id, review_item_id,
        source_type, uploaded_by_user_id, share_visitor_id,
        status, created_at
    )
    WHERE comment_id IS NULL;
