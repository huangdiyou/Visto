-- Encoder selection, circuit breaker and Owner override for the managed media
-- runtime (docs/MEDIA_ENCODING_SELECTION_DESIGN.md 4).
--
-- Every default is "not probed yet / not specified", so an instance upgraded from
-- before this table existed keeps encoding exactly the way it did: nothing here
-- changes an existing deployment's encoder until a probe has actually run.
--
-- preferred_encoder is the Owner's explicit choice ('' = follow the probe).
-- detected_encoders is a JSON array written by the probe. active_encoder is what
-- the next job will use. failure_count counts consecutive failures of the active
-- encoder and resets on any success. The tripped_* columns record the breaker so
-- a restart cannot forget why the encoder changed.
CREATE TABLE system_media_encoding_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    preferred_encoder TEXT NOT NULL DEFAULT '',
    detected_encoders TEXT NOT NULL DEFAULT '[]',
    detected_at TEXT,
    active_encoder TEXT NOT NULL DEFAULT '',
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    tripped_encoder TEXT,
    tripped_reason TEXT,
    tripped_at TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by TEXT,
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO system_media_encoding_settings (
    id, preferred_encoder, detected_encoders, active_encoder, failure_count, revision, updated_at
) VALUES (1, '', '[]', '', 0, 1, '1970-01-01T00:00:00Z');
