CREATE TABLE review_decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
    review_item_id TEXT REFERENCES review_items(id) ON DELETE CASCADE,
    actor_kind TEXT NOT NULL CHECK (
        actor_kind IN ('user', 'share_visitor')
    ),
    actor_user_id TEXT REFERENCES users(id),
    actor_visitor_id TEXT REFERENCES share_visitors(id),
    decision TEXT NOT NULL CHECK (
        decision IN ('approved', 'changes_requested', 'rejected')
    ),
    note TEXT,
    created_at TEXT NOT NULL,
    CHECK (
        (actor_kind = 'user' AND actor_user_id IS NOT NULL
            AND actor_visitor_id IS NULL)
        OR
        (actor_kind = 'share_visitor' AND actor_user_id IS NULL
            AND actor_visitor_id IS NOT NULL)
    )
);

CREATE INDEX review_decisions_session_idx
    ON review_decisions (review_session_id, created_at DESC);

CREATE INDEX review_decisions_item_idx
    ON review_decisions (review_item_id, created_at DESC)
    WHERE review_item_id IS NOT NULL;
