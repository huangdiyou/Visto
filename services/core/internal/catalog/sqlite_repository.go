package catalog

import (
	"context"
	"database/sql"
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

func (repository *SQLiteRepository) ListProjects(
	ctx context.Context,
	input ListProjectsInput,
	now time.Time,
) ([]Project, error) {
	includeAll := 0
	if strings.EqualFold(input.WorkspaceRole, "owner") {
		includeAll = 1
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			projects.id,
			projects.workspace_id,
			projects.primary_owner_user_id,
			projects.name,
			projects.description,
			projects.status,
			(
				SELECT COUNT(*)
				FROM assets
				WHERE assets.project_id = projects.id
					AND assets.deleted_at IS NULL
			),
			(
				SELECT COUNT(*)
				FROM collections
				WHERE collections.project_id = projects.id
					AND collections.deleted_at IS NULL
			),
			projects.revision,
			projects.created_at,
			projects.updated_at,
			projects.archived_at
		FROM projects
		WHERE projects.workspace_id = ?
			AND projects.deleted_at IS NULL
			AND (
				? = 1 OR EXISTS (
					SELECT 1
					FROM project_memberships
					WHERE project_memberships.workspace_id = projects.workspace_id
						AND project_memberships.project_id = projects.id
						AND project_memberships.user_id = ?
						AND project_memberships.status = 'active'
						AND (
							project_memberships.expires_at IS NULL
							OR project_memberships.expires_at > ?
						)
				)
			)
		ORDER BY
			CASE projects.status WHEN 'active' THEN 0 ELSE 1 END,
			projects.updated_at DESC,
			projects.name
	`,
		input.WorkspaceID,
		includeAll,
		input.UserID,
		formatDatabaseTime(now),
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}

	return projects, nil
}

func (repository *SQLiteRepository) Project(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (Project, error) {
	return repository.project(ctx, repository.db, workspaceID, projectID)
}

func (repository *SQLiteRepository) ProjectOverview(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (ProjectOverview, error) {
	var overview ProjectOverview
	var err error

	overview.Reviews.Total, err = repository.countProjectOverviewReviews(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.Reviews.Items, err = repository.listProjectOverviewReviews(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.OpenFeedback.Total, err = repository.countProjectOverviewFeedback(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.OpenFeedback.Items, err = repository.listProjectOverviewFeedback(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.RecentVersions.Total, err = repository.countProjectOverviewVersions(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.RecentVersions.Items, err = repository.listProjectOverviewVersions(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.FailedTasks.Total, err = repository.countProjectOverviewFailedTasks(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}
	overview.FailedTasks.Items, err = repository.listProjectOverviewFailedTasks(
		ctx,
		workspaceID,
		projectID,
	)
	if err != nil {
		return ProjectOverview{}, err
	}

	return overview, nil
}

func (repository *SQLiteRepository) countProjectOverviewReviews(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (int, error) {
	var total int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM review_sessions
		WHERE workspace_id = ?
			AND project_id = ?
			AND status IN ('changes_requested', 'open', 'draft')
	`, workspaceID, projectID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count project overview reviews: %w", err)
	}
	return total, nil
}

func (repository *SQLiteRepository) listProjectOverviewReviews(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]ProjectOverviewReview, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			review_sessions.id,
			review_sessions.name,
			review_sessions.status,
			review_sessions.due_at,
			COALESCE(responsible.display_name, ''),
			COALESCE(assets.name, ''),
			COALESCE(asset_versions.version_number, 0),
			(
				SELECT COUNT(*)
				FROM review_items
				WHERE review_items.review_session_id = review_sessions.id
			),
			(
				SELECT COUNT(*)
				FROM comment_threads
				WHERE comment_threads.review_session_id = review_sessions.id
					AND comment_threads.status = 'open'
					AND comment_threads.deleted_at IS NULL
			),
			review_sessions.updated_at
		FROM review_sessions
		LEFT JOIN users responsible
			ON responsible.id = review_sessions.responsible_user_id
		LEFT JOIN review_items
			ON review_items.id = (
				SELECT first_item.id
				FROM review_items first_item
				WHERE first_item.review_session_id = review_sessions.id
				ORDER BY first_item.position
				LIMIT 1
			)
		LEFT JOIN assets ON assets.id = review_items.asset_id
		LEFT JOIN asset_versions ON asset_versions.id = review_items.asset_version_id
		WHERE review_sessions.workspace_id = ?
			AND review_sessions.project_id = ?
			AND review_sessions.status IN ('changes_requested', 'open', 'draft')
		ORDER BY
			CASE review_sessions.status
				WHEN 'changes_requested' THEN 0
				WHEN 'open' THEN 1
				ELSE 2
			END,
			CASE WHEN review_sessions.due_at IS NULL THEN 1 ELSE 0 END,
			review_sessions.due_at,
			review_sessions.updated_at DESC
		LIMIT 6
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project overview reviews: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectOverviewReview, 0, 6)
	for rows.Next() {
		var item ProjectOverviewReview
		var dueAt sql.NullString
		var updatedAt string
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Status,
			&dueAt,
			&item.ResponsibleName,
			&item.AssetName,
			&item.VersionNumber,
			&item.ItemCount,
			&item.OpenFeedbackCount,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project overview review: %w", err)
		}
		item.DueAt, err = nullableTime(dueAt)
		if err != nil {
			return nil, fmt.Errorf("parse project overview review due time: %w", err)
		}
		item.UpdatedAt, err = parseDatabaseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse project overview review update time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project overview reviews: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) countProjectOverviewFeedback(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (int, error) {
	var total int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM comment_threads
		JOIN review_sessions
			ON review_sessions.id = comment_threads.review_session_id
		WHERE comment_threads.workspace_id = ?
			AND review_sessions.project_id = ?
			AND comment_threads.status = 'open'
			AND comment_threads.deleted_at IS NULL
	`, workspaceID, projectID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count project overview feedback: %w", err)
	}
	return total, nil
}

func (repository *SQLiteRepository) listProjectOverviewFeedback(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]ProjectOverviewFeedback, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			comment_threads.id,
			review_sessions.id,
			review_sessions.name,
			assets.id,
			assets.name,
			asset_versions.id,
			asset_versions.version_number,
			CASE comment_threads.author_kind
				WHEN 'user' THEN COALESCE(author_user.display_name, '未知成员')
				ELSE COALESCE(author_visitor.display_name, '匿名访客')
			END,
			COALESCE((
				SELECT comments.body
				FROM comments
				WHERE comments.thread_id = comment_threads.id
					AND comments.deleted_at IS NULL
				ORDER BY comments.created_at DESC
				LIMIT 1
			), ''),
			COALESCE(annotations.kind, 'whole'),
			annotations.time_start_us,
			annotations.time_end_us,
			comment_threads.updated_at
		FROM comment_threads
		JOIN review_sessions
			ON review_sessions.id = comment_threads.review_session_id
		JOIN review_items ON review_items.id = comment_threads.review_item_id
		JOIN assets ON assets.id = review_items.asset_id
		JOIN asset_versions ON asset_versions.id = comment_threads.asset_version_id
		LEFT JOIN users author_user
			ON author_user.id = comment_threads.author_user_id
		LEFT JOIN share_visitors author_visitor
			ON author_visitor.id = comment_threads.author_visitor_id
		LEFT JOIN annotations ON annotations.thread_id = comment_threads.id
		WHERE comment_threads.workspace_id = ?
			AND review_sessions.project_id = ?
			AND comment_threads.status = 'open'
			AND comment_threads.deleted_at IS NULL
		ORDER BY comment_threads.updated_at DESC
		LIMIT 6
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project overview feedback: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectOverviewFeedback, 0, 6)
	for rows.Next() {
		var item ProjectOverviewFeedback
		var start sql.NullInt64
		var end sql.NullInt64
		var updatedAt string
		if err := rows.Scan(
			&item.ID,
			&item.ReviewID,
			&item.ReviewName,
			&item.AssetID,
			&item.AssetName,
			&item.AssetVersionID,
			&item.VersionNumber,
			&item.AuthorName,
			&item.Body,
			&item.AnnotationKind,
			&start,
			&end,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project overview feedback: %w", err)
		}
		if start.Valid {
			item.TimeStartUS = &start.Int64
		}
		if end.Valid {
			item.TimeEndUS = &end.Int64
		}
		item.UpdatedAt, err = parseDatabaseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse project overview feedback update time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project overview feedback: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) countProjectOverviewVersions(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (int, error) {
	var total int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM project_assets
		JOIN assets ON assets.id = project_assets.asset_id
		JOIN asset_versions ON asset_versions.asset_id = assets.id
		WHERE project_assets.workspace_id = ?
			AND project_assets.project_id = ?
			AND project_assets.status = 'active'
			AND assets.deleted_at IS NULL
			AND asset_versions.deleted_at IS NULL
	`, workspaceID, projectID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count project overview versions: %w", err)
	}
	return total, nil
}

func (repository *SQLiteRepository) listProjectOverviewVersions(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]ProjectOverviewVersion, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			asset_versions.id,
			assets.id,
			assets.name,
			asset_versions.version_number,
			asset_versions.source_filename,
			asset_versions.processing_status,
			users.display_name,
			asset_versions.created_at
		FROM project_assets
		JOIN assets ON assets.id = project_assets.asset_id
		JOIN asset_versions ON asset_versions.asset_id = assets.id
		JOIN users ON users.id = asset_versions.created_by
		WHERE project_assets.workspace_id = ?
			AND project_assets.project_id = ?
			AND project_assets.status = 'active'
			AND assets.deleted_at IS NULL
			AND asset_versions.deleted_at IS NULL
		ORDER BY asset_versions.created_at DESC, asset_versions.version_number DESC
		LIMIT 6
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project overview versions: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectOverviewVersion, 0, 6)
	for rows.Next() {
		var item ProjectOverviewVersion
		var createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.AssetID,
			&item.AssetName,
			&item.VersionNumber,
			&item.SourceFilename,
			&item.ProcessingStatus,
			&item.ActorName,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan project overview version: %w", err)
		}
		item.CreatedAt, err = parseDatabaseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse project overview version creation time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project overview versions: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) countProjectOverviewFailedTasks(
	ctx context.Context,
	workspaceID string,
	projectID string,
) (int, error) {
	var total int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT jobs.id)
		FROM jobs
		JOIN version_files ON version_files.storage_object_id = jobs.subject_id
		JOIN asset_versions ON asset_versions.id = version_files.asset_version_id
		JOIN project_assets ON project_assets.asset_id = asset_versions.asset_id
		JOIN assets ON assets.id = asset_versions.asset_id
		WHERE jobs.workspace_id = ?
			AND jobs.subject_type = 'storageObject'
			AND jobs.status = 'failed'
			AND project_assets.project_id = ?
			AND project_assets.status = 'active'
			AND assets.current_version_id = asset_versions.id
			AND assets.deleted_at IS NULL
			AND asset_versions.deleted_at IS NULL
	`, workspaceID, projectID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count project overview failed tasks: %w", err)
	}
	return total, nil
}

func (repository *SQLiteRepository) listProjectOverviewFailedTasks(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]ProjectOverviewFailedTask, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			jobs.id,
			jobs.type,
			MIN(assets.id),
			MIN(assets.name),
			COALESCE(jobs.last_error_code, ''),
			COALESCE(jobs.last_error_message, ''),
			jobs.updated_at
		FROM jobs
		JOIN version_files ON version_files.storage_object_id = jobs.subject_id
		JOIN asset_versions ON asset_versions.id = version_files.asset_version_id
		JOIN project_assets ON project_assets.asset_id = asset_versions.asset_id
		JOIN assets ON assets.id = asset_versions.asset_id
		WHERE jobs.workspace_id = ?
			AND jobs.subject_type = 'storageObject'
			AND jobs.status = 'failed'
			AND project_assets.project_id = ?
			AND project_assets.status = 'active'
			AND assets.current_version_id = asset_versions.id
			AND asset_versions.deleted_at IS NULL
			AND assets.deleted_at IS NULL
		GROUP BY
			jobs.id,
			jobs.type,
			jobs.last_error_code,
			jobs.last_error_message,
			jobs.updated_at
		ORDER BY jobs.updated_at DESC
		LIMIT 6
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project overview failed tasks: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectOverviewFailedTask, 0, 6)
	for rows.Next() {
		var item ProjectOverviewFailedTask
		var updatedAt string
		if err := rows.Scan(
			&item.ID,
			&item.Type,
			&item.AssetID,
			&item.AssetName,
			&item.ErrorCode,
			&item.ErrorMessage,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project overview failed task: %w", err)
		}
		item.UpdatedAt, err = parseDatabaseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse project overview failed task update time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project overview failed tasks: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) CreateProject(
	ctx context.Context,
	record projectRecord,
) (Project, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Project{}, fmt.Errorf("begin project creation: %w", err)
	}
	defer tx.Rollback()
	if record.StorageGrantID != "" {
		if err := validateProjectCreationStorageGrant(
			ctx,
			tx,
			record.WorkspaceID,
			record.StorageGrantID,
		); err != nil {
			return Project{}, err
		}
	}

	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO projects (
			id, workspace_id, primary_owner_user_id, name, description, status,
			created_by,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'active', ?, 1, ?, ?)
	`,
		record.ID,
		record.WorkspaceID,
		record.UserID,
		record.Name,
		record.Description,
		record.UserID,
		now,
		now,
	); err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_memberships (
			id, workspace_id, project_id, user_id, role_key, permissions_json,
			status, created_by, joined_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'primary_owner', ?, 'active', ?, ?, ?, ?)
	`,
		record.MembershipID,
		record.WorkspaceID,
		record.ID,
		record.UserID,
		record.PrimaryPermissions,
		record.UserID,
		now,
		now,
		now,
	); err != nil {
		return Project{}, fmt.Errorf("create project primary owner membership: %w", err)
	}
	if record.StorageGrantID != "" {
		purposes := [...]string{"upload", "default_rendition", "archive"}
		for index, purpose := range purposes {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO project_storage_selections (
					id, workspace_id, project_id, grant_id, purpose,
					selected_by, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, record.StorageSelectionIDs[index], record.WorkspaceID, record.ID,
				record.StorageGrantID, purpose, record.UserID, now, now); err != nil {
				return Project{}, fmt.Errorf(
					"create project %s storage selection: %w",
					purpose,
					err,
				)
			}
		}
	}

	project, err := repository.project(ctx, tx, record.WorkspaceID, record.ID)
	if err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, fmt.Errorf("commit project creation: %w", err)
	}
	return project, nil
}

func validateProjectCreationStorageGrant(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	grantID string,
) error {
	var grantStatus, providerKind, providerStatus string
	var rootStatus, bucketPurpose, bucketStatus sql.NullString
	var bucketProjectAvailable sql.NullBool
	err := tx.QueryRowContext(ctx, `
		SELECT g.status, p.kind, p.status, r.status,
			b.purpose, b.status, b.project_available
		FROM project_storage_grants g
		JOIN storage_providers p
			ON p.id = g.storage_provider_id
			AND p.workspace_id = g.workspace_id
			AND p.deleted_at IS NULL
		JOIN authorized_roots r
			ON r.id = g.authorized_root_id
			AND r.workspace_id = g.workspace_id
			AND r.deleted_at IS NULL
		LEFT JOIN local_managed_buckets b
			ON b.authorized_root_id = r.id
			AND b.workspace_id = g.workspace_id
			AND b.deleted_at IS NULL
		WHERE g.workspace_id = ? AND g.id = ?
	`, workspaceID, grantID).Scan(
		&grantStatus,
		&providerKind,
		&providerStatus,
		&rootStatus,
		&bucketPurpose,
		&bucketStatus,
		&bucketProjectAvailable,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProjectStorageUnavailable
		}
		return fmt.Errorf("validate project creation storage grant: %w", err)
	}
	if grantStatus != "active" || providerStatus != "active" ||
		!rootStatus.Valid || rootStatus.String != "available" {
		return ErrProjectStorageUnavailable
	}
	switch providerKind {
	case "webdav", "s3":
		return nil
	case "local":
		if bucketPurpose.Valid && bucketPurpose.String == "upload" &&
			bucketStatus.Valid && bucketStatus.String == "active" &&
			bucketProjectAvailable.Valid && bucketProjectAvailable.Bool {
			return nil
		}
	}
	return ErrProjectStorageUnavailable
}

func (repository *SQLiteRepository) UpdateProject(
	ctx context.Context,
	input UpdateProjectInput,
	now time.Time,
) (Project, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE projects
		SET name = ?,
			description = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`,
		input.Name,
		input.Description,
		formatDatabaseTime(now),
		input.ID,
		input.WorkspaceID,
		input.Revision,
	)
	if err != nil {
		return Project{}, fmt.Errorf("update project: %w", err)
	}
	if err := requireChangedProject(ctx, repository.db, result, input); err != nil {
		return Project{}, err
	}

	return repository.Project(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) SetProjectStatus(
	ctx context.Context,
	input ProjectStateInput,
	status string,
	now time.Time,
) (Project, error) {
	var archivedAt any
	if status == "archived" {
		archivedAt = formatDatabaseTime(now)
	}

	result, err := repository.db.ExecContext(ctx, `
		UPDATE projects
		SET status = ?,
			archived_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`,
		status,
		archivedAt,
		formatDatabaseTime(now),
		input.ID,
		input.WorkspaceID,
		input.Revision,
	)
	if err != nil {
		return Project{}, fmt.Errorf("set project status: %w", err)
	}
	if err := requireChangedProject(ctx, repository.db, result, UpdateProjectInput{
		WorkspaceID: input.WorkspaceID,
		ID:          input.ID,
		Revision:    input.Revision,
	}); err != nil {
		return Project{}, err
	}

	return repository.Project(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) DeleteProject(
	ctx context.Context,
	input ProjectStateInput,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project deletion: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatDatabaseTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET deleted_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`, timestamp, timestamp, input.ID, input.WorkspaceID, input.Revision)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if err := requireChangedProject(ctx, tx, result, UpdateProjectInput{
		WorkspaceID: input.WorkspaceID,
		ID:          input.ID,
		Revision:    input.Revision,
	}); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE collections
		SET deleted_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE project_id = ? AND deleted_at IS NULL
	`, timestamp, timestamp, input.ID); err != nil {
		return fmt.Errorf("delete project collections: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET project_id = NULL,
			revision = revision + 1,
			updated_at = ?
		WHERE workspace_id = ?
			AND project_id = ?
			AND deleted_at IS NULL
	`, timestamp, input.WorkspaceID, input.ID); err != nil {
		return fmt.Errorf("detach project assets: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project deletion: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ListCollections(
	ctx context.Context,
	workspaceID string,
	projectID string,
) ([]Collection, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			collections.id,
			collections.workspace_id,
			collections.project_id,
			collections.name,
			collections.description,
			collections.kind,
			collections.position,
			(
				SELECT COUNT(*)
				FROM collection_items
				WHERE collection_items.collection_id = collections.id
			),
			collections.revision,
			collections.created_at,
			collections.updated_at
		FROM collections
		WHERE collections.workspace_id = ?
			AND collections.project_id = ?
			AND collections.deleted_at IS NULL
		ORDER BY collections.position, collections.created_at
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	collections := make([]Collection, 0)
	for rows.Next() {
		collection, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		collections = append(collections, collection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections: %w", err)
	}

	return collections, nil
}

func (repository *SQLiteRepository) Collection(
	ctx context.Context,
	workspaceID string,
	collectionID string,
) (Collection, error) {
	return repository.collection(ctx, repository.db, workspaceID, collectionID)
}

func (repository *SQLiteRepository) CreateCollection(
	ctx context.Context,
	record collectionRecord,
) (Collection, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Collection{}, fmt.Errorf("begin collection creation: %w", err)
	}
	defer tx.Rollback()

	status, err := projectStatus(ctx, tx, record.WorkspaceID, record.ProjectID)
	if err != nil {
		return Collection{}, err
	}
	if status == "archived" {
		return Collection{}, ErrProjectArchived
	}

	var position int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(position) + 1, 0)
		FROM collections
		WHERE workspace_id = ?
			AND project_id = ?
			AND deleted_at IS NULL
	`, record.WorkspaceID, record.ProjectID).Scan(&position); err != nil {
		return Collection{}, fmt.Errorf("choose collection position: %w", err)
	}

	now := formatDatabaseTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collections (
			id, workspace_id, project_id, name, description, kind,
			position, created_by, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'delivery', ?, ?, 1, ?, ?)
	`,
		record.ID,
		record.WorkspaceID,
		record.ProjectID,
		record.Name,
		record.Description,
		position,
		record.UserID,
		now,
		now,
	); err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Collection{}, fmt.Errorf("commit collection creation: %w", err)
	}
	return repository.Collection(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) UpdateCollection(
	ctx context.Context,
	input UpdateCollectionInput,
	now time.Time,
) (Collection, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE collections
		SET name = ?,
			description = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
			AND EXISTS (
				SELECT 1
				FROM projects
				WHERE projects.id = collections.project_id
					AND projects.status = 'active'
					AND projects.deleted_at IS NULL
			)
	`,
		input.Name,
		input.Description,
		formatDatabaseTime(now),
		input.ID,
		input.WorkspaceID,
		input.Revision,
	)
	if err != nil {
		return Collection{}, fmt.Errorf("update collection: %w", err)
	}
	if err := requireChangedCollection(ctx, repository.db, result, input); err != nil {
		return Collection{}, err
	}
	return repository.Collection(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) DeleteCollection(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	revision int,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE collections
		SET deleted_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`,
		formatDatabaseTime(now),
		formatDatabaseTime(now),
		collectionID,
		workspaceID,
		revision,
	)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	return requireChangedCollection(ctx, repository.db, result, UpdateCollectionInput{
		WorkspaceID: workspaceID,
		ID:          collectionID,
		Revision:    revision,
	})
}

func (repository *SQLiteRepository) ListCollectionItems(
	ctx context.Context,
	workspaceID string,
	collectionID string,
) ([]CollectionItem, error) {
	if _, err := repository.Collection(ctx, workspaceID, collectionID); err != nil {
		return nil, err
	}

	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			collection_items.id,
			collection_items.collection_id,
			collection_items.asset_id,
			assets.name,
			assets.type,
			collection_items.pinned_version_id,
			collection_items.position,
			collection_items.caption,
			collection_items.created_at
		FROM collection_items
		JOIN assets ON assets.id = collection_items.asset_id
		JOIN collections ON collections.id = collection_items.collection_id
		WHERE collection_items.collection_id = ?
			AND collections.workspace_id = ?
			AND collections.deleted_at IS NULL
			AND assets.deleted_at IS NULL
		ORDER BY collection_items.position
	`, collectionID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list collection items: %w", err)
	}
	defer rows.Close()

	items := make([]CollectionItem, 0)
	for rows.Next() {
		item, err := scanCollectionItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection items: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) AddCollectionItem(
	ctx context.Context,
	record collectionItemRecord,
) (CollectionItem, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CollectionItem{}, fmt.Errorf("begin collection item creation: %w", err)
	}
	defer tx.Rollback()

	var projectID string
	var projectState string
	if err := tx.QueryRowContext(ctx, `
		SELECT collections.project_id, projects.status
		FROM collections
		JOIN projects ON projects.id = collections.project_id
		WHERE collections.id = ?
			AND collections.workspace_id = ?
			AND collections.deleted_at IS NULL
			AND projects.deleted_at IS NULL
	`, record.CollectionID, record.WorkspaceID).Scan(&projectID, &projectState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CollectionItem{}, ErrNotFound
		}
		return CollectionItem{}, fmt.Errorf("read collection project: %w", err)
	}
	if projectState == "archived" {
		return CollectionItem{}, ErrProjectArchived
	}

	var assetProjectID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT project_id
		FROM assets
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, record.AssetID, record.WorkspaceID).Scan(&assetProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CollectionItem{}, ErrNotFound
		}
		return CollectionItem{}, fmt.Errorf("read collection asset: %w", err)
	}

	if assetProjectID.Valid && assetProjectID.String != projectID {
		return CollectionItem{}, ErrAssetProjectConflict
	}
	if !assetProjectID.Valid {
		if _, err := tx.ExecContext(ctx, `
			UPDATE assets
			SET project_id = ?, revision = revision + 1, updated_at = ?
			WHERE id = ? AND workspace_id = ?
		`,
			projectID,
			formatDatabaseTime(record.Now),
			record.AssetID,
			record.WorkspaceID,
		); err != nil {
			return CollectionItem{}, fmt.Errorf("assign asset to project: %w", err)
		}
	}

	if record.PinnedVersionID != nil {
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1
			FROM asset_versions
			WHERE id = ?
				AND asset_id = ?
				AND workspace_id = ?
				AND deleted_at IS NULL
		`, *record.PinnedVersionID, record.AssetID, record.WorkspaceID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CollectionItem{}, ErrPinnedVersionInvalid
			}
			return CollectionItem{}, fmt.Errorf("validate collection pinned version: %w", err)
		}
	}

	var position int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(position) + 1, 0)
		FROM collection_items
		WHERE collection_id = ?
	`, record.CollectionID).Scan(&position); err != nil {
		return CollectionItem{}, fmt.Errorf("choose collection item position: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO collection_items (
			id, collection_id, asset_id, pinned_version_id,
			position, caption, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		record.ID,
		record.CollectionID,
		record.AssetID,
		record.PinnedVersionID,
		position,
		record.Caption,
		formatDatabaseTime(record.Now),
	); err != nil {
		if strings.Contains(err.Error(), "collection_items.collection_id, collection_items.asset_id") {
			return CollectionItem{}, ErrDuplicateItem
		}
		return CollectionItem{}, fmt.Errorf("create collection item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return CollectionItem{}, fmt.Errorf("commit collection item creation: %w", err)
	}
	return repository.collectionItem(
		ctx,
		record.WorkspaceID,
		record.CollectionID,
		record.ID,
	)
}

func (repository *SQLiteRepository) ReorderCollectionItems(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	itemIDs []string,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin collection item reorder: %w", err)
	}
	defer tx.Rollback()

	if _, err := collectionProject(ctx, tx, workspaceID, collectionID); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM collection_items
		WHERE collection_id = ?
		ORDER BY position
	`, collectionID)
	if err != nil {
		return fmt.Errorf("read collection item order: %w", err)
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan collection item order: %w", err)
		}
		existing[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close collection item order: %w", err)
	}

	if len(existing) != len(itemIDs) {
		return ErrInvalidOrder
	}
	seen := make(map[string]struct{}, len(itemIDs))
	for _, id := range itemIDs {
		if _, ok := existing[id]; !ok {
			return ErrInvalidOrder
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrInvalidOrder
		}
		seen[id] = struct{}{}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE collection_items
		SET position = position + 1000000
		WHERE collection_id = ?
	`, collectionID); err != nil {
		return fmt.Errorf("offset collection item positions: %w", err)
	}
	for position, id := range itemIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE collection_items
			SET position = ?
			WHERE id = ? AND collection_id = ?
		`, position, id, collectionID); err != nil {
			return fmt.Errorf("set collection item position: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collection item reorder: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) DeleteCollectionItem(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	itemID string,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin collection item deletion: %w", err)
	}
	defer tx.Rollback()

	if _, err := collectionProject(ctx, tx, workspaceID, collectionID); err != nil {
		return err
	}

	var position int
	if err := tx.QueryRowContext(ctx, `
		SELECT position
		FROM collection_items
		WHERE id = ? AND collection_id = ?
	`, itemID, collectionID).Scan(&position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read collection item: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM collection_items
		WHERE id = ? AND collection_id = ?
	`, itemID, collectionID); err != nil {
		return fmt.Errorf("delete collection item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collection_items
		SET position = position - 1
		WHERE collection_id = ? AND position > ?
	`, collectionID, position); err != nil {
		return fmt.Errorf("compact collection item positions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collection item deletion: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

type projectQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (repository *SQLiteRepository) project(
	ctx context.Context,
	queryer projectQueryer,
	workspaceID string,
	projectID string,
) (Project, error) {
	project, err := scanProject(queryer.QueryRowContext(ctx, `
		SELECT
			projects.id,
			projects.workspace_id,
			projects.primary_owner_user_id,
			projects.name,
			projects.description,
			projects.status,
			(
				SELECT COUNT(*)
				FROM assets
				WHERE assets.project_id = projects.id
					AND assets.deleted_at IS NULL
			),
			(
				SELECT COUNT(*)
				FROM collections
				WHERE collections.project_id = projects.id
					AND collections.deleted_at IS NULL
			),
			projects.revision,
			projects.created_at,
			projects.updated_at,
			projects.archived_at
		FROM projects
		WHERE projects.id = ?
			AND projects.workspace_id = ?
			AND projects.deleted_at IS NULL
	`, projectID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func scanProject(scanner rowScanner) (Project, error) {
	var project Project
	var primaryOwnerUserID sql.NullString
	var description sql.NullString
	var createdAt string
	var updatedAt string
	var archivedAt sql.NullString
	if err := scanner.Scan(
		&project.ID,
		&project.WorkspaceID,
		&primaryOwnerUserID,
		&project.Name,
		&description,
		&project.Status,
		&project.AssetCount,
		&project.CollectionCount,
		&project.Revision,
		&createdAt,
		&updatedAt,
		&archivedAt,
	); err != nil {
		return Project{}, err
	}

	var err error
	project.PrimaryOwnerUserID = nullableString(primaryOwnerUserID)
	project.Description = nullableString(description)
	project.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return Project{}, fmt.Errorf("parse project creation time: %w", err)
	}
	project.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("parse project update time: %w", err)
	}
	project.ArchivedAt, err = nullableTime(archivedAt)
	if err != nil {
		return Project{}, fmt.Errorf("parse project archive time: %w", err)
	}
	return project, nil
}

type collectionQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (repository *SQLiteRepository) collection(
	ctx context.Context,
	queryer collectionQueryer,
	workspaceID string,
	collectionID string,
) (Collection, error) {
	collection, err := scanCollection(queryer.QueryRowContext(ctx, `
		SELECT
			collections.id,
			collections.workspace_id,
			collections.project_id,
			collections.name,
			collections.description,
			collections.kind,
			collections.position,
			(
				SELECT COUNT(*)
				FROM collection_items
				WHERE collection_items.collection_id = collections.id
			),
			collections.revision,
			collections.created_at,
			collections.updated_at
		FROM collections
		WHERE collections.id = ?
			AND collections.workspace_id = ?
			AND collections.deleted_at IS NULL
	`, collectionID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	return collection, err
}

func scanCollection(scanner rowScanner) (Collection, error) {
	var collection Collection
	var description sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&collection.ID,
		&collection.WorkspaceID,
		&collection.ProjectID,
		&collection.Name,
		&description,
		&collection.Kind,
		&collection.Position,
		&collection.ItemCount,
		&collection.Revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Collection{}, err
	}

	var err error
	collection.Description = nullableString(description)
	collection.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return Collection{}, fmt.Errorf("parse collection creation time: %w", err)
	}
	collection.UpdatedAt, err = parseDatabaseTime(updatedAt)
	if err != nil {
		return Collection{}, fmt.Errorf("parse collection update time: %w", err)
	}
	return collection, nil
}

func (repository *SQLiteRepository) collectionItem(
	ctx context.Context,
	workspaceID string,
	collectionID string,
	itemID string,
) (CollectionItem, error) {
	item, err := scanCollectionItem(repository.db.QueryRowContext(ctx, `
		SELECT
			collection_items.id,
			collection_items.collection_id,
			collection_items.asset_id,
			assets.name,
			assets.type,
			collection_items.pinned_version_id,
			collection_items.position,
			collection_items.caption,
			collection_items.created_at
		FROM collection_items
		JOIN assets ON assets.id = collection_items.asset_id
		JOIN collections ON collections.id = collection_items.collection_id
		WHERE collection_items.id = ?
			AND collection_items.collection_id = ?
			AND collections.workspace_id = ?
			AND collections.deleted_at IS NULL
	`, itemID, collectionID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return CollectionItem{}, ErrNotFound
	}
	return item, err
}

func scanCollectionItem(scanner rowScanner) (CollectionItem, error) {
	var item CollectionItem
	var pinnedVersionID sql.NullString
	var caption sql.NullString
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.CollectionID,
		&item.AssetID,
		&item.AssetName,
		&item.AssetType,
		&pinnedVersionID,
		&item.Position,
		&caption,
		&createdAt,
	); err != nil {
		return CollectionItem{}, err
	}
	item.PinnedVersionID = nullableString(pinnedVersionID)
	item.Caption = nullableString(caption)
	var err error
	item.CreatedAt, err = parseDatabaseTime(createdAt)
	if err != nil {
		return CollectionItem{}, fmt.Errorf("parse collection item creation time: %w", err)
	}
	return item, nil
}

type queryRowExecutor interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func requireChangedProject(
	ctx context.Context,
	queryer queryRowExecutor,
	result sql.Result,
	input UpdateProjectInput,
) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read project write result: %w", err)
	}
	if changed > 0 {
		return nil
	}

	var revision int
	err = queryer.QueryRowContext(ctx, `
		SELECT revision
		FROM projects
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, input.ID, input.WorkspaceID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify project write: %w", err)
	}
	return ErrRevisionConflict
}

func requireChangedCollection(
	ctx context.Context,
	queryer queryRowExecutor,
	result sql.Result,
	input UpdateCollectionInput,
) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read collection write result: %w", err)
	}
	if changed > 0 {
		return nil
	}

	var revision int
	var projectStatus string
	err = queryer.QueryRowContext(ctx, `
		SELECT collections.revision, projects.status
		FROM collections
		JOIN projects ON projects.id = collections.project_id
		WHERE collections.id = ?
			AND collections.workspace_id = ?
			AND collections.deleted_at IS NULL
			AND projects.deleted_at IS NULL
	`, input.ID, input.WorkspaceID).Scan(&revision, &projectStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("classify collection write: %w", err)
	}
	if projectStatus == "archived" {
		return ErrProjectArchived
	}
	return ErrRevisionConflict
}

func projectStatus(
	ctx context.Context,
	queryer queryRowExecutor,
	workspaceID string,
	projectID string,
) (string, error) {
	var status string
	err := queryer.QueryRowContext(ctx, `
		SELECT status
		FROM projects
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, projectID, workspaceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read project status: %w", err)
	}
	return status, nil
}

func collectionProject(
	ctx context.Context,
	queryer queryRowExecutor,
	workspaceID string,
	collectionID string,
) (string, error) {
	var projectID string
	err := queryer.QueryRowContext(ctx, `
		SELECT project_id
		FROM collections
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, collectionID, workspaceID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read collection project: %w", err)
	}
	return projectID, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseDatabaseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func formatDatabaseTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000Z")
}

func parseDatabaseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
