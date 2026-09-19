CREATE TABLE media_probes (
    storage_object_id TEXT PRIMARY KEY REFERENCES storage_objects(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed')),
    source_fingerprint TEXT NOT NULL,
    media_type TEXT NOT NULL CHECK (
        media_type IN (
            'video', 'image', 'audio', 'pdf',
            'design', 'document', 'other'
        )
    ),
    format_name TEXT,
    format_long_name TEXT,
    duration_us INTEGER,
    bit_rate INTEGER,
    width INTEGER,
    height INTEGER,
    rotation_degrees INTEGER,
    frame_rate REAL,
    video_codec TEXT,
    audio_codec TEXT,
    raw_metadata_json TEXT,
    error_code TEXT,
    error_message TEXT,
    probed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX media_probes_workspace_type_idx
    ON media_probes (workspace_id, media_type, probed_at DESC);

CREATE INDEX media_probes_status_idx
    ON media_probes (workspace_id, status, updated_at DESC);
