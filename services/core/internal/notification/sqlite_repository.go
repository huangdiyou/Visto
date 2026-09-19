package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SQLiteRepository struct {
	db *sql.DB
}

func NewSQLiteRepository(db *sql.DB) *SQLiteRepository {
	return &SQLiteRepository{db: db}
}

func (repository *SQLiteRepository) WorkspaceManagerIDs(
	ctx context.Context,
	workspaceID string,
	excludeUserID string,
) ([]string, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT user_id
		FROM memberships
		WHERE workspace_id = ? AND status = 'active'
			AND role_key IN ('owner', 'admin')
			AND (? = '' OR user_id <> ?)
		ORDER BY created_at
	`, workspaceID, excludeUserID, excludeUserID)
	if err != nil {
		return nil, fmt.Errorf("list notification recipients: %w", err)
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan notification recipient: %w", err)
		}
		items = append(items, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification recipients: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) CreateMany(
	ctx context.Context,
	items []Notification,
	outbox []OutboxMessage,
) error {
	if len(items) == 0 && len(outbox) == 0 {
		return nil
	}
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notifications: %w", err)
	}
	defer tx.Rollback()
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notifications (
				id, workspace_id, recipient_user_id, type, resource_type,
				resource_id, title, body, read_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		`, item.ID, item.WorkspaceID, item.RecipientUserID, item.Type,
			item.ResourceType, item.ResourceID, item.Title, item.Body,
			formatTime(item.CreatedAt)); err != nil {
			return fmt.Errorf("create notification: %w", err)
		}
	}
	for _, item := range outbox {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_outbox (
				id, workspace_id, notification_id, event_type, payload_json,
				idempotency_key, status, attempts, max_attempts, available_at,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		`, item.ID, item.WorkspaceID, item.NotificationID, item.EventType,
			string(item.Payload), item.IdempotencyKey, item.MaxAttempts,
			formatTime(item.AvailableAt), formatTime(item.CreatedAt),
			formatTime(item.UpdatedAt)); err != nil {
			return fmt.Errorf("create notification outbox message: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notifications: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ClaimOutbox(
	ctx context.Context,
	now time.Time,
	leaseDuration time.Duration,
) (OutboxMessage, bool, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, false, fmt.Errorf("begin outbox claim: %w", err)
	}
	defer tx.Rollback()
	expiredBefore := now.Add(-leaseDuration)
	item, err := scanOutbox(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, notification_id, event_type, payload_json,
			idempotency_key, status, attempts, max_attempts, available_at,
			locked_at, last_error_code, last_error_message, created_at,
			updated_at, delivered_at
		FROM notification_outbox
		WHERE attempts < max_attempts
			AND (
				(status = 'pending' AND available_at <= ?)
				OR (status = 'processing' AND locked_at <= ?)
			)
		ORDER BY available_at, created_at
		LIMIT 1
	`, formatTime(now), formatTime(expiredBefore)))
	if errors.Is(err, ErrNotFound) {
		return OutboxMessage{}, false, nil
	}
	if err != nil {
		return OutboxMessage{}, false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status = 'processing', attempts = attempts + 1,
			locked_at = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, formatTime(now), formatTime(now), item.ID, item.Status)
	if err != nil {
		return OutboxMessage{}, false, fmt.Errorf("claim outbox message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return OutboxMessage{}, false, fmt.Errorf("read outbox claim result: %w", err)
	}
	if affected != 1 {
		return OutboxMessage{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, false, fmt.Errorf("commit outbox claim: %w", err)
	}
	item.Status = "processing"
	item.Attempts++
	item.LockedAt = &now
	item.UpdatedAt = now
	return item, true, nil
}

func (repository *SQLiteRepository) CompleteOutbox(
	ctx context.Context,
	id string,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status = 'delivered', delivered_at = ?, locked_at = NULL,
			last_error_code = NULL, last_error_message = NULL, updated_at = ?
		WHERE id = ? AND status = 'processing'
	`, formatTime(now), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("complete outbox message: %w", err)
	}
	return requireAffected(result)
}

func (repository *SQLiteRepository) FailOutbox(
	ctx context.Context,
	id string,
	deliveryError DeliveryError,
	availableAt time.Time,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
			available_at = ?, locked_at = NULL, last_error_code = ?,
			last_error_message = ?, updated_at = ?
		WHERE id = ? AND status = 'processing'
	`, formatTime(availableAt), deliveryError.Code, deliveryError.Message,
		formatTime(now), id)
	if err != nil {
		return fmt.Errorf("fail outbox message: %w", err)
	}
	return requireAffected(result)
}

func (repository *SQLiteRepository) List(
	ctx context.Context,
	workspaceID string,
	userID string,
	unreadOnly bool,
	limit int,
) ([]Notification, int, error) {
	var unreadCount int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM notifications
		WHERE workspace_id = ? AND recipient_user_id = ? AND read_at IS NULL
	`, workspaceID, userID).Scan(&unreadCount); err != nil {
		return nil, 0, fmt.Errorf("count unread notifications: %w", err)
	}
	query := `
		SELECT id, workspace_id, recipient_user_id, type, resource_type,
			resource_id, title, body, read_at, created_at
		FROM notifications
		WHERE workspace_id = ? AND recipient_user_id = ?`
	if unreadOnly {
		query += " AND read_at IS NULL"
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	rows, err := repository.db.QueryContext(ctx, query, workspaceID, userID, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	items := make([]Notification, 0)
	for rows.Next() {
		item, err := scanNotification(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate notifications: %w", err)
	}
	return items, unreadCount, nil
}

func (repository *SQLiteRepository) MarkRead(
	ctx context.Context,
	workspaceID string,
	userID string,
	id string,
	now time.Time,
) (Notification, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notifications
		SET read_at = COALESCE(read_at, ?)
		WHERE id = ? AND workspace_id = ? AND recipient_user_id = ?
	`, formatTime(now), id, workspaceID, userID)
	if err != nil {
		return Notification{}, fmt.Errorf("mark notification read: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Notification{}, fmt.Errorf("read notification update result: %w", err)
	}
	if affected == 0 {
		return Notification{}, ErrNotFound
	}
	item, err := scanNotification(repository.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, recipient_user_id, type, resource_type,
			resource_id, title, body, read_at, created_at
		FROM notifications
		WHERE id = ? AND workspace_id = ? AND recipient_user_id = ?
	`, id, workspaceID, userID))
	if err != nil {
		return Notification{}, err
	}
	return item, nil
}

func (repository *SQLiteRepository) MarkAllRead(
	ctx context.Context,
	workspaceID string,
	userID string,
	now time.Time,
) error {
	_, err := repository.db.ExecContext(ctx, `
		UPDATE notifications
		SET read_at = ?
		WHERE workspace_id = ? AND recipient_user_id = ? AND read_at IS NULL
	`, formatTime(now), workspaceID, userID)
	if err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(...any) error
}

func scanNotification(row scanner) (Notification, error) {
	var item Notification
	var readAt sql.NullString
	var createdAt string
	if err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.RecipientUserID, &item.Type,
		&item.ResourceType, &item.ResourceID, &item.Title, &item.Body,
		&readAt, &createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, ErrNotFound
		}
		return Notification{}, fmt.Errorf("scan notification: %w", err)
	}
	if readAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, readAt.String)
		if err != nil {
			return Notification{}, fmt.Errorf("parse notification read time: %w", err)
		}
		item.ReadAt = &value
	}
	value, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Notification{}, fmt.Errorf("parse notification create time: %w", err)
	}
	item.CreatedAt = value
	return item, nil
}

func scanOutbox(row scanner) (OutboxMessage, error) {
	var item OutboxMessage
	var notificationID, lockedAt, lastErrorCode, lastErrorMessage sql.NullString
	var payload string
	var availableAt, createdAt, updatedAt string
	var deliveredAt sql.NullString
	if err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&notificationID,
		&item.EventType,
		&payload,
		&item.IdempotencyKey,
		&item.Status,
		&item.Attempts,
		&item.MaxAttempts,
		&availableAt,
		&lockedAt,
		&lastErrorCode,
		&lastErrorMessage,
		&createdAt,
		&updatedAt,
		&deliveredAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OutboxMessage{}, ErrNotFound
		}
		return OutboxMessage{}, fmt.Errorf("scan outbox message: %w", err)
	}
	item.Payload = []byte(payload)
	item.NotificationID = nullableValue(notificationID)
	item.LockedAt = nullableTime(lockedAt)
	item.LastErrorCode = nullableValue(lastErrorCode)
	item.LastError = nullableValue(lastErrorMessage)
	item.DeliveredAt = nullableTime(deliveredAt)
	var err error
	item.AvailableAt, err = time.Parse(time.RFC3339Nano, availableAt)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("parse outbox availability: %w", err)
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("parse outbox creation: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("parse outbox update: %w", err)
	}
	return item, nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read outbox update result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func nullableValue(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
