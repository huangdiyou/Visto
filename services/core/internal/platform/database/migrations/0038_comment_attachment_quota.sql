CREATE INDEX comment_attachments_review_quota_idx
    ON comment_attachments (workspace_id, review_session_id, source_type, share_visitor_id);
