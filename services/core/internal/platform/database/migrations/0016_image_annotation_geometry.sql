CREATE INDEX annotations_geometry_kind_idx
    ON annotations (kind, geometry_version, created_at)
    WHERE kind IN ('point', 'region');

CREATE TRIGGER annotations_geometry_insert_guard
BEFORE INSERT ON annotations
WHEN NEW.kind IN ('point', 'region')
BEGIN
    SELECT CASE
        WHEN NEW.geometry_json IS NULL OR json_valid(NEW.geometry_json) != 1
        THEN RAISE(ABORT, 'invalid annotation geometry')
    END;
    SELECT CASE
        WHEN COALESCE(json_type(NEW.geometry_json, '$.x'), '') NOT IN ('integer', 'real')
            OR COALESCE(json_type(NEW.geometry_json, '$.y'), '') NOT IN ('integer', 'real')
            OR json_extract(NEW.geometry_json, '$.x') < 0
            OR json_extract(NEW.geometry_json, '$.x') > 1
            OR json_extract(NEW.geometry_json, '$.y') < 0
            OR json_extract(NEW.geometry_json, '$.y') > 1
        THEN RAISE(ABORT, 'invalid annotation coordinates')
    END;
    SELECT CASE
        WHEN NEW.kind = 'point'
            AND json_extract(NEW.geometry_json, '$.shape') != 'point'
        THEN RAISE(ABORT, 'invalid point annotation geometry')
    END;
    SELECT CASE
        WHEN NEW.kind = 'region'
            AND (
                json_extract(NEW.geometry_json, '$.shape') != 'rect'
                OR COALESCE(json_type(NEW.geometry_json, '$.width'), '') NOT IN ('integer', 'real')
                OR COALESCE(json_type(NEW.geometry_json, '$.height'), '') NOT IN ('integer', 'real')
                OR json_extract(NEW.geometry_json, '$.width') <= 0
                OR json_extract(NEW.geometry_json, '$.height') <= 0
                OR json_extract(NEW.geometry_json, '$.x')
                    + json_extract(NEW.geometry_json, '$.width') > 1
                OR json_extract(NEW.geometry_json, '$.y')
                    + json_extract(NEW.geometry_json, '$.height') > 1
            )
        THEN RAISE(ABORT, 'invalid region annotation geometry')
    END;
END;

CREATE TRIGGER annotations_geometry_update_guard
BEFORE UPDATE OF kind, geometry_json, geometry_version ON annotations
WHEN NEW.kind IN ('point', 'region')
BEGIN
    SELECT CASE
        WHEN NEW.geometry_json IS NULL OR json_valid(NEW.geometry_json) != 1
        THEN RAISE(ABORT, 'invalid annotation geometry')
    END;
    SELECT CASE
        WHEN COALESCE(json_type(NEW.geometry_json, '$.x'), '') NOT IN ('integer', 'real')
            OR COALESCE(json_type(NEW.geometry_json, '$.y'), '') NOT IN ('integer', 'real')
            OR json_extract(NEW.geometry_json, '$.x') < 0
            OR json_extract(NEW.geometry_json, '$.x') > 1
            OR json_extract(NEW.geometry_json, '$.y') < 0
            OR json_extract(NEW.geometry_json, '$.y') > 1
        THEN RAISE(ABORT, 'invalid annotation coordinates')
    END;
    SELECT CASE
        WHEN NEW.kind = 'point'
            AND json_extract(NEW.geometry_json, '$.shape') != 'point'
        THEN RAISE(ABORT, 'invalid point annotation geometry')
    END;
    SELECT CASE
        WHEN NEW.kind = 'region'
            AND (
                json_extract(NEW.geometry_json, '$.shape') != 'rect'
                OR COALESCE(json_type(NEW.geometry_json, '$.width'), '') NOT IN ('integer', 'real')
                OR COALESCE(json_type(NEW.geometry_json, '$.height'), '') NOT IN ('integer', 'real')
                OR json_extract(NEW.geometry_json, '$.width') <= 0
                OR json_extract(NEW.geometry_json, '$.height') <= 0
                OR json_extract(NEW.geometry_json, '$.x')
                    + json_extract(NEW.geometry_json, '$.width') > 1
                OR json_extract(NEW.geometry_json, '$.y')
                    + json_extract(NEW.geometry_json, '$.height') > 1
            )
        THEN RAISE(ABORT, 'invalid region annotation geometry')
    END;
END;
