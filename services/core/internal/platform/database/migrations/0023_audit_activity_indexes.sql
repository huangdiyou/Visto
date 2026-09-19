CREATE INDEX audit_logs_actor_idx
    ON audit_logs (workspace_id, actor_id, occurred_at DESC);

CREATE INDEX audit_logs_action_idx
    ON audit_logs (workspace_id, action, occurred_at DESC);

CREATE INDEX audit_logs_resource_type_idx
    ON audit_logs (workspace_id, resource_type, occurred_at DESC);
