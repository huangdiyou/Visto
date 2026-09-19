CREATE TABLE comment_threads (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    review_session_id TEXT NOT NULL REFERENCES review_sessions(id) ON DELETE CASCADE,
    review_item_id TEXT NOT NULL REFERENCES review_items(id) ON DELETE CASCADE,
    asset_version_id TEXT NOT NULL REFERENCES asset_versions(id),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'share_visitor')),
    author_user_id TEXT REFERENCES users(id),
    author_visitor_id TEXT REFERENCES share_visitors(id),
    status TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    resolved_by_user_id TEXT REFERENCES users(id),
    resolved_at TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT,
    CHECK (
        (author_kind = 'user' AND author_user_id IS NOT NULL AND author_visitor_id IS NULL)
        OR
        (author_kind = 'share_visitor' AND author_user_id IS NULL AND author_visitor_id IS NOT NULL)
    )
);

CREATE TABLE comments (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES comment_threads(id) ON DELETE CASCADE,
    author_kind TEXT NOT NULL CHECK (
        author_kind IN ('user', 'share_visitor', 'system')
    ),
    author_user_id TEXT REFERENCES users(id),
    author_visitor_id TEXT REFERENCES share_visitors(id),
    body TEXT NOT NULL,
    edited_at TEXT,
    created_at TEXT NOT NULL,
    deleted_at TEXT,
    CHECK (
        (author_kind = 'user' AND author_user_id IS NOT NULL AND author_visitor_id IS NULL)
        OR
        (author_kind = 'share_visitor' AND author_user_id IS NULL AND author_visitor_id IS NOT NULL)
        OR
        (author_kind = 'system' AND author_user_id IS NULL AND author_visitor_id IS NULL)
    )
);

CREATE TABLE annotations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL UNIQUE REFERENCES comment_threads(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (
        kind IN (
            'whole', 'time_point', 'time_range', 'point',
            'region', 'drawing', 'page_region'
        )
    ),
    time_start_us INTEGER,
    time_end_us INTEGER,
    page_number INTEGER,
    geometry_version INTEGER NOT NULL DEFAULT 1 CHECK (geometry_version > 0),
    geometry_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (
        (kind = 'time_point' AND time_start_us IS NOT NULL
            AND time_start_us >= 0 AND time_end_us IS NULL)
        OR
        (kind = 'time_range' AND time_start_us IS NOT NULL
            AND time_end_us IS NOT NULL AND time_start_us >= 0
            AND time_end_us > time_start_us)
        OR
        (kind NOT IN ('time_point', 'time_range'))
    )
);

CREATE INDEX comment_threads_review_idx
    ON comment_threads (review_session_id, review_item_id, status, created_at);

CREATE INDEX comments_thread_idx
    ON comments (thread_id, created_at);

CREATE INDEX annotations_time_idx
    ON annotations (time_start_us, time_end_us)
    WHERE kind IN ('time_point', 'time_range');
