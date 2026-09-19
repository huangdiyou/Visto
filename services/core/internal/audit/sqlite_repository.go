package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) CreateAccess(
	ctx context.Context,
	record accessRecord,
) (AccessEvent, error) {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO access_events (
			id, workspace_id, share_id, share_link_id, visitor_id,
			visitor_session_id, event_type, resource_type, resource_id,
			ip_hash, user_agent_summary, metadata_json, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ShareID, record.ShareLinkID,
		record.VisitorID, record.VisitorSessionID, record.EventType,
		record.ResourceType, record.ResourceID, record.IPHash,
		record.UserAgentSummary, record.MetadataJSON, formatTime(record.OccurredAt))
	if err != nil {
		return AccessEvent{}, fmt.Errorf("create access event: %w", err)
	}
	return record.AccessEvent, nil
}

func (repository *SQLiteRepository) ListAccess(
	ctx context.Context,
	workspaceID string,
	shareID string,
	limit int,
) ([]AccessEvent, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT access_events.id, access_events.workspace_id,
			access_events.share_id, access_events.share_link_id,
			access_events.visitor_id, access_events.visitor_session_id,
			COALESCE(share_visitors.display_name, ''),
			access_events.event_type, access_events.resource_type,
			access_events.resource_id, access_events.ip_hash,
			access_events.user_agent_summary, access_events.occurred_at
		FROM access_events
		LEFT JOIN share_visitors ON share_visitors.id = access_events.visitor_id
		WHERE access_events.workspace_id = ? AND access_events.share_id = ?
		ORDER BY access_events.occurred_at DESC
		LIMIT ?
	`, workspaceID, shareID, limit)
	if err != nil {
		return nil, fmt.Errorf("list access events: %w", err)
	}
	defer rows.Close()

	items := make([]AccessEvent, 0)
	for rows.Next() {
		item, err := scanAccessEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access events: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) CreateLog(
	ctx context.Context,
	record logRecord,
) (AuditLog, error) {
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_type, actor_id, action, resource_type,
			resource_id, request_id, before_json, after_json, ip_hash,
			occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ActorType, record.ActorID,
		record.Action, record.ResourceType, record.ResourceID, record.RequestID,
		record.BeforeJSON, record.AfterJSON, record.IPHash,
		formatTime(record.OccurredAt))
	if err != nil {
		return AuditLog{}, fmt.Errorf("create audit log: %w", err)
	}
	return record.AuditLog, nil
}

func (repository *SQLiteRepository) ListLogs(
	ctx context.Context,
	workspaceID string,
	filter LogListFilter,
) ([]AuditLog, error) {
	query := `
		SELECT audit_logs.id, audit_logs.workspace_id, audit_logs.actor_type,
			audit_logs.actor_id,
			COALESCE(users.display_name, share_visitors.display_name, ''),
			audit_logs.action, audit_logs.resource_type, audit_logs.resource_id,
			audit_logs.request_id, audit_logs.after_json, audit_logs.ip_hash,
			audit_logs.occurred_at
		FROM audit_logs
		LEFT JOIN users
			ON audit_logs.actor_type = 'user' AND users.id = audit_logs.actor_id
		LEFT JOIN share_visitors
			ON audit_logs.actor_type = 'visitor'
			AND share_visitors.id = audit_logs.actor_id
		WHERE audit_logs.workspace_id = ?`
	args := []any{workspaceID}
	if strings.TrimSpace(filter.ActorID) != "" {
		query += " AND audit_logs.actor_id = ?"
		args = append(args, strings.TrimSpace(filter.ActorID))
	}
	if strings.TrimSpace(filter.Action) != "" {
		query += " AND audit_logs.action = ?"
		args = append(args, strings.TrimSpace(filter.Action))
	}
	if strings.TrimSpace(filter.ResourceType) != "" {
		query += " AND audit_logs.resource_type = ?"
		args = append(args, strings.TrimSpace(filter.ResourceType))
	}
	if strings.TrimSpace(filter.ResourceID) != "" {
		query += " AND audit_logs.resource_id = ?"
		args = append(args, strings.TrimSpace(filter.ResourceID))
	}
	query += " ORDER BY audit_logs.occurred_at DESC LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	items := make([]AuditLog, 0)
	for rows.Next() {
		item, err := scanAuditLog(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit logs: %w", err)
	}
	return items, nil
}

type scanner interface {
	Scan(...any) error
}

func scanAccessEvent(row scanner) (AccessEvent, error) {
	var item AccessEvent
	var linkID, visitorID, sessionID, resourceType, resourceID, ipHash sql.NullString
	var occurredAt string
	if err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.ShareID, &linkID, &visitorID,
		&sessionID, &item.VisitorName, &item.EventType, &resourceType,
		&resourceID, &ipHash, &item.UserAgentSummary, &occurredAt,
	); err != nil {
		return AccessEvent{}, fmt.Errorf("scan access event: %w", err)
	}
	item.ShareLinkID = nullableString(linkID)
	item.VisitorID = nullableString(visitorID)
	item.VisitorSessionID = nullableString(sessionID)
	item.ResourceType = nullableString(resourceType)
	item.ResourceID = nullableString(resourceID)
	item.IPHash = nullableString(ipHash)
	value, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return AccessEvent{}, fmt.Errorf("parse access event time: %w", err)
	}
	item.OccurredAt = value
	return item, nil
}

func scanAuditLog(row scanner) (AuditLog, error) {
	var item AuditLog
	var actorID, ipHash sql.NullString
	var afterJSON, occurredAt string
	if err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.ActorType, &actorID,
		&item.ActorName, &item.Action, &item.ResourceType, &item.ResourceID,
		&item.RequestID, &afterJSON, &ipHash, &occurredAt,
	); err != nil {
		return AuditLog{}, fmt.Errorf("scan audit log: %w", err)
	}
	item.ActorID = nullableString(actorID)
	item.IPHash = nullableString(ipHash)
	item.Details = safeAuditDetails(item.Action, item.ResourceType, afterJSON)
	value, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return AuditLog{}, fmt.Errorf("parse audit log time: %w", err)
	}
	item.OccurredAt = value
	return item, nil
}

func safeAuditDetails(action string, resourceType string, afterJSON string) map[string]string {
	if resourceType != "upload_check" || !strings.HasPrefix(action, "upload_check.") {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(afterJSON), &raw); err != nil {
		return nil
	}
	allowed := []string{
		"projectId",
		"assetId",
		"assetVersionId",
		"storageObjectId",
		"uploadSecurityPolicy",
		"status",
		"resultCode",
	}
	details := make(map[string]string)
	for _, key := range allowed {
		if value, ok := auditDetailString(raw[key]); ok {
			details[key] = value
		}
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func auditDetailString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	case *string:
		if typed == nil {
			return "", false
		}
		trimmed := strings.TrimSpace(*typed)
		return trimmed, trimmed != ""
	default:
		return "", false
	}
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
