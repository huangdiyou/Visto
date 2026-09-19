CREATE INDEX storage_objects_library_modified_idx
    ON storage_objects (
        workspace_id, authorized_root_id, modified_at DESC, id
    )
    WHERE deleted_at IS NULL;

CREATE INDEX storage_objects_library_name_idx
    ON storage_objects (
        workspace_id, authorized_root_id, object_key COLLATE NOCASE, id
    )
    WHERE deleted_at IS NULL;

CREATE INDEX storage_objects_library_size_idx
    ON storage_objects (
        workspace_id, authorized_root_id, size_bytes, id
    )
    WHERE deleted_at IS NULL;
