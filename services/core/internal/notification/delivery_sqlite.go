package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (repository *SQLiteRepository) DeliveryTargets(
	ctx context.Context,
	message OutboxMessage,
) ([]DeliveryTarget, error) {
	payload, err := parseOutboxPayload(message)
	if err != nil {
		return nil, err
	}
	if payload.RecipientUserID == "" {
		return nil, nil
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT c.id, c.workspace_id, c.kind, c.name, c.status, c.config_json,
			c.secret_ref, c.revision, c.created_at, c.updated_at,
			c.last_test_at, c.last_test_status, c.last_error_code,
			c.last_error_message, users.email, users.display_name,
			COALESCE(p.enabled, 1)
		FROM notification_channels c
		JOIN users ON users.id = ?
		LEFT JOIN notification_preferences p
			ON p.workspace_id = c.workspace_id
			AND p.user_id = users.id
			AND p.channel_kind = c.kind
		WHERE c.workspace_id = ?
			AND c.status = 'active'
			AND c.deleted_at IS NULL
		ORDER BY c.kind, c.name
	`, payload.RecipientUserID, message.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list notification delivery targets: %w", err)
	}
	defer rows.Close()
	targets := make([]DeliveryTarget, 0)
	for rows.Next() {
		var target DeliveryTarget
		var configJSON, createdAt, updatedAt string
		var secretRef, email sql.NullString
		var lastTestAt, lastTestStatus sql.NullString
		var errorCode, errorMessage sql.NullString
		var enabled int
		if err := rows.Scan(
			&target.Channel.ID,
			&target.Channel.WorkspaceID,
			&target.Channel.Kind,
			&target.Channel.Name,
			&target.Channel.Status,
			&configJSON,
			&secretRef,
			&target.Channel.Revision,
			&createdAt,
			&updatedAt,
			&lastTestAt,
			&lastTestStatus,
			&errorCode,
			&errorMessage,
			&email,
			&target.RecipientName,
			&enabled,
		); err != nil {
			return nil, fmt.Errorf("scan notification delivery target: %w", err)
		}
		if enabled != 1 {
			continue
		}
		if err := decodeChannelJSON(configJSON, &target.Channel); err != nil {
			return nil, err
		}
		if err := parseChannelTimes(
			&target.Channel,
			createdAt,
			updatedAt,
			lastTestAt,
			lastTestStatus,
			errorCode,
			errorMessage,
		); err != nil {
			return nil, err
		}
		if secretRef.Valid {
			target.SecretRef = secretRef.String
		}
		target.RecipientUserID = payload.RecipientUserID
		if email.Valid {
			target.RecipientEmail = &email.String
		}
		if target.Channel.Kind == "email" && target.RecipientEmail == nil {
			continue
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification delivery targets: %w", err)
	}
	return targets, nil
}

func (repository *SQLiteRepository) UpsertDelivery(
	ctx context.Context,
	record createDeliveryRecord,
) error {
	now := formatTime(record.Now)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO notification_deliveries (
			id, workspace_id, outbox_id, notification_id, recipient_user_id,
			channel_id, channel_kind, idempotency_key, status, attempts,
			max_attempts, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'queued', 0, ?, ?, ?)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
	`, record.ID, record.WorkspaceID, record.OutboxID, record.NotificationID,
		record.RecipientUserID, record.ChannelID, record.ChannelKind,
		record.IdempotencyKey, record.MaxAttempts, now, now)
	if err != nil {
		return fmt.Errorf("upsert notification delivery: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ListDeliveriesForOutbox(
	ctx context.Context,
	outboxID string,
) ([]Delivery, error) {
	rows, err := repository.db.QueryContext(ctx, deliverySelect+`
		WHERE d.outbox_id = ?
		ORDER BY d.created_at
	`, outboxID)
	if err != nil {
		return nil, fmt.Errorf("list outbox deliveries: %w", err)
	}
	defer rows.Close()
	return scanDeliveries(rows)
}

func (repository *SQLiteRepository) ListDeliveries(
	ctx context.Context,
	workspaceID string,
	limit int,
) ([]Delivery, error) {
	rows, err := repository.db.QueryContext(ctx, deliverySelect+`
		WHERE d.workspace_id = ?
		ORDER BY d.updated_at DESC
		LIMIT ?
	`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notification deliveries: %w", err)
	}
	defer rows.Close()
	return scanDeliveries(rows)
}

func (repository *SQLiteRepository) StartDelivery(
	ctx context.Context,
	id string,
	now time.Time,
) (Delivery, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET status = 'sending', attempts = attempts + 1, updated_at = ?
		WHERE id = ?
			AND status IN ('queued', 'failed', 'sending')
			AND attempts < max_attempts
	`, formatTime(now), id)
	if err != nil {
		return Delivery{}, fmt.Errorf("start notification delivery: %w", err)
	}
	if err := requireDeliveryChanged(ctx, repository.db, result, id); err != nil {
		return Delivery{}, err
	}
	return repository.deliveryByID(ctx, id)
}

func (repository *SQLiteRepository) CompleteDelivery(
	ctx context.Context,
	id string,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET status = 'succeeded', last_error_code = NULL,
			last_error_message = NULL, delivered_at = ?, updated_at = ?
		WHERE id = ? AND status = 'sending'
	`, formatTime(now), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("complete notification delivery: %w", err)
	}
	return requireDeliveryChanged(ctx, repository.db, result, id)
}

func (repository *SQLiteRepository) FailDeliveryRecord(
	ctx context.Context,
	id string,
	deliveryError DeliveryError,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET status = 'failed', last_error_code = ?,
			last_error_message = ?, updated_at = ?
		WHERE id = ? AND status = 'sending'
	`, deliveryError.Code, deliveryError.Message, formatTime(now), id)
	if err != nil {
		return fmt.Errorf("fail notification delivery: %w", err)
	}
	return requireDeliveryChanged(ctx, repository.db, result, id)
}

func (repository *SQLiteRepository) RetryDelivery(
	ctx context.Context,
	workspaceID string,
	id string,
	now time.Time,
) (Delivery, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, fmt.Errorf("begin notification delivery retry: %w", err)
	}
	defer tx.Rollback()
	var outboxID, status string
	err = tx.QueryRowContext(ctx, `
		SELECT outbox_id, status
		FROM notification_deliveries
		WHERE id = ? AND workspace_id = ?
	`, id, workspaceID).Scan(&outboxID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrDeliveryNotFound
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("read notification delivery retry: %w", err)
	}
	if status != "failed" {
		return Delivery{}, ErrDeliveryNotRetryable
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET status = 'queued', last_error_code = NULL,
			last_error_message = NULL, updated_at = ?
		WHERE id = ? AND workspace_id = ?
	`, formatTime(now), id, workspaceID); err != nil {
		return Delivery{}, fmt.Errorf("reset notification delivery: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notification_outbox
		SET status = 'pending', attempts = 0, available_at = ?,
			locked_at = NULL, last_error_code = NULL,
			last_error_message = NULL, updated_at = ?
		WHERE id = ? AND workspace_id = ?
	`, formatTime(now), formatTime(now), outboxID, workspaceID); err != nil {
		return Delivery{}, fmt.Errorf("reset notification outbox: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Delivery{}, fmt.Errorf("commit notification delivery retry: %w", err)
	}
	return repository.deliveryByID(ctx, id)
}

func (repository *SQLiteRepository) deliveryByID(
	ctx context.Context,
	id string,
) (Delivery, error) {
	return scanDelivery(repository.db.QueryRowContext(ctx, deliverySelect+`
		WHERE d.id = ?
	`, id))
}

const deliverySelect = `
	SELECT d.id, d.workspace_id, d.outbox_id, d.notification_id,
		d.recipient_user_id, d.channel_id, d.channel_kind,
		COALESCE(c.name, ''), d.idempotency_key, d.status, d.attempts,
		d.max_attempts, d.last_error_code, d.last_error_message,
		d.created_at, d.updated_at, d.delivered_at
	FROM notification_deliveries d
	LEFT JOIN notification_channels c ON c.id = d.channel_id
`

func scanDeliveries(rows *sql.Rows) ([]Delivery, error) {
	items := make([]Delivery, 0)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification deliveries: %w", err)
	}
	return items, nil
}

func scanDelivery(row scanner) (Delivery, error) {
	var item Delivery
	var notificationID, recipientUserID, channelID sql.NullString
	var errorCode, errorMessage, deliveredAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.OutboxID,
		&notificationID,
		&recipientUserID,
		&channelID,
		&item.ChannelKind,
		&item.ChannelName,
		&item.IdempotencyKey,
		&item.Status,
		&item.Attempts,
		&item.MaxAttempts,
		&errorCode,
		&errorMessage,
		&createdAt,
		&updatedAt,
		&deliveredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrDeliveryNotFound
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("scan notification delivery: %w", err)
	}
	item.NotificationID = nullableValue(notificationID)
	item.RecipientUserID = nullableValue(recipientUserID)
	item.ChannelID = nullableValue(channelID)
	item.LastErrorCode = nullableValue(errorCode)
	item.LastError = nullableValue(errorMessage)
	item.DeliveredAt = nullableTime(deliveredAt)
	var parseErr error
	item.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdAt)
	if parseErr != nil {
		return Delivery{}, fmt.Errorf("parse notification delivery creation time: %w", parseErr)
	}
	item.UpdatedAt, parseErr = time.Parse(time.RFC3339Nano, updatedAt)
	if parseErr != nil {
		return Delivery{}, fmt.Errorf("parse notification delivery update time: %w", parseErr)
	}
	return item, nil
}

func requireDeliveryChanged(
	ctx context.Context,
	db *sql.DB,
	result sql.Result,
	id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read notification delivery update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM notification_deliveries
		WHERE id = ?
	`, id).Scan(&count); err != nil {
		return fmt.Errorf("read notification delivery update conflict: %w", err)
	}
	if count == 0 {
		return ErrDeliveryNotFound
	}
	return ErrDeliveryNotRetryable
}
