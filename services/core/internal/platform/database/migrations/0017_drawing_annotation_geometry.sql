CREATE INDEX annotations_drawing_kind_idx
    ON annotations (kind, geometry_version, created_at)
    WHERE kind = 'drawing';

CREATE TRIGGER annotations_drawing_insert_guard
BEFORE INSERT ON annotations
WHEN NEW.kind = 'drawing'
BEGIN
    SELECT CASE
        WHEN NEW.geometry_json IS NULL OR json_valid(NEW.geometry_json) != 1
        THEN RAISE(ABORT, 'invalid drawing annotation geometry')
    END;
    SELECT CASE
        WHEN json_extract(NEW.geometry_json, '$.shape') != 'drawing'
            OR COALESCE(json_type(NEW.geometry_json, '$.elements'), '') != 'array'
            OR json_array_length(NEW.geometry_json, '$.elements') < 1
            OR json_array_length(NEW.geometry_json, '$.elements') > 20
        THEN RAISE(ABORT, 'invalid drawing annotation elements')
    END;
END;

CREATE TRIGGER annotations_drawing_update_guard
BEFORE UPDATE OF kind, geometry_json, geometry_version ON annotations
WHEN NEW.kind = 'drawing'
BEGIN
    SELECT CASE
        WHEN NEW.geometry_json IS NULL OR json_valid(NEW.geometry_json) != 1
        THEN RAISE(ABORT, 'invalid drawing annotation geometry')
    END;
    SELECT CASE
        WHEN json_extract(NEW.geometry_json, '$.shape') != 'drawing'
            OR COALESCE(json_type(NEW.geometry_json, '$.elements'), '') != 'array'
            OR json_array_length(NEW.geometry_json, '$.elements') < 1
            OR json_array_length(NEW.geometry_json, '$.elements') > 20
        THEN RAISE(ABORT, 'invalid drawing annotation elements')
    END;
END;
