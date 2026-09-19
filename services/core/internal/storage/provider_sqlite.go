package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (repository *SQLiteRepository) ListProviders(
	ctx context.Context,
	workspaceID string,
) ([]Provider, error) {
	rows, err := repository.db.QueryContext(ctx, providerSelect+`
		WHERE workspace_id = ? AND deleted_at IS NULL
		ORDER BY kind, name
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list storage providers: %w", err)
	}
	defer rows.Close()
	items := make([]Provider, 0)
	for rows.Next() {
		item, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage providers: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Provider(
	ctx context.Context,
	workspaceID string,
	id string,
) (Provider, error) {
	return scanProvider(repository.db.QueryRowContext(ctx, providerSelect+`
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID))
}

func (repository *SQLiteRepository) ResolvedProvider(
	ctx context.Context,
	workspaceID string,
	id string,
) (providerRecord, error) {
	var item providerRecord
	var configJSON, capabilitiesJSON string
	var secretRef sql.NullString
	var createdAt, updatedAt string
	var lastTestAt, lastTestStatus sql.NullString
	var errorCode, errorMessage sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, kind, name, status, config_json, secret_ref,
			capabilities_json, revision, created_at, updated_at,
			last_test_at, last_test_status, last_error_code, last_error_message
		FROM storage_providers
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Kind,
		&item.Name,
		&item.Status,
		&configJSON,
		&secretRef,
		&capabilitiesJSON,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&lastTestAt,
		&lastTestStatus,
		&errorCode,
		&errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return providerRecord{}, ErrProviderNotFound
	}
	if err != nil {
		return providerRecord{}, fmt.Errorf("resolve storage provider: %w", err)
	}
	if err := decodeProviderJSON(
		configJSON,
		capabilitiesJSON,
		&item.Provider,
	); err != nil {
		return providerRecord{}, err
	}
	if secretRef.Valid {
		item.SecretRef = secretRef.String
	}
	if err := parseProviderTimes(
		&item.Provider,
		createdAt,
		updatedAt,
		lastTestAt,
		lastTestStatus,
		errorCode,
		errorMessage,
	); err != nil {
		return providerRecord{}, err
	}
	return item, nil
}

func (repository *SQLiteRepository) CreateProvider(
	ctx context.Context,
	record createProviderRecord,
) (Provider, error) {
	configJSON, capabilitiesJSON, err := encodeProviderJSON(
		record.Config,
		record.Capabilities,
	)
	if err != nil {
		return Provider{}, err
	}
	now := formatDatabaseTime(record.Now)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO storage_providers (
			id, workspace_id, kind, name, status, config_json, secret_ref,
			capabilities_json, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, record.ID, record.WorkspaceID, record.Kind, record.Name, record.Status,
		configJSON, nullableText(record.SecretRef), capabilitiesJSON, now, now)
	if err != nil {
		if isProviderNameConflict(err) {
			return Provider{}, ErrProviderNameConflict
		}
		return Provider{}, fmt.Errorf("create storage provider: %w", err)
	}
	return repository.Provider(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) UpdateProvider(
	ctx context.Context,
	record updateProviderRecord,
) (Provider, error) {
	current, err := repository.ResolvedProvider(
		ctx,
		record.WorkspaceID,
		record.ID,
	)
	if err != nil {
		return Provider{}, err
	}
	configJSON, capabilitiesJSON, err := encodeProviderJSON(
		record.Config,
		record.Capabilities,
	)
	if err != nil {
		return Provider{}, err
	}
	secretRef := current.SecretRef
	if record.SecretRef != nil {
		secretRef = *record.SecretRef
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_providers
		SET name = ?, config_json = ?, secret_ref = ?,
			capabilities_json = ?, status = 'offline',
			revision = revision + 1, updated_at = ?,
			last_error_code = NULL, last_error_message = NULL
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, record.Name, configJSON, nullableText(secretRef), capabilitiesJSON,
		formatDatabaseTime(record.Now), record.ID, record.WorkspaceID,
		record.Revision)
	if err != nil {
		if isProviderNameConflict(err) {
			return Provider{}, ErrProviderNameConflict
		}
		return Provider{}, fmt.Errorf("update storage provider: %w", err)
	}
	if err := requireProviderChanged(
		ctx,
		repository.db,
		result,
		record.WorkspaceID,
		record.ID,
	); err != nil {
		return Provider{}, err
	}
	return repository.Provider(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) UpdateProviderTest(
	ctx context.Context,
	workspaceID string,
	id string,
	status string,
	capabilities map[string]bool,
	errorCode string,
	errorMessage string,
	now time.Time,
) (Provider, error) {
	_, capabilitiesJSON, err := encodeProviderJSON(nil, capabilities)
	if err != nil {
		return Provider{}, err
	}
	testStatus := "succeeded"
	providerStatus := "active"
	if status != "succeeded" {
		testStatus = "failed"
		providerStatus = "error"
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_providers
		SET status = ?, capabilities_json = ?, last_test_at = ?,
			last_test_status = ?, last_error_code = ?,
			last_error_message = ?, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, providerStatus, capabilitiesJSON, formatDatabaseTime(now), testStatus,
		nullableText(errorCode), nullableText(errorMessage),
		formatDatabaseTime(now), id, workspaceID)
	if err != nil {
		return Provider{}, fmt.Errorf("update storage provider test: %w", err)
	}
	if err := requireProviderChanged(
		ctx,
		repository.db,
		result,
		workspaceID,
		id,
	); err != nil {
		return Provider{}, err
	}
	return repository.Provider(ctx, workspaceID, id)
}

func (repository *SQLiteRepository) DeleteProvider(
	ctx context.Context,
	input ProviderStateInput,
	now time.Time,
) error {
	var rootCount int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM authorized_roots
		WHERE workspace_id = ? AND storage_provider_id = ?
			AND deleted_at IS NULL
	`, input.WorkspaceID, input.ID).Scan(&rootCount); err != nil {
		return fmt.Errorf("count provider roots: %w", err)
	}
	if rootCount > 0 {
		return ErrProviderInUse
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE storage_providers
		SET deleted_at = ?, status = 'disabled',
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, formatDatabaseTime(now), formatDatabaseTime(now), input.ID,
		input.WorkspaceID, input.Revision)
	if err != nil {
		return fmt.Errorf("delete storage provider: %w", err)
	}
	return requireProviderChanged(
		ctx,
		repository.db,
		result,
		input.WorkspaceID,
		input.ID,
	)
}

func (repository *SQLiteRepository) RegisterProviderRoot(
	ctx context.Context,
	record registerProviderRootRecord,
) (AuthorizedRoot, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthorizedRoot{}, fmt.Errorf("begin provider root registration: %w", err)
	}
	defer tx.Rollback()
	var providerCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM storage_providers
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
			AND kind IN ('webdav', 's3')
	`, record.ProviderID, record.WorkspaceID).Scan(&providerCount); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("validate root provider: %w", err)
	}
	if providerCount != 1 {
		return AuthorizedRoot{}, ErrProviderNotFound
	}
	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_path_secrets (
			id, workspace_id, path_text, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
	`, record.PathSecretID, record.WorkspaceID, record.BasePath, now, now); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("store provider root prefix: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO authorized_roots (
			id, workspace_id, storage_provider_id, display_name,
			display_path, path_secret_ref, mode, scan_enabled,
			status, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'available', 1, ?, ?)
	`, record.RootID, record.WorkspaceID, record.ProviderID,
		record.DisplayName, displayProviderPath(record.BasePath),
		record.PathSecretID, record.Mode, boolInt(record.ScanEnabled),
		now, now); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("create provider root: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AuthorizedRoot{}, fmt.Errorf("commit provider root: %w", err)
	}
	return repository.Root(ctx, record.WorkspaceID, record.RootID)
}

const providerSelect = `
	SELECT id, workspace_id, kind, name, status, config_json,
		capabilities_json, revision, created_at, updated_at,
		last_test_at, last_test_status, last_error_code, last_error_message
	FROM storage_providers
`

type providerScanner interface {
	Scan(...any) error
}

func scanProvider(scanner providerScanner) (Provider, error) {
	var item Provider
	var configJSON, capabilitiesJSON string
	var createdAt, updatedAt string
	var lastTestAt, lastTestStatus sql.NullString
	var errorCode, errorMessage sql.NullString
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Kind,
		&item.Name,
		&item.Status,
		&configJSON,
		&capabilitiesJSON,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&lastTestAt,
		&lastTestStatus,
		&errorCode,
		&errorMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrProviderNotFound
	}
	if err != nil {
		return Provider{}, fmt.Errorf("scan storage provider: %w", err)
	}
	if err := decodeProviderJSON(
		configJSON,
		capabilitiesJSON,
		&item,
	); err != nil {
		return Provider{}, err
	}
	if err := parseProviderTimes(
		&item,
		createdAt,
		updatedAt,
		lastTestAt,
		lastTestStatus,
		errorCode,
		errorMessage,
	); err != nil {
		return Provider{}, err
	}
	return item, nil
}

func encodeProviderJSON(
	config map[string]string,
	capabilities map[string]bool,
) (string, string, error) {
	if config == nil {
		config = map[string]string{}
	}
	if capabilities == nil {
		capabilities = map[string]bool{}
	}
	configValue, err := json.Marshal(config)
	if err != nil {
		return "", "", fmt.Errorf("encode provider config: %w", err)
	}
	capabilityValue, err := json.Marshal(capabilities)
	if err != nil {
		return "", "", fmt.Errorf("encode provider capabilities: %w", err)
	}
	return string(configValue), string(capabilityValue), nil
}

func decodeProviderJSON(
	configJSON string,
	capabilitiesJSON string,
	item *Provider,
) error {
	if err := json.Unmarshal([]byte(configJSON), &item.Config); err != nil {
		return fmt.Errorf("decode provider config: %w", err)
	}
	if err := json.Unmarshal(
		[]byte(capabilitiesJSON),
		&item.Capabilities,
	); err != nil {
		return fmt.Errorf("decode provider capabilities: %w", err)
	}
	return nil
}

func parseProviderTimes(
	item *Provider,
	createdAt string,
	updatedAt string,
	lastTestAt sql.NullString,
	lastTestStatus sql.NullString,
	errorCode sql.NullString,
	errorMessage sql.NullString,
) error {
	var err error
	item.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return fmt.Errorf("parse provider creation time: %w", err)
	}
	item.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return fmt.Errorf("parse provider update time: %w", err)
	}
	if lastTestAt.Valid {
		value, err := parseDatabaseTime(lastTestAt.String)
		if err != nil {
			return fmt.Errorf("parse provider test time: %w", err)
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

func requireProviderChanged(
	ctx context.Context,
	db *sql.DB,
	result sql.Result,
	workspaceID string,
	id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read provider update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM storage_providers
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("read provider update conflict: %w", err)
	}
	if count == 0 {
		return ErrProviderNotFound
	}
	return ErrRevisionConflict
}

func isProviderNameConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") &&
		strings.Contains(message, "storage_providers")
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func displayProviderPath(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return "/"
	}
	return "/" + value
}
