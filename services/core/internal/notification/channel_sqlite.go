package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (repository *SQLiteRepository) ListChannels(
	ctx context.Context,
	workspaceID string,
) ([]Channel, error) {
	rows, err := repository.db.QueryContext(ctx, channelSelect+`
		WHERE workspace_id = ? AND deleted_at IS NULL
		ORDER BY kind, name
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()
	items := make([]Channel, 0)
	for rows.Next() {
		item, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification channels: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Channel(
	ctx context.Context,
	workspaceID string,
	id string,
) (Channel, error) {
	return scanChannel(repository.db.QueryRowContext(ctx, channelSelect+`
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID))
}

func (repository *SQLiteRepository) ResolvedChannel(
	ctx context.Context,
	workspaceID string,
	id string,
) (channelRecord, error) {
	var item channelRecord
	var configJSON, createdAt, updatedAt string
	var secretRef sql.NullString
	var lastTestAt, lastTestStatus sql.NullString
	var errorCode, errorMessage sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, kind, name, status, config_json, secret_ref,
			revision, created_at, updated_at, last_test_at, last_test_status,
			last_error_code, last_error_message
		FROM notification_channels
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Kind,
		&item.Name,
		&item.Status,
		&configJSON,
		&secretRef,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&lastTestAt,
		&lastTestStatus,
		&errorCode,
		&errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return channelRecord{}, ErrChannelNotFound
	}
	if err != nil {
		return channelRecord{}, fmt.Errorf("resolve notification channel: %w", err)
	}
	if err := decodeChannelJSON(configJSON, &item.Channel); err != nil {
		return channelRecord{}, err
	}
	if secretRef.Valid {
		item.SecretRef = secretRef.String
	}
	if err := parseChannelTimes(
		&item.Channel,
		createdAt,
		updatedAt,
		lastTestAt,
		lastTestStatus,
		errorCode,
		errorMessage,
	); err != nil {
		return channelRecord{}, err
	}
	return item, nil
}

func (repository *SQLiteRepository) CreateChannel(
	ctx context.Context,
	record createChannelRecord,
) (Channel, error) {
	configJSON, err := encodeChannelJSON(record.Config)
	if err != nil {
		return Channel{}, err
	}
	now := formatTime(record.Now)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO notification_channels (
			id, workspace_id, kind, name, status, config_json, secret_ref,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, record.ID, record.WorkspaceID, record.Kind, record.Name, record.Status,
		configJSON, nullableDatabaseText(record.SecretRef), now, now)
	if err != nil {
		if isChannelNameConflict(err) {
			return Channel{}, ErrChannelNameConflict
		}
		return Channel{}, fmt.Errorf("create notification channel: %w", err)
	}
	return repository.Channel(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) UpdateChannel(
	ctx context.Context,
	record updateChannelRecord,
) (Channel, error) {
	current, err := repository.ResolvedChannel(
		ctx,
		record.WorkspaceID,
		record.ID,
	)
	if err != nil {
		return Channel{}, err
	}
	configJSON, err := encodeChannelJSON(record.Config)
	if err != nil {
		return Channel{}, err
	}
	secretRef := current.SecretRef
	if record.SecretRef != nil {
		secretRef = *record.SecretRef
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_channels
		SET name = ?, config_json = ?, secret_ref = ?, status = 'disabled',
			revision = revision + 1, updated_at = ?,
			last_error_code = NULL, last_error_message = NULL
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, record.Name, configJSON, nullableDatabaseText(secretRef),
		formatTime(record.Now), record.ID, record.WorkspaceID, record.Revision)
	if err != nil {
		if isChannelNameConflict(err) {
			return Channel{}, ErrChannelNameConflict
		}
		return Channel{}, fmt.Errorf("update notification channel: %w", err)
	}
	if err := requireChannelChanged(
		ctx,
		repository.db,
		result,
		record.WorkspaceID,
		record.ID,
	); err != nil {
		return Channel{}, err
	}
	return repository.Channel(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) UpdateChannelTest(
	ctx context.Context,
	workspaceID string,
	id string,
	status string,
	errorCode string,
	errorMessage string,
	now time.Time,
) (Channel, error) {
	testStatus := "succeeded"
	channelStatus := "active"
	if status != "succeeded" {
		testStatus = "failed"
		channelStatus = "error"
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_channels
		SET status = ?, last_test_at = ?, last_test_status = ?,
			last_error_code = ?, last_error_message = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, channelStatus, formatTime(now), testStatus,
		nullableDatabaseText(errorCode), nullableDatabaseText(errorMessage),
		formatTime(now), id, workspaceID)
	if err != nil {
		return Channel{}, fmt.Errorf("update notification channel test: %w", err)
	}
	if err := requireChannelChanged(
		ctx,
		repository.db,
		result,
		workspaceID,
		id,
	); err != nil {
		return Channel{}, err
	}
	return repository.Channel(ctx, workspaceID, id)
}

func (repository *SQLiteRepository) DeleteChannel(
	ctx context.Context,
	input ChannelStateInput,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE notification_channels
		SET deleted_at = ?, status = 'disabled',
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, formatTime(now), formatTime(now), input.ID, input.WorkspaceID,
		input.Revision)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	return requireChannelChanged(
		ctx,
		repository.db,
		result,
		input.WorkspaceID,
		input.ID,
	)
}

func (repository *SQLiteRepository) Preferences(
	ctx context.Context,
	workspaceID string,
	userID string,
) (Preferences, error) {
	var memberCount int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memberships
		WHERE workspace_id = ? AND user_id = ? AND status = 'active'
	`, workspaceID, userID).Scan(&memberCount); err != nil {
		return Preferences{}, fmt.Errorf("validate notification preferences user: %w", err)
	}
	if memberCount != 1 {
		return Preferences{}, ErrNotFound
	}
	result := Preferences{
		UserID:            userID,
		EmailEnabled:      true,
		FeishuEnabled:     true,
		WechatWorkEnabled: true,
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT channel_kind, enabled, updated_at
		FROM notification_preferences
		WHERE workspace_id = ? AND user_id = ?
	`, workspaceID, userID)
	if err != nil {
		return Preferences{}, fmt.Errorf("read notification preferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, updatedAt string
		var enabled int
		if err := rows.Scan(&kind, &enabled, &updatedAt); err != nil {
			return Preferences{}, fmt.Errorf("scan notification preference: %w", err)
		}
		switch kind {
		case "email":
			result.EmailEnabled = enabled == 1
		case "feishu":
			result.FeishuEnabled = enabled == 1
		case "wechat_work":
			result.WechatWorkEnabled = enabled == 1
		}
		parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
		if err == nil && parsed.After(result.UpdatedAt) {
			result.UpdatedAt = parsed
		}
	}
	if err := rows.Err(); err != nil {
		return Preferences{}, fmt.Errorf("iterate notification preferences: %w", err)
	}
	return result, nil
}

func (repository *SQLiteRepository) SetPreferences(
	ctx context.Context,
	input PreferenceInput,
	now time.Time,
) (Preferences, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Preferences{}, fmt.Errorf("begin notification preferences: %w", err)
	}
	defer tx.Rollback()
	var memberCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM memberships
		WHERE workspace_id = ? AND user_id = ? AND status = 'active'
	`, input.WorkspaceID, input.UserID).Scan(&memberCount); err != nil {
		return Preferences{}, fmt.Errorf("validate notification preferences user: %w", err)
	}
	if memberCount != 1 {
		return Preferences{}, ErrNotFound
	}
	values := map[string]bool{
		"email":       input.EmailEnabled,
		"feishu":      input.FeishuEnabled,
		"wechat_work": input.WechatWorkEnabled,
	}
	for kind, enabled := range values {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_preferences (
				workspace_id, user_id, channel_kind, enabled, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, user_id, channel_kind) DO UPDATE SET
				enabled = excluded.enabled,
				updated_at = excluded.updated_at
		`, input.WorkspaceID, input.UserID, kind, boolInt(enabled),
			formatTime(now), formatTime(now)); err != nil {
			return Preferences{}, fmt.Errorf("upsert notification preference: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Preferences{}, fmt.Errorf("commit notification preferences: %w", err)
	}
	return repository.Preferences(ctx, input.WorkspaceID, input.UserID)
}

const channelSelect = `
	SELECT id, workspace_id, kind, name, status, config_json, revision,
		created_at, updated_at, last_test_at, last_test_status,
		last_error_code, last_error_message
	FROM notification_channels
`

func scanChannel(row scanner) (Channel, error) {
	var item Channel
	var configJSON, createdAt, updatedAt string
	var lastTestAt, lastTestStatus sql.NullString
	var errorCode, errorMessage sql.NullString
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Kind,
		&item.Name,
		&item.Status,
		&configJSON,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&lastTestAt,
		&lastTestStatus,
		&errorCode,
		&errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrChannelNotFound
	}
	if err != nil {
		return Channel{}, fmt.Errorf("scan notification channel: %w", err)
	}
	if err := decodeChannelJSON(configJSON, &item); err != nil {
		return Channel{}, err
	}
	if err := parseChannelTimes(
		&item,
		createdAt,
		updatedAt,
		lastTestAt,
		lastTestStatus,
		errorCode,
		errorMessage,
	); err != nil {
		return Channel{}, err
	}
	return item, nil
}

func encodeChannelJSON(config map[string]string) (string, error) {
	if config == nil {
		config = map[string]string{}
	}
	value, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode notification channel config: %w", err)
	}
	return string(value), nil
}

func decodeChannelJSON(configJSON string, item *Channel) error {
	if err := json.Unmarshal([]byte(configJSON), &item.Config); err != nil {
		return fmt.Errorf("decode notification channel config: %w", err)
	}
	return nil
}

func parseChannelTimes(
	item *Channel,
	createdAt string,
	updatedAt string,
	lastTestAt sql.NullString,
	lastTestStatus sql.NullString,
	errorCode sql.NullString,
	errorMessage sql.NullString,
) error {
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return fmt.Errorf("parse notification channel creation time: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return fmt.Errorf("parse notification channel update time: %w", err)
	}
	if lastTestAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, lastTestAt.String)
		if err != nil {
			return fmt.Errorf("parse notification channel test time: %w", err)
		}
		item.LastTestAt = &value
	}
	if lastTestStatus.Valid {
		item.LastTestStatus = &lastTestStatus.String
	}
	if errorCode.Valid {
		item.LastErrorCode = &errorCode.String
	}
	if errorMessage.Valid {
		item.LastErrorMessage = &errorMessage.String
	}
	return nil
}

func requireChannelChanged(
	ctx context.Context,
	db *sql.DB,
	result sql.Result,
	workspaceID string,
	id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read notification channel update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM notification_channels
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("read notification channel update conflict: %w", err)
	}
	if count == 0 {
		return ErrChannelNotFound
	}
	return ErrRevisionConflict
}

func isChannelNameConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") &&
		strings.Contains(message, "notification_channels")
}

func nullableDatabaseText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
