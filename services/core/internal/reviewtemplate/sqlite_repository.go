package reviewtemplate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func (repository *SQLiteRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Template, error) {
	rows, err := repository.db.QueryContext(ctx, templateSelect+`
		WHERE workspace_id = ? AND deleted_at IS NULL
		ORDER BY updated_at DESC, created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list review templates: %w", err)
	}
	defer rows.Close()
	items := make([]Template, 0)
	for rows.Next() {
		item, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review templates: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) (Template, error) {
	return scanTemplate(repository.db.QueryRowContext(ctx, templateSelect+`
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID))
}

func (repository *SQLiteRepository) Create(
	ctx context.Context,
	item Template,
	now time.Time,
) (Template, error) {
	roles, err := json.Marshal(item.ParticipantRoles)
	if err != nil {
		return Template{}, fmt.Errorf("encode template roles: %w", err)
	}
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO review_templates (
			id, workspace_id, name, description, participant_roles_json,
			allow_download, due_days, decision_rule, created_by, revision,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, item.ID, item.WorkspaceID, item.Name, item.Description, string(roles),
		item.AllowDownload, item.DueDays, item.DecisionRule, item.CreatedBy,
		formatTime(now), formatTime(now))
	if err != nil {
		if isUniqueConstraint(err) {
			return Template{}, ErrNameConflict
		}
		return Template{}, fmt.Errorf("create review template: %w", err)
	}
	return repository.Get(ctx, item.WorkspaceID, item.ID)
}

func (repository *SQLiteRepository) Update(
	ctx context.Context,
	input UpdateInput,
	now time.Time,
) (Template, error) {
	roles, err := json.Marshal(input.ParticipantRoles)
	if err != nil {
		return Template{}, fmt.Errorf("encode template roles: %w", err)
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE review_templates
		SET name = ?, description = ?, participant_roles_json = ?,
			allow_download = ?, due_days = ?, decision_rule = ?,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, input.Name, input.Description, string(roles), input.AllowDownload,
		input.DueDays, input.DecisionRule, formatTime(now), input.ID,
		input.WorkspaceID, input.Revision)
	if err != nil {
		if isUniqueConstraint(err) {
			return Template{}, ErrNameConflict
		}
		return Template{}, fmt.Errorf("update review template: %w", err)
	}
	if err := requireTemplateChanged(
		ctx,
		repository.db,
		result,
		input.WorkspaceID,
		input.ID,
	); err != nil {
		return Template{}, err
	}
	return repository.Get(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) Delete(
	ctx context.Context,
	input DeleteInput,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE review_templates
		SET deleted_at = ?, updated_at = ?, revision = revision + 1
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND deleted_at IS NULL
	`, formatTime(now), formatTime(now), input.ID, input.WorkspaceID,
		input.Revision)
	if err != nil {
		return fmt.Errorf("delete review template: %w", err)
	}
	return requireTemplateChanged(
		ctx,
		repository.db,
		result,
		input.WorkspaceID,
		input.ID,
	)
}

const templateSelect = `
	SELECT id, workspace_id, name, description, participant_roles_json,
		allow_download, due_days, decision_rule, created_by, revision,
		created_at, updated_at
	FROM review_templates
`

type scanner interface {
	Scan(...any) error
}

func scanTemplate(row scanner) (Template, error) {
	var item Template
	var description sql.NullString
	var rolesJSON string
	var allowDownload bool
	var dueDays sql.NullInt64
	var createdAt, updatedAt string
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.Name, &description, &rolesJSON,
		&allowDownload, &dueDays, &item.DecisionRule, &item.CreatedBy,
		&item.Revision, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("scan review template: %w", err)
	}
	if description.Valid {
		item.Description = &description.String
	}
	if err := json.Unmarshal([]byte(rolesJSON), &item.ParticipantRoles); err != nil {
		return Template{}, fmt.Errorf("decode template roles: %w", err)
	}
	item.AllowDownload = allowDownload
	if dueDays.Valid {
		value := int(dueDays.Int64)
		item.DueDays = &value
	}
	var parseErr error
	item.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return Template{}, parseErr
	}
	item.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return Template{}, parseErr
	}
	return item, nil
}

func requireTemplateChanged(
	ctx context.Context,
	db *sql.DB,
	result sql.Result,
	workspaceID string,
	id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read template update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM review_templates
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, id, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("read template update conflict: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return ErrRevisionConflict
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse review template time: %w", err)
	}
	return result, nil
}
