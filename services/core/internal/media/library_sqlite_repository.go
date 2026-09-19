package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"review-studio.local/core/internal/storage"
)

type SQLiteLibraryRepository struct {
	db *sql.DB
}

func NewSQLiteLibraryRepository(db *sql.DB) *SQLiteLibraryRepository {
	return &SQLiteLibraryRepository{db: db}
}

func (repository *SQLiteLibraryRepository) Query(
	ctx context.Context,
	workspaceID string,
	rootID string,
	query LibraryQuery,
) (LibraryPage, error) {
	purpose, err := repository.validateRoot(ctx, workspaceID, rootID)
	if err != nil {
		return LibraryPage{}, err
	}
	return repository.query(ctx, workspaceID, rootID, purpose, query, libraryScope{
		RootBound: true,
	})
}

func (repository *SQLiteLibraryRepository) QueryProject(
	ctx context.Context,
	workspaceID string,
	projectID string,
	query LibraryQuery,
) (LibraryPage, error) {
	query.ProjectID = projectID
	return repository.query(ctx, workspaceID, "", "", query, libraryScope{
		RequireAsset: true,
	})
}

func (repository *SQLiteLibraryRepository) QueryProjectCandidates(
	ctx context.Context,
	workspaceID string,
	projectID string,
	query LibraryQuery,
) (LibraryPage, error) {
	query.ProjectID = ""
	return repository.query(ctx, workspaceID, "", "", query, libraryScope{
		RequireAsset:     true,
		ExcludeProjectID: projectID,
	})
}

type libraryScope struct {
	RootBound        bool
	RequireAsset     bool
	ExcludeProjectID string
}

func (repository *SQLiteLibraryRepository) query(
	ctx context.Context,
	workspaceID string,
	rootID string,
	rootPurpose string,
	query LibraryQuery,
	scope libraryScope,
) (LibraryPage, error) {
	where, arguments := libraryWhere(
		workspaceID,
		rootID,
		rootPurpose,
		query,
		scope,
	)
	var total int
	if err := repository.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) "+libraryFrom()+where,
		arguments...,
	).Scan(&total); err != nil {
		return LibraryPage{}, fmt.Errorf("count media library items: %w", err)
	}

	order := libraryOrder(query.Sort)
	arguments = append(arguments, query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			COALESCE(cso.id, so.id),
			so.workspace_id,
			COALESCE(cso.storage_provider_id, so.storage_provider_id),
			COALESCE(cso.authorized_root_id, so.authorized_root_id),
			COALESCE(cso.object_key, so.object_key),
			COALESCE(cso.status, so.status),
			COALESCE(cso.size_bytes, so.size_bytes),
			COALESCE(cso.modified_at, so.modified_at),
			COALESCE(cso.quick_fingerprint, so.quick_fingerprint),
			COALESCE(cso.mime_type, so.mime_type),
			COALESCE(cso.first_discovered_at, so.first_discovered_at),
			COALESCE(cso.last_seen_at, so.last_seen_at),
			COALESCE(cso.missing_since, so.missing_since),
			COALESCE(cso.created_at, so.created_at),
			COALESCE(cso.updated_at, so.updated_at)
	`+libraryFrom()+`
	`+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", arguments...)
	if err != nil {
		return LibraryPage{}, fmt.Errorf("query media library items: %w", err)
	}
	defer rows.Close()

	items := make([]LibraryItem, 0, query.PageSize)
	ids := make([]string, 0, query.PageSize)
	index := make(map[string]int, query.PageSize)
	for rows.Next() {
		object, scanErr := scanLibraryObject(rows)
		if scanErr != nil {
			return LibraryPage{}, scanErr
		}
		index[object.ID] = len(items)
		ids = append(ids, object.ID)
		items = append(items, LibraryItem{Object: object})
	}
	if err := rows.Err(); err != nil {
		return LibraryPage{}, fmt.Errorf("iterate media library items: %w", err)
	}
	if len(ids) == 0 {
		return LibraryPage{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
	}

	if err := repository.loadProbes(ctx, workspaceID, ids, items, index); err != nil {
		return LibraryPage{}, err
	}
	if err := repository.loadAssets(ctx, workspaceID, ids, items, index); err != nil {
		return LibraryPage{}, err
	}
	if err := repository.loadRenditions(ctx, workspaceID, ids, items, index); err != nil {
		return LibraryPage{}, err
	}
	if err := repository.loadJobs(ctx, workspaceID, ids, items, index); err != nil {
		return LibraryPage{}, err
	}
	if err := repository.loadUploadChecks(ctx, workspaceID, ids, items, index); err != nil {
		return LibraryPage{}, err
	}
	return LibraryPage{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func libraryFrom() string {
	return `
		FROM storage_objects so
		LEFT JOIN assets la
			ON la.origin_storage_object_id = so.id
			AND la.deleted_at IS NULL
		LEFT JOIN asset_versions lav
			ON lav.id = la.current_version_id
			AND lav.deleted_at IS NULL
		LEFT JOIN version_files lvf
			ON lvf.asset_version_id = lav.id
			AND lvf.role = 'primary'
		LEFT JOIN storage_objects cso
			ON cso.id = lvf.storage_object_id
			AND cso.deleted_at IS NULL
		LEFT JOIN media_probes mp
			ON mp.storage_object_id = COALESCE(cso.id, so.id)
	`
}

func (repository *SQLiteLibraryRepository) validateRoot(
	ctx context.Context,
	workspaceID string,
	rootID string,
) (string, error) {
	var purpose string
	if err := repository.db.QueryRowContext(ctx, `
		SELECT purpose FROM authorized_roots
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, rootID, workspaceID).Scan(&purpose); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", storage.ErrRootNotFound
		}
		return "", fmt.Errorf("validate media library root: %w", err)
	}
	return purpose, nil
}

func libraryWhere(
	workspaceID string,
	rootID string,
	rootPurpose string,
	query LibraryQuery,
	scope libraryScope,
) (string, []any) {
	parts := []string{
		"WHERE so.workspace_id = ?",
		"AND so.deleted_at IS NULL",
		`AND NOT EXISTS (
			SELECT 1 FROM upload_checks uc
			WHERE uc.workspace_id = so.workspace_id
				AND uc.asset_version_id = lav.id
				AND uc.status <> 'ready'
		)`,
	}
	arguments := []any{workspaceID}
	if scope.RootBound {
		parts = append(parts, "AND so.authorized_root_id = ?")
		arguments = append(arguments, rootID)
	}
	if value := strings.TrimSpace(query.Search); value != "" {
		parts = append(
			parts,
			"AND instr(lower(COALESCE(cso.object_key, so.object_key)), lower(?)) > 0",
		)
		arguments = append(arguments, value)
	}
	if rootPurpose == "managed_versions" {
		parts = append(
			parts,
			`AND EXISTS (
				SELECT 1 FROM assets origin_asset
				WHERE origin_asset.origin_storage_object_id = so.id
					AND origin_asset.deleted_at IS NULL
			)`,
		)
	}
	if scope.RequireAsset {
		parts = append(parts, `AND EXISTS (`+libraryAssetExistsQuery()+`)`)
	}
	if query.MediaType != "" && query.MediaType != "all" {
		parts = append(parts, "AND "+libraryMediaTypeExpression()+" = ?")
		arguments = append(arguments, query.MediaType)
	}
	if query.State != "" && query.State != "all" {
		parts = append(parts, "AND "+libraryStateExpression()+" = ?")
		arguments = append(arguments, query.State)
	}
	if query.ProjectID == "unassigned" {
		parts = append(
			parts,
			`AND EXISTS (`+libraryAssetExistsQuery()+`)
			AND NOT EXISTS (`+libraryProjectAssetExistsQuery()+`)`,
		)
	} else if query.ProjectID != "" && query.ProjectID != "all" {
		parts = append(
			parts,
			`AND EXISTS (`+libraryProjectAssetExistsQuery()+` AND pa.project_id = ?)`,
		)
		arguments = append(arguments, query.ProjectID)
	}
	if scope.ExcludeProjectID != "" {
		parts = append(
			parts,
			`AND NOT EXISTS (`+libraryProjectAssetExistsQuery()+` AND pa.project_id = ?)`,
		)
		arguments = append(arguments, scope.ExcludeProjectID)
	}
	if query.ModifiedFrom != nil {
		parts = append(parts, "AND COALESCE(cso.modified_at, so.modified_at) >= ?")
		arguments = append(arguments, formatTime(*query.ModifiedFrom))
	}
	if query.ModifiedTo != nil {
		parts = append(parts, "AND COALESCE(cso.modified_at, so.modified_at) < ?")
		arguments = append(arguments, formatTime(*query.ModifiedTo))
	}
	return strings.Join(parts, " "), arguments
}

func libraryAssetExistsQuery() string {
	return `SELECT 1
		FROM assets a
		WHERE a.origin_storage_object_id = so.id
			AND a.deleted_at IS NULL`
}

func libraryProjectAssetExistsQuery() string {
	return `SELECT 1
		FROM assets a
		JOIN project_assets pa
			ON pa.asset_id = a.id
			AND pa.workspace_id = a.workspace_id
			AND pa.status = 'active'
		WHERE a.origin_storage_object_id = so.id
			AND a.deleted_at IS NULL`
}

func libraryMediaTypeExpression() string {
	return `COALESCE(
		mp.media_type,
		CASE
			WHEN COALESCE(cso.mime_type, so.mime_type) LIKE 'image/%' THEN 'image'
			WHEN COALESCE(cso.mime_type, so.mime_type) LIKE 'video/%' THEN 'video'
			ELSE 'other'
		END
	)`
}

func libraryStateExpression() string {
	return `CASE
		WHEN COALESCE(cso.status, so.status) = 'missing' THEN 'missing'
		WHEN mp.status = 'failed' THEN 'failed'
		WHEN EXISTS (
			SELECT 1 FROM renditions r
			WHERE r.workspace_id = so.workspace_id
				AND r.source_storage_object_id = COALESCE(cso.id, so.id)
				AND r.status = 'ready'
				AND r.kind IN ('screen_preview', 'proxy', 'hls')
		) OR (
			mp.status = 'succeeded'
			AND ` + libraryMediaTypeExpression() + ` NOT IN ('video', 'image')
		) THEN 'ready'
		WHEN EXISTS (
			SELECT 1 FROM jobs j
			WHERE j.workspace_id = so.workspace_id
				AND j.subject_type = 'storageObject'
				AND j.subject_id = COALESCE(cso.id, so.id)
				AND j.type IN (
					'media.generate_image_renditions',
					'media.generate_video_renditions',
					'media.generate_video_enhancements',
					'media.process_asset_version'
				)
				AND j.status IN ('queued', 'leased', 'running', 'cancel_requested')
		) THEN 'processing'
		WHEN EXISTS (
				SELECT 1 FROM jobs j
				WHERE j.workspace_id = so.workspace_id
					AND j.subject_type = 'storageObject'
					AND j.subject_id = COALESCE(cso.id, so.id)
					AND j.type IN (
						'media.generate_image_renditions',
						'media.generate_video_renditions',
						'media.generate_video_enhancements',
						'media.process_asset_version'
					)
					AND j.status IN ('failed', 'cancelled')
			)
			OR EXISTS (
				SELECT 1 FROM renditions r
				WHERE r.workspace_id = so.workspace_id
					AND r.source_storage_object_id = COALESCE(cso.id, so.id)
					AND r.status = 'failed'
			) THEN 'failed'
		ELSE 'waiting'
	END`
}

func libraryOrder(sortValue string) string {
	switch sortValue {
	case "name_desc":
		return "COALESCE(cso.object_key, so.object_key) COLLATE NOCASE DESC, so.id"
	case "modified_asc":
		return "COALESCE(cso.modified_at, so.modified_at) ASC, so.id"
	case "size_desc":
		return "COALESCE(cso.size_bytes, so.size_bytes) DESC, so.id"
	case "size_asc":
		return "COALESCE(cso.size_bytes, so.size_bytes) ASC, so.id"
	case "name_asc":
		return "COALESCE(cso.object_key, so.object_key) COLLATE NOCASE ASC, so.id"
	default:
		return "COALESCE(cso.modified_at, so.modified_at) DESC, so.id"
	}
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func objectArguments(workspaceID string, ids []string) []any {
	result := make([]any, 0, len(ids)+1)
	result = append(result, workspaceID)
	for _, id := range ids {
		result = append(result, id)
	}
	return result
}

func (repository *SQLiteLibraryRepository) loadAssets(
	ctx context.Context,
	workspaceID string,
	ids []string,
	items []LibraryItem,
	index map[string]int,
) error {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			vf.storage_object_id,
			a.id,
			COALESCE((
				SELECT pa.project_id
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), a.project_id),
			COALESCE((
				SELECT pp.name
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), p.name),
			a.name, a.type, a.revision, av.id, av.version_number
		FROM version_files vf
		JOIN asset_versions av ON av.id = vf.asset_version_id
		JOIN assets a
			ON a.id = av.asset_id
			AND a.current_version_id = av.id
		LEFT JOIN projects p ON p.id = a.project_id AND p.deleted_at IS NULL
		WHERE a.workspace_id = ?
			AND vf.storage_object_id IN (`+placeholders(len(ids))+`)
			AND vf.role = 'primary'
			AND av.deleted_at IS NULL
			AND a.deleted_at IS NULL
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load media library assets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var objectID string
		var item LibraryAsset
		var projectID, projectName sql.NullString
		if err := rows.Scan(
			&objectID,
			&item.ID,
			&projectID,
			&projectName,
			&item.Name,
			&item.Type,
			&item.Revision,
			&item.VersionID,
			&item.VersionNumber,
		); err != nil {
			return fmt.Errorf("scan media library asset: %w", err)
		}
		if projectID.Valid {
			item.ProjectID = &projectID.String
		}
		if projectName.Valid {
			item.ProjectName = &projectName.String
		}
		items[index[objectID]].Asset = &item
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) AssignProject(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
	projectID *string,
	now time.Time,
) (LibraryAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return LibraryAsset{}, fmt.Errorf("begin media asset assignment: %w", err)
	}
	defer tx.Rollback()

	if projectID != nil {
		if err := requireActiveProject(ctx, tx, workspaceID, *projectID); err != nil {
			return LibraryAsset{}, ErrLibraryProjectInvalid
		}
	}

	var assetID string
	if err := tx.QueryRowContext(ctx, `
		SELECT a.id
		FROM version_files vf
		JOIN asset_versions av ON av.id = vf.asset_version_id
		JOIN assets a
			ON a.id = av.asset_id
			AND a.current_version_id = av.id
		WHERE vf.storage_object_id = ?
			AND vf.role = 'primary'
			AND a.workspace_id = ?
			AND a.deleted_at IS NULL
	`, storageObjectID, workspaceID).Scan(&assetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LibraryAsset{}, ErrLibraryAssetNotFound
		}
		return LibraryAsset{}, fmt.Errorf("read media asset assignment: %w", err)
	}

	timestamp := formatTime(now)
	if projectID == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE project_assets
			SET status = 'trashed',
				trashed_by = COALESCE(trashed_by, (
					SELECT created_by FROM assets
					WHERE assets.id = project_assets.asset_id
						AND assets.workspace_id = project_assets.workspace_id
				)),
				trashed_at = ?,
				trash_expires_at = ?,
				updated_at = ?
			WHERE workspace_id = ?
				AND asset_id = ?
				AND status = 'active'
		`, timestamp, formatTime(now.AddDate(0, 0, 30)), timestamp,
			workspaceID, assetID); err != nil {
			return LibraryAsset{}, fmt.Errorf("unassign media asset projects: %w", err)
		}
	} else if err := upsertActiveProjectAsset(
		ctx,
		tx,
		projectAssetRecord{
			ID:          "past_" + *projectID + "_" + assetID,
			WorkspaceID: workspaceID,
			ProjectID:   *projectID,
			AssetID:     assetID,
			UserID:      "",
			Now:         now,
		},
	); err != nil {
		return LibraryAsset{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET project_id = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, projectID, timestamp, assetID, workspaceID); err != nil {
		return LibraryAsset{}, fmt.Errorf("assign media asset project: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LibraryAsset{}, fmt.Errorf("commit media asset assignment: %w", err)
	}
	return repository.asset(ctx, workspaceID, storageObjectID)
}

func (repository *SQLiteLibraryRepository) AssetProjectID(
	ctx context.Context,
	workspaceID string,
	assetID string,
) (*string, error) {
	projectIDs, err := repository.AssetProjectIDs(ctx, workspaceID, assetID)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) == 0 {
		return nil, nil
	}
	return &projectIDs[0], nil
}

func (repository *SQLiteLibraryRepository) AssetProjectIDs(
	ctx context.Context,
	workspaceID string,
	assetID string,
) ([]string, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT pa.project_id
		FROM project_assets pa
		JOIN projects p
			ON p.id = pa.project_id
			AND p.workspace_id = pa.workspace_id
			AND p.deleted_at IS NULL
		WHERE pa.workspace_id = ?
			AND pa.asset_id = ?
			AND pa.status = 'active'
		ORDER BY pa.created_at
	`, workspaceID, assetID)
	if err != nil {
		return nil, fmt.Errorf("read media asset projects: %w", err)
	}
	defer rows.Close()

	projectIDs := make([]string, 0)
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			return nil, fmt.Errorf("scan media asset project: %w", err)
		}
		projectIDs = append(projectIDs, projectID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media asset projects: %w", err)
	}
	if len(projectIDs) > 0 {
		return projectIDs, nil
	}

	var fallback sql.NullString
	err = repository.db.QueryRowContext(ctx, `
		SELECT project_id
		FROM assets
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&fallback)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryAssetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read media asset legacy project: %w", err)
	}
	if fallback.Valid {
		return []string{fallback.String}, nil
	}
	return nil, nil
}

func (repository *SQLiteLibraryRepository) AssetForStorageObject(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
) (LibraryAsset, error) {
	return repository.asset(ctx, workspaceID, storageObjectID)
}

func (repository *SQLiteLibraryRepository) AssetIDForVersion(
	ctx context.Context,
	workspaceID string,
	versionID string,
) (string, error) {
	var assetID string
	err := repository.db.QueryRowContext(ctx, `
		SELECT av.asset_id
		FROM asset_versions av
		JOIN assets a
			ON a.id = av.asset_id
			AND a.workspace_id = av.workspace_id
			AND a.deleted_at IS NULL
		WHERE av.id = ?
			AND av.workspace_id = ?
			AND av.deleted_at IS NULL
	`, versionID, workspaceID).Scan(&assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryAssetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read asset version project subject: %w", err)
	}
	return assetID, nil
}

func (repository *SQLiteLibraryRepository) AddAssetToProject(
	ctx context.Context,
	record projectAssetRecord,
) (ProjectAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("begin add project asset: %w", err)
	}
	defer tx.Rollback()

	if err := requireActiveProject(ctx, tx, record.WorkspaceID, record.ProjectID); err != nil {
		return ProjectAsset{}, err
	}
	if err := requireActiveAsset(ctx, tx, record.WorkspaceID, record.AssetID); err != nil {
		return ProjectAsset{}, err
	}
	if err := upsertActiveProjectAsset(ctx, tx, record); err != nil {
		return ProjectAsset{}, err
	}
	if err := setAssetPreferredProject(
		ctx,
		tx,
		record.WorkspaceID,
		record.AssetID,
		&record.ProjectID,
		record.Now,
	); err != nil {
		return ProjectAsset{}, err
	}
	item, err := activeProjectAsset(ctx, tx, record.WorkspaceID, record.ProjectID, record.AssetID)
	if err != nil {
		return ProjectAsset{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAsset{}, fmt.Errorf("commit add project asset: %w", err)
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) MoveAssetToProject(
	ctx context.Context,
	record moveProjectAssetRecord,
) (ProjectAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("begin move project asset: %w", err)
	}
	defer tx.Rollback()

	if _, err := activeProjectAsset(
		ctx,
		tx,
		record.WorkspaceID,
		record.SourceProjectID,
		record.AssetID,
	); err != nil {
		return ProjectAsset{}, err
	}
	if err := requireActiveProject(
		ctx,
		tx,
		record.WorkspaceID,
		record.TargetProjectID,
	); err != nil {
		return ProjectAsset{}, err
	}
	if err := upsertActiveProjectAsset(ctx, tx, projectAssetRecord{
		ID:          record.TargetRelationID,
		WorkspaceID: record.WorkspaceID,
		ProjectID:   record.TargetProjectID,
		AssetID:     record.AssetID,
		UserID:      record.UserID,
		Now:         record.Now,
	}); err != nil {
		return ProjectAsset{}, err
	}
	if _, err := trashProjectAsset(
		ctx,
		tx,
		record.WorkspaceID,
		record.SourceProjectID,
		record.AssetID,
		record.UserID,
		record.Now,
	); err != nil {
		return ProjectAsset{}, err
	}
	if err := setAssetPreferredProject(
		ctx,
		tx,
		record.WorkspaceID,
		record.AssetID,
		&record.TargetProjectID,
		record.Now,
	); err != nil {
		return ProjectAsset{}, err
	}
	item, err := activeProjectAsset(
		ctx,
		tx,
		record.WorkspaceID,
		record.TargetProjectID,
		record.AssetID,
	)
	if err != nil {
		return ProjectAsset{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAsset{}, fmt.Errorf("commit move project asset: %w", err)
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) TrashAssetFromProject(
	ctx context.Context,
	record trashProjectAssetRecord,
) (ProjectAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("begin trash project asset: %w", err)
	}
	defer tx.Rollback()

	item, err := trashProjectAsset(
		ctx,
		tx,
		record.WorkspaceID,
		record.ProjectID,
		record.AssetID,
		record.UserID,
		record.Now,
	)
	if err != nil {
		return ProjectAsset{}, err
	}
	if err := resetAssetPreferredProject(
		ctx,
		tx,
		record.WorkspaceID,
		record.AssetID,
		record.Now,
	); err != nil {
		return ProjectAsset{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAsset{}, fmt.Errorf("commit trash project asset: %w", err)
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) RestoreAssetToProject(
	ctx context.Context,
	record restoreProjectAssetRecord,
) (ProjectAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("begin restore project asset: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatTime(record.Now)
	result, err := tx.ExecContext(ctx, `
		UPDATE project_assets
		SET status = 'active',
			restored_by = ?,
			restored_at = ?,
			trashed_by = NULL,
			trashed_at = NULL,
			trash_expires_at = NULL,
			updated_at = ?
		WHERE workspace_id = ?
			AND project_id = ?
			AND asset_id = ?
			AND status = 'trashed'
			AND (trash_expires_at IS NULL OR trash_expires_at > ?)
	`, record.UserID, timestamp, timestamp, record.WorkspaceID,
		record.ProjectID, record.AssetID, timestamp)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("restore project asset: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("read restore project asset result: %w", err)
	}
	if affected == 0 {
		return ProjectAsset{}, ErrProjectAssetNotFound
	}
	if err := setAssetPreferredProject(
		ctx,
		tx,
		record.WorkspaceID,
		record.AssetID,
		&record.ProjectID,
		record.Now,
	); err != nil {
		return ProjectAsset{}, err
	}
	item, err := activeProjectAsset(ctx, tx, record.WorkspaceID, record.ProjectID, record.AssetID)
	if err != nil {
		return ProjectAsset{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProjectAsset{}, fmt.Errorf("commit restore project asset: %w", err)
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) ListProjectTrash(
	ctx context.Context,
	workspaceID string,
	projectID string,
	now time.Time,
) ([]ProjectAsset, error) {
	rows, err := repository.db.QueryContext(
		ctx,
		projectAssetSelect()+`
		WHERE pa.workspace_id = ?
			AND pa.project_id = ?
			AND pa.status = 'trashed'
			AND (pa.trash_expires_at IS NULL OR pa.trash_expires_at > ?)
			AND p.deleted_at IS NULL
			AND a.deleted_at IS NULL
		ORDER BY pa.trashed_at DESC, pa.updated_at DESC
	`, workspaceID, projectID, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("list project asset trash: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectAsset, 0)
	for rows.Next() {
		item, scanErr := scanProjectAsset(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func projectAssetSelect() string {
	return `
		SELECT
			pa.id, pa.workspace_id, pa.project_id, p.name,
			pa.asset_id, a.name, a.type, pa.status, pa.added_by,
			pa.trashed_by, pa.trashed_at, pa.trash_expires_at,
			pa.created_at, pa.updated_at
		FROM project_assets pa
		JOIN projects p
			ON p.id = pa.project_id
			AND p.workspace_id = pa.workspace_id
		JOIN assets a
			ON a.id = pa.asset_id
			AND a.workspace_id = pa.workspace_id
	`
}

func scanProjectAsset(scanner rowScanner) (ProjectAsset, error) {
	var item ProjectAsset
	var trashedBy, trashedAt, trashExpiresAt sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ProjectID,
		&item.ProjectName,
		&item.AssetID,
		&item.AssetName,
		&item.AssetType,
		&item.Status,
		&item.AddedBy,
		&trashedBy,
		&trashedAt,
		&trashExpiresAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ProjectAsset{}, fmt.Errorf("scan project asset: %w", err)
	}
	if trashedBy.Valid {
		item.TrashedBy = &trashedBy.String
	}
	var err error
	item.TrashedAt, err = parseNullableTime(trashedAt)
	if err != nil {
		return ProjectAsset{}, err
	}
	item.TrashExpiresAt, err = parseNullableTime(trashExpiresAt)
	if err != nil {
		return ProjectAsset{}, err
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return ProjectAsset{}, err
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return ProjectAsset{}, err
	}
	return item, nil
}

func scanUploadCheck(scanner rowScanner) (UploadCheck, error) {
	var item UploadCheck
	var projectID, uploadedBy, visitorID sql.NullString
	var resultCode, message sql.NullString
	var createdAt, updatedAt string
	var completedAt, quarantinedAt, rejectedAt sql.NullString
	if err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&projectID,
		&item.AssetID,
		&item.AssetVersionID,
		&item.StorageObjectID,
		&uploadedBy,
		&visitorID,
		&item.SourceType,
		&item.UploadSecurityPolicy,
		&item.Status,
		&resultCode,
		&message,
		&createdAt,
		&updatedAt,
		&completedAt,
		&quarantinedAt,
		&rejectedAt,
	); err != nil {
		return UploadCheck{}, fmt.Errorf("scan upload check: %w", err)
	}
	if projectID.Valid {
		item.ProjectID = &projectID.String
	}
	if uploadedBy.Valid {
		item.UploadedByUserID = &uploadedBy.String
	}
	if visitorID.Valid {
		item.ShareVisitorID = &visitorID.String
	}
	if resultCode.Valid {
		item.ResultCode = &resultCode.String
	}
	if message.Valid {
		item.Message = &message.String
	}
	var err error
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return UploadCheck{}, fmt.Errorf("parse upload check creation time: %w", err)
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return UploadCheck{}, fmt.Errorf("parse upload check update time: %w", err)
	}
	item.CompletedAt, err = parseNullableTime(completedAt)
	if err != nil {
		return UploadCheck{}, err
	}
	item.QuarantinedAt, err = parseNullableTime(quarantinedAt)
	if err != nil {
		return UploadCheck{}, err
	}
	item.RejectedAt, err = parseNullableTime(rejectedAt)
	if err != nil {
		return UploadCheck{}, err
	}
	return item, nil
}

func requireActiveProject(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projectID string,
) error {
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM projects
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, projectID, workspaceID).Scan(&status)
	if err != nil || status != "active" {
		return ErrLibraryProjectInvalid
	}
	return nil
}

func requireActiveAsset(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	assetID string,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM assets
		WHERE id = ?
			AND workspace_id = ?
			AND status = 'active'
			AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("validate media asset: %w", err)
	}
	if count != 1 {
		return ErrLibraryAssetNotFound
	}
	return nil
}

func upsertActiveProjectAsset(
	ctx context.Context,
	tx *sql.Tx,
	record projectAssetRecord,
) error {
	addedBy := strings.TrimSpace(record.UserID)
	if addedBy == "" {
		var err error
		addedBy, err = assetCreatedBy(ctx, tx, record.WorkspaceID, record.AssetID)
		if err != nil {
			return err
		}
	}

	var existingID, status string
	err := tx.QueryRowContext(ctx, `
		SELECT id, status
		FROM project_assets
		WHERE workspace_id = ?
			AND project_id = ?
			AND asset_id = ?
	`, record.WorkspaceID, record.ProjectID, record.AssetID).Scan(
		&existingID,
		&status,
	)
	timestamp := formatTime(record.Now)
	if err == nil {
		if status == "active" {
			return nil
		}
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE project_assets
			SET status = 'active',
				added_by = ?,
				trashed_by = NULL,
				trashed_at = NULL,
				trash_expires_at = NULL,
				restored_by = ?,
				restored_at = ?,
				updated_at = ?
			WHERE id = ?
				AND workspace_id = ?
		`, addedBy, addedBy, timestamp, timestamp,
			existingID, record.WorkspaceID); updateErr != nil {
			return fmt.Errorf("restore project asset relation: %w", updateErr)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read project asset relation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_assets (
			id, workspace_id, project_id, asset_id, added_by, status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'active', ?, ?)
	`, record.ID, record.WorkspaceID, record.ProjectID, record.AssetID,
		addedBy, timestamp, timestamp); err != nil {
		return fmt.Errorf("create project asset relation: %w", err)
	}
	return nil
}

func activeProjectAsset(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projectID string,
	assetID string,
) (ProjectAsset, error) {
	item, err := scanProjectAsset(tx.QueryRowContext(
		ctx,
		projectAssetSelect()+`
		WHERE pa.workspace_id = ?
			AND pa.project_id = ?
			AND pa.asset_id = ?
			AND pa.status = 'active'
			AND p.deleted_at IS NULL
			AND a.deleted_at IS NULL
	`, workspaceID, projectID, assetID))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectAsset{}, ErrProjectAssetNotFound
	}
	return item, err
}

func trashProjectAsset(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projectID string,
	assetID string,
	userID string,
	now time.Time,
) (ProjectAsset, error) {
	timestamp := formatTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE project_assets
		SET status = 'trashed',
			trashed_by = ?,
			trashed_at = ?,
			trash_expires_at = ?,
			updated_at = ?
		WHERE workspace_id = ?
			AND project_id = ?
			AND asset_id = ?
			AND status = 'active'
	`, userID, timestamp, formatTime(now.AddDate(0, 0, 30)), timestamp,
		workspaceID, projectID, assetID)
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("trash project asset: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ProjectAsset{}, fmt.Errorf("read trash project asset result: %w", err)
	}
	if affected == 0 {
		return ProjectAsset{}, ErrProjectAssetNotFound
	}
	item, err := scanProjectAsset(tx.QueryRowContext(
		ctx,
		projectAssetSelect()+`
		WHERE pa.workspace_id = ?
			AND pa.project_id = ?
			AND pa.asset_id = ?
			AND pa.status = 'trashed'
	`, workspaceID, projectID, assetID))
	if err != nil {
		return ProjectAsset{}, err
	}
	return item, nil
}

func setAssetPreferredProject(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	assetID string,
	projectID *string,
	now time.Time,
) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET project_id = ?, revision = revision + 1, updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, projectID, formatTime(now), assetID, workspaceID)
	if err != nil {
		return fmt.Errorf("update asset preferred project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read asset preferred project result: %w", err)
	}
	if affected == 0 {
		return ErrLibraryAssetNotFound
	}
	return nil
}

func resetAssetPreferredProject(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	assetID string,
	now time.Time,
) error {
	var next sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT project_id
		FROM project_assets
		WHERE workspace_id = ?
			AND asset_id = ?
			AND status = 'active'
		ORDER BY created_at
		LIMIT 1
	`, workspaceID, assetID).Scan(&next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read next asset project: %w", err)
	}
	var projectID *string
	if next.Valid {
		projectID = &next.String
	}
	return setAssetPreferredProject(ctx, tx, workspaceID, assetID, projectID, now)
}

func assetCreatedBy(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	assetID string,
) (string, error) {
	var userID string
	err := tx.QueryRowContext(ctx, `
		SELECT created_by
		FROM assets
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrLibraryAssetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read asset creator: %w", err)
	}
	return userID, nil
}

func (repository *SQLiteLibraryRepository) ListVersions(
	ctx context.Context,
	workspaceID string,
	assetID string,
) ([]AssetVersion, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			av.id, av.asset_id, av.version_number, av.label, av.note,
			av.processing_status, av.source_filename, av.source_mime,
			av.source_size_bytes, av.source_fingerprint,
			vf.storage_object_id, so.authorized_root_id,
			so.object_key, so.status, av.created_at,
			CASE WHEN a.current_version_id = av.id THEN 1 ELSE 0 END
		FROM asset_versions av
		JOIN assets a ON a.id = av.asset_id
		JOIN version_files vf
			ON vf.asset_version_id = av.id
			AND vf.role = 'primary'
		JOIN storage_objects so ON so.id = vf.storage_object_id
		WHERE av.workspace_id = ?
			AND av.asset_id = ?
			AND av.deleted_at IS NULL
			AND a.workspace_id = ?
			AND a.deleted_at IS NULL
		ORDER BY av.version_number DESC
	`, workspaceID, assetID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list asset versions: %w", err)
	}
	defer rows.Close()

	versions := make([]AssetVersion, 0)
	objectIDs := make([]string, 0)
	byObject := make(map[string][]int)
	for rows.Next() {
		var item AssetVersion
		var label, note, sourceMIME sql.NullString
		var createdAt string
		var current int
		if err := rows.Scan(
			&item.ID,
			&item.AssetID,
			&item.VersionNumber,
			&label,
			&note,
			&item.ProcessingStatus,
			&item.SourceFilename,
			&sourceMIME,
			&item.SourceSizeBytes,
			&item.SourceFingerprint,
			&item.StorageObjectID,
			&item.AuthorizedRootID,
			&item.StorageObjectKey,
			&item.StorageObjectStatus,
			&createdAt,
			&current,
		); err != nil {
			return nil, fmt.Errorf("scan asset version: %w", err)
		}
		if label.Valid {
			item.Label = &label.String
		}
		if note.Valid {
			item.Note = &note.String
		}
		if sourceMIME.Valid {
			item.SourceMIME = &sourceMIME.String
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse asset version creation time: %w", parseErr)
		}
		item.CreatedAt = parsed
		item.IsCurrent = current == 1
		if _, ok := byObject[item.StorageObjectID]; !ok {
			objectIDs = append(objectIDs, item.StorageObjectID)
		}
		byObject[item.StorageObjectID] = append(
			byObject[item.StorageObjectID],
			len(versions),
		)
		versions = append(versions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset versions: %w", err)
	}
	if len(versions) == 0 {
		return nil, ErrLibraryAssetNotFound
	}
	if err := repository.loadVersionProbes(ctx, workspaceID, objectIDs, versions, byObject); err != nil {
		return nil, err
	}
	if err := repository.loadVersionRenditions(ctx, workspaceID, objectIDs, versions, byObject); err != nil {
		return nil, err
	}
	if err := repository.loadVersionUploadChecks(ctx, workspaceID, objectIDs, versions, byObject); err != nil {
		return nil, err
	}
	for index := range versions {
		normalizeVersionProcessingStatus(&versions[index])
	}
	return versions, nil
}

func normalizeVersionProcessingStatus(version *AssetVersion) {
	switch AssetVersionProcessingStage(*version) {
	case ProcessingStageComplete:
		version.ProcessingStatus = "ready"
	case ProcessingStagePreviewReady,
		ProcessingStageOptimizing,
		ProcessingStageEnhancementFailed:
		version.ProcessingStatus = "partial"
	case ProcessingStageProcessing:
		version.ProcessingStatus = "processing"
	case ProcessingStageFailed:
		version.ProcessingStatus = "failed"
	default:
		version.ProcessingStatus = "pending"
	}
}

func imageProfilesReady(
	profiles []ImageProfile,
	readyProfiles map[string]struct{},
) bool {
	for _, profile := range profiles {
		if _, ok := readyProfiles[profile.Kind+":"+profile.Key]; !ok {
			return false
		}
	}
	return true
}

func videoProfilesReady(
	profiles []VideoProfile,
	readyProfiles map[string]struct{},
) bool {
	for _, profile := range profiles {
		if _, ok := readyProfiles[profile.Kind+":"+profile.Key]; !ok {
			return false
		}
	}
	return true
}

func (repository *SQLiteLibraryRepository) SetCurrentVersion(
	ctx context.Context,
	workspaceID string,
	assetID string,
	versionID string,
	revision int,
	now time.Time,
) (LibraryAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return LibraryAsset{}, fmt.Errorf("begin set current asset version: %w", err)
	}
	defer tx.Rollback()

	var currentRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT revision
		FROM assets
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LibraryAsset{}, ErrLibraryAssetNotFound
		}
		return LibraryAsset{}, fmt.Errorf("read asset revision: %w", err)
	}
	if currentRevision != revision {
		return LibraryAsset{}, ErrLibraryRevisionConflict
	}

	var assetType string
	if err := tx.QueryRowContext(ctx, `
		SELECT CASE
			WHEN source_mime LIKE 'video/%' THEN 'video'
			WHEN source_mime LIKE 'image/%' THEN 'image'
			WHEN source_mime LIKE 'audio/%' THEN 'audio'
			WHEN source_mime = 'application/pdf' THEN 'pdf'
			ELSE 'other'
		END
		FROM asset_versions
		WHERE id = ?
			AND asset_id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, versionID, assetID, workspaceID).Scan(&assetType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LibraryAsset{}, ErrLibraryVersionNotFound
		}
		return LibraryAsset{}, fmt.Errorf("read asset version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET current_version_id = ?, type = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, versionID, assetType, formatTime(now), assetID, workspaceID); err != nil {
		return LibraryAsset{}, fmt.Errorf("set current asset version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LibraryAsset{}, fmt.Errorf("commit current asset version: %w", err)
	}
	return repository.assetByID(ctx, workspaceID, assetID)
}

func (repository *SQLiteLibraryRepository) UpdateAssetName(
	ctx context.Context,
	workspaceID string,
	assetID string,
	name string,
	revision int,
	now time.Time,
) (LibraryAsset, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return LibraryAsset{}, fmt.Errorf("begin update asset name: %w", err)
	}
	defer tx.Rollback()

	var currentRevision int
	if err := tx.QueryRowContext(ctx, `
		SELECT revision
		FROM assets
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, assetID, workspaceID).Scan(&currentRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LibraryAsset{}, ErrLibraryAssetNotFound
		}
		return LibraryAsset{}, fmt.Errorf("read asset revision: %w", err)
	}
	if currentRevision != revision {
		return LibraryAsset{}, ErrLibraryRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET name = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, name, formatTime(now), assetID, workspaceID); err != nil {
		return LibraryAsset{}, fmt.Errorf("update asset name: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LibraryAsset{}, fmt.Errorf("commit update asset name: %w", err)
	}
	return repository.assetByID(ctx, workspaceID, assetID)
}

type managedRootRecord struct {
	WorkspaceID     string
	ProviderID      string
	RootID          string
	PathSecretID    string
	ManagedRootPath string
	Now             time.Time
}

func (repository *SQLiteLibraryRepository) ensureManagedRoot(
	ctx context.Context,
	tx *sql.Tx,
	record managedRootRecord,
) (string, string, error) {
	if strings.TrimSpace(record.ManagedRootPath) == "" {
		if record.RootID == "" || record.ProviderID == "" {
			return "", "", ErrLibraryUploadTargetInvalid
		}
		var providerID string
		err := tx.QueryRowContext(ctx, `
			SELECT storage_provider_id
			FROM authorized_roots
			WHERE id = ?
				AND workspace_id = ?
				AND storage_provider_id = ?
				AND status = 'available'
				AND deleted_at IS NULL
		`, record.RootID, record.WorkspaceID, record.ProviderID).Scan(&providerID)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrLibraryUploadTargetInvalid
		}
		if err != nil {
			return "", "", fmt.Errorf("read selected upload root: %w", err)
		}
		return record.RootID, providerID, nil
	}

	desiredPath := filepath.Clean(record.ManagedRootPath)
	rootID := record.RootID
	providerID := record.ProviderID
	var storedPath string
	err := tx.QueryRowContext(ctx, `
		SELECT
			authorized_roots.id,
			authorized_roots.storage_provider_id,
			local_path_secrets.path_text
		FROM authorized_roots
		JOIN local_path_secrets
			ON local_path_secrets.id = authorized_roots.path_secret_ref
			AND local_path_secrets.workspace_id = authorized_roots.workspace_id
		WHERE authorized_roots.workspace_id = ?
			AND authorized_roots.purpose = 'managed_versions'
			AND authorized_roots.deleted_at IS NULL
	`, record.WorkspaceID).Scan(&rootID, &providerID, &storedPath)
	if err == nil {
		if filepath.Clean(storedPath) != desiredPath {
			if err := relinkManagedRootPath(
				ctx, tx, record.WorkspaceID, rootID, desiredPath, record.Now,
			); err != nil {
				return "", "", err
			}
		}
		return rootID, providerID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("read managed upload root: %w", err)
	}

	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO storage_providers (
			id, workspace_id, kind, name, status, config_json,
			capabilities_json, created_at, updated_at
		) VALUES (?, ?, 'local', 'Review Studio Managed Uploads',
			'active', '{}', '{"read":true,"write":true}', ?, ?)
	`, providerID, record.WorkspaceID, timestamp, timestamp); err != nil {
		return "", "", fmt.Errorf("create managed upload provider: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_path_secrets (
			id, workspace_id, path_text, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
	`, record.PathSecretID, record.WorkspaceID, record.ManagedRootPath,
		timestamp, timestamp); err != nil {
		return "", "", fmt.Errorf("create managed upload path: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO authorized_roots (
			id, workspace_id, storage_provider_id, display_name,
			display_path, path_secret_ref, mode, scan_enabled,
			status, revision, created_at, updated_at, purpose
		) VALUES (?, ?, ?, '托管上传',
			'Core managed storage', ?, 'managed', 0,
			'available', 1, ?, ?, 'managed_versions')
	`, rootID, record.WorkspaceID, providerID, record.PathSecretID,
		timestamp, timestamp); err != nil {
		return "", "", fmt.Errorf("create managed upload root: %w", err)
	}
	return rootID, providerID, nil
}

// relinkManagedRootPath points an app-managed root at the path derived from this
// instance's data directory.
//
// The path of a managed_versions root is never chosen by a user: it is the
// managed source area of the running instance. A backup restored into another
// data directory carries the source instance's absolute path in
// local_path_secrets, and reusing that root as-is would keep writing new
// originals into the source instance's directory. Only app-derived roots are
// relinked here; user-created buckets keep the path the owner selected, because
// nothing in the database can tell where those should point after a move.
func relinkManagedRootPath(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	rootID string,
	desiredPath string,
	now time.Time,
) error {
	timestamp := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE local_path_secrets
		SET path_text = ?, updated_at = ?
		WHERE workspace_id = ?
			AND id = (
				SELECT path_secret_ref
				FROM authorized_roots
				WHERE id = ? AND workspace_id = ?
			)
	`, desiredPath, timestamp, workspaceID, rootID, workspaceID); err != nil {
		return fmt.Errorf("relink managed upload path: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ?
	`, timestamp, rootID, workspaceID); err != nil {
		return fmt.Errorf("bump relinked managed upload root: %w", err)
	}
	return nil
}

func (repository *SQLiteLibraryRepository) CreateVersion(
	ctx context.Context,
	record createAssetVersionRecord,
) (AssetVersion, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AssetVersion{}, fmt.Errorf("begin create asset version: %w", err)
	}
	defer tx.Rollback()

	var currentRevision int
	var currentType string
	if err := tx.QueryRowContext(ctx, `
		SELECT revision, type
		FROM assets
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, record.AssetID, record.WorkspaceID).Scan(
		&currentRevision,
		&currentType,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AssetVersion{}, ErrLibraryAssetNotFound
		}
		return AssetVersion{}, fmt.Errorf("read asset for new version: %w", err)
	}
	if currentRevision != record.Revision {
		return AssetVersion{}, ErrLibraryRevisionConflict
	}
	if incomingType := libraryAssetType(record.MIMEType); incomingType != currentType {
		return AssetVersion{}, ErrLibraryUploadInvalid
	}

	var versionNumber int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_number), 0) + 1
		FROM asset_versions
		WHERE asset_id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, record.AssetID, record.WorkspaceID).Scan(&versionNumber); err != nil {
		return AssetVersion{}, fmt.Errorf("read next asset version number: %w", err)
	}

	rootID, providerID, err := repository.ensureManagedRoot(ctx, tx, managedRootRecord{
		WorkspaceID:     record.WorkspaceID,
		ProviderID:      record.ProviderID,
		RootID:          record.RootID,
		PathSecretID:    record.PathSecretID,
		ManagedRootPath: record.ManagedRootPath,
		Now:             record.Now,
	})
	if err != nil {
		return AssetVersion{}, err
	}

	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO storage_objects (
			id, workspace_id, storage_provider_id, authorized_root_id,
			object_key, kind, status, size_bytes, modified_at,
			quick_fingerprint, mime_type, first_discovered_at,
			last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'source', 'available', ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.StorageObjectID, record.WorkspaceID, providerID, rootID,
		record.ObjectKey, record.Observed.SizeBytes,
		formatTime(record.Observed.ModifiedAt), record.Observed.QuickFingerprint,
		record.MIMEType, timestamp, timestamp, timestamp, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("create managed source object: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions (
			id, workspace_id, asset_id, version_number, label, note,
			processing_status, source_filename, source_mime,
			source_size_bytes, source_fingerprint, media_metadata_json,
			created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, '{}', ?, ?)
	`, record.VersionID, record.WorkspaceID, record.AssetID, versionNumber,
		record.Label, record.Note, record.Filename, record.MIMEType,
		record.Observed.SizeBytes, record.Observed.QuickFingerprint,
		record.UserID, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("create uploaded asset version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (?, ?, ?, ?, 'primary', ?)
	`, record.VersionFileID, record.WorkspaceID, record.VersionID,
		record.StorageObjectID, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("link uploaded asset version: %w", err)
	}
	if err := insertUploadCheck(ctx, tx, uploadCheckRecord{
		ID:                   record.UploadCheckID,
		WorkspaceID:          record.WorkspaceID,
		ProjectID:            record.ProjectID,
		AssetID:              record.AssetID,
		AssetVersionID:       record.VersionID,
		StorageObjectID:      record.StorageObjectID,
		UploadedByUserID:     &record.UserID,
		SourceType:           "user",
		UploadSecurityPolicy: record.UploadSecurityPolicy,
		Status:               record.UploadCheckStatus,
		ResultCode:           record.UploadCheckResultCode,
		Message:              record.UploadCheckMessage,
		Now:                  record.Now,
	}); err != nil {
		return AssetVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE assets
		SET current_version_id = ?, type = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, record.VersionID, libraryAssetType(record.MIMEType), timestamp,
		record.AssetID, record.WorkspaceID); err != nil {
		return AssetVersion{}, fmt.Errorf("activate uploaded asset version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AssetVersion{}, fmt.Errorf("commit uploaded asset version: %w", err)
	}

	items, err := repository.ListVersions(ctx, record.WorkspaceID, record.AssetID)
	if err != nil {
		return AssetVersion{}, err
	}
	for _, item := range items {
		if item.ID == record.VersionID {
			return item, nil
		}
	}
	return AssetVersion{}, ErrLibraryVersionNotFound
}

type uploadCheckRecord struct {
	ID                   string
	WorkspaceID          string
	ProjectID            *string
	AssetID              string
	AssetVersionID       string
	StorageObjectID      string
	UploadedByUserID     *string
	ShareVisitorID       *string
	SourceType           string
	UploadSecurityPolicy string
	Status               string
	ResultCode           string
	Message              *string
	Now                  time.Time
}

func insertUploadCheck(
	ctx context.Context,
	tx *sql.Tx,
	record uploadCheckRecord,
) error {
	sourceType := strings.TrimSpace(record.SourceType)
	if sourceType == "" {
		sourceType = "user"
	}
	policy := normalizeUploadSecurityPolicy(record.UploadSecurityPolicy)
	status := normalizeUploadCheckStatus(record.Status)
	resultCode := strings.TrimSpace(record.ResultCode)
	if resultCode == "" {
		resultCode = "type_checks_passed"
	}
	timestamp := formatTime(record.Now)
	var completedAt any = timestamp
	var quarantinedAt any
	var rejectedAt any
	if status == "quarantined" {
		quarantinedAt = timestamp
	}
	if status == "rejected" {
		rejectedAt = timestamp
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO upload_checks (
			id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ProjectID, record.AssetID,
		record.AssetVersionID, record.StorageObjectID,
		record.UploadedByUserID, record.ShareVisitorID, sourceType, policy,
		status, resultCode, nullableStringPtr(record.Message), timestamp,
		timestamp, completedAt, quarantinedAt, rejectedAt); err != nil {
		return fmt.Errorf("create upload check: %w", err)
	}
	return nil
}

func normalizeUploadCheckStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "uploaded", "checking", "processing", "ready", "quarantined", "rejected":
		return strings.TrimSpace(value)
	default:
		return "ready"
	}
}

func (repository *SQLiteLibraryRepository) ListUploadChecks(
	ctx context.Context,
	workspaceID string,
	query UploadCheckQuery,
) (UploadCheckPage, error) {
	page := query.Page
	if page <= 0 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	where := []string{"uc.workspace_id = ?"}
	args := []any{workspaceID}
	if strings.TrimSpace(query.Status) != "" {
		where = append(where, "uc.status = ?")
		args = append(args, strings.TrimSpace(query.Status))
	}
	if strings.TrimSpace(query.ProjectID) != "" {
		where = append(where, "uc.project_id = ?")
		args = append(args, strings.TrimSpace(query.ProjectID))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM upload_checks uc
		WHERE `+whereSQL,
		args...,
	).Scan(&total); err != nil {
		return UploadCheckPage{}, fmt.Errorf("count upload checks: %w", err)
	}

	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			uc.id, uc.workspace_id, uc.project_id, uc.asset_id,
			uc.asset_version_id, uc.storage_object_id,
			uc.uploaded_by_user_id, uc.share_visitor_id, uc.source_type,
			uc.upload_security_policy, uc.status, uc.result_code, uc.message,
			uc.created_at, uc.updated_at, uc.completed_at, uc.quarantined_at,
			uc.rejected_at,
			assets.name,
			asset_versions.source_filename,
			asset_versions.source_size_bytes,
			projects.name,
			users.display_name
		FROM upload_checks uc
		JOIN assets
			ON assets.workspace_id = uc.workspace_id
			AND assets.id = uc.asset_id
		JOIN asset_versions
			ON asset_versions.workspace_id = uc.workspace_id
			AND asset_versions.id = uc.asset_version_id
		LEFT JOIN projects
			ON projects.workspace_id = uc.workspace_id
			AND projects.id = uc.project_id
		LEFT JOIN users
			ON users.id = uc.uploaded_by_user_id
		WHERE `+whereSQL+`
		ORDER BY uc.updated_at DESC, uc.created_at DESC
		LIMIT ? OFFSET ?
	`, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return UploadCheckPage{}, fmt.Errorf("list upload checks: %w", err)
	}
	defer rows.Close()

	items := make([]UploadCheckListItem, 0)
	for rows.Next() {
		item, scanErr := scanUploadCheckListItem(rows)
		if scanErr != nil {
			return UploadCheckPage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return UploadCheckPage{}, fmt.Errorf("iterate upload checks: %w", err)
	}
	return UploadCheckPage{
		Items: items, Total: total, Page: page, PageSize: pageSize,
	}, nil
}

func scanUploadCheckListItem(scanner rowScanner) (UploadCheckListItem, error) {
	var item UploadCheckListItem
	var projectID, uploadedBy, visitorID sql.NullString
	var resultCode, message sql.NullString
	var createdAt, updatedAt string
	var completedAt, quarantinedAt, rejectedAt sql.NullString
	var projectName, uploadedByName sql.NullString
	if err := scanner.Scan(
		&item.Check.ID,
		&item.Check.WorkspaceID,
		&projectID,
		&item.Check.AssetID,
		&item.Check.AssetVersionID,
		&item.Check.StorageObjectID,
		&uploadedBy,
		&visitorID,
		&item.Check.SourceType,
		&item.Check.UploadSecurityPolicy,
		&item.Check.Status,
		&resultCode,
		&message,
		&createdAt,
		&updatedAt,
		&completedAt,
		&quarantinedAt,
		&rejectedAt,
		&item.AssetName,
		&item.SourceFilename,
		&item.SourceSizeBytes,
		&projectName,
		&uploadedByName,
	); err != nil {
		return UploadCheckListItem{}, fmt.Errorf("scan upload check list item: %w", err)
	}
	if projectID.Valid {
		item.Check.ProjectID = &projectID.String
	}
	if uploadedBy.Valid {
		item.Check.UploadedByUserID = &uploadedBy.String
	}
	if visitorID.Valid {
		item.Check.ShareVisitorID = &visitorID.String
	}
	if resultCode.Valid {
		item.Check.ResultCode = &resultCode.String
	}
	if message.Valid {
		item.Check.Message = &message.String
	}
	var err error
	if item.Check.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return UploadCheckListItem{}, fmt.Errorf("parse upload check creation time: %w", err)
	}
	if item.Check.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return UploadCheckListItem{}, fmt.Errorf("parse upload check update time: %w", err)
	}
	item.Check.CompletedAt, err = parseNullableTime(completedAt)
	if err != nil {
		return UploadCheckListItem{}, err
	}
	item.Check.QuarantinedAt, err = parseNullableTime(quarantinedAt)
	if err != nil {
		return UploadCheckListItem{}, err
	}
	item.Check.RejectedAt, err = parseNullableTime(rejectedAt)
	if err != nil {
		return UploadCheckListItem{}, err
	}
	if projectName.Valid {
		item.ProjectName = &projectName.String
	}
	if uploadedByName.Valid {
		item.UploadedByName = &uploadedByName.String
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) ResolveUploadCheckForProcessing(
	ctx context.Context,
	record resolveUploadCheckRecord,
) (UploadCheck, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return UploadCheck{}, fmt.Errorf("begin resolve upload check: %w", err)
	}
	defer tx.Rollback()

	check, err := scanUploadCheck(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		FROM upload_checks
		WHERE workspace_id = ? AND id = ?
	`, record.WorkspaceID, record.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return UploadCheck{}, ErrUploadCheckNotFound
	}
	if err != nil {
		return UploadCheck{}, err
	}
	if check.Status != "quarantined" {
		return UploadCheck{}, ErrUploadCheckConflict
	}

	var sourceFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT source_fingerprint
		FROM asset_versions
		WHERE workspace_id = ? AND id = ? AND deleted_at IS NULL
	`, record.WorkspaceID, check.AssetVersionID).Scan(&sourceFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return UploadCheck{}, ErrLibraryVersionNotFound
	}
	if err != nil {
		return UploadCheck{}, fmt.Errorf("load upload check asset version: %w", err)
	}
	payload, err := json.Marshal(ProcessAssetVersionPayload{
		VersionID:         check.AssetVersionID,
		StorageObjectID:   check.StorageObjectID,
		SourceFingerprint: sourceFingerprint,
	})
	if err != nil {
		return UploadCheck{}, fmt.Errorf("marshal upload check processing job: %w", err)
	}

	resultCode := strings.TrimSpace(record.ResultCode)
	if resultCode == "" {
		resultCode = "manual_released"
	}
	timestamp := formatTime(record.Now)
	result, err := tx.ExecContext(ctx, `
		UPDATE upload_checks
		SET status = 'ready',
			result_code = ?,
			message = ?,
			updated_at = ?,
			completed_at = ?,
			rejected_at = NULL
		WHERE workspace_id = ?
			AND id = ?
			AND status = 'quarantined'
	`, resultCode, nullableStringPtr(record.Message), timestamp, timestamp,
		record.WorkspaceID, record.ID)
	if err != nil {
		return UploadCheck{}, fmt.Errorf("resolve upload check status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UploadCheck{}, fmt.Errorf("inspect upload check release: %w", err)
	}
	if affected == 0 {
		return UploadCheck{}, ErrUploadCheckConflict
	}

	idempotencyKey := fmt.Sprintf("process-asset-version:%s:v1", check.AssetVersionID)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO jobs (
			id, workspace_id, type, status, priority, idempotency_key,
			subject_type, subject_id, payload_json, available_at,
			max_attempts, attempt_count, created_at, updated_at
		) VALUES (?, ?, ?, 'queued', 12, ?, 'storageObject', ?, ?, ?, 3, 0, ?, ?)
	`, record.JobID, record.WorkspaceID, ProcessAssetVersionJobType,
		idempotencyKey, check.StorageObjectID, string(payload), timestamp,
		timestamp, timestamp); err != nil {
		return UploadCheck{}, fmt.Errorf("create upload check processing job: %w", err)
	}

	updated, err := scanUploadCheck(tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		FROM upload_checks
		WHERE workspace_id = ? AND id = ?
	`, record.WorkspaceID, record.ID))
	if err != nil {
		return UploadCheck{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadCheck{}, fmt.Errorf("commit resolved upload check: %w", err)
	}
	return updated, nil
}

func (repository *SQLiteLibraryRepository) UpdateUploadCheckStatus(
	ctx context.Context,
	workspaceID string,
	id string,
	status string,
	resultCode string,
	message *string,
	now time.Time,
) (UploadCheck, error) {
	status = normalizeUploadCheckStatus(status)
	if status != "ready" && status != "rejected" {
		return UploadCheck{}, ErrUploadCheckInvalid
	}
	resultCode = strings.TrimSpace(resultCode)
	if resultCode == "" {
		if status == "rejected" {
			resultCode = "manual_rejected"
		} else {
			resultCode = "manual_released"
		}
	}
	timestamp := formatTime(now)
	var rejectedAt any
	if status == "rejected" {
		rejectedAt = timestamp
	}
	result, err := repository.db.ExecContext(ctx, `
		UPDATE upload_checks
		SET status = ?,
			result_code = ?,
			message = ?,
			updated_at = ?,
			completed_at = ?,
			rejected_at = CASE WHEN ? = 'rejected' THEN ? ELSE rejected_at END
		WHERE workspace_id = ?
			AND id = ?
			AND status = 'quarantined'
	`, status, resultCode, nullableStringPtr(message), timestamp, timestamp,
		status, rejectedAt, workspaceID, id)
	if err != nil {
		return UploadCheck{}, fmt.Errorf("update upload check status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UploadCheck{}, fmt.Errorf("inspect upload check update: %w", err)
	}
	if affected == 0 {
		var existing string
		err := repository.db.QueryRowContext(ctx, `
			SELECT status
			FROM upload_checks
			WHERE workspace_id = ? AND id = ?
		`, workspaceID, id).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			return UploadCheck{}, ErrUploadCheckNotFound
		}
		if err != nil {
			return UploadCheck{}, fmt.Errorf("load upload check status: %w", err)
		}
		return UploadCheck{}, ErrUploadCheckConflict
	}

	check, err := repository.getUploadCheck(ctx, workspaceID, id)
	if err != nil {
		return UploadCheck{}, err
	}
	return check, nil
}

func (repository *SQLiteLibraryRepository) getUploadCheck(
	ctx context.Context,
	workspaceID string,
	id string,
) (UploadCheck, error) {
	check, err := scanUploadCheck(repository.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		FROM upload_checks
		WHERE workspace_id = ? AND id = ?
	`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return UploadCheck{}, ErrUploadCheckNotFound
	}
	return check, err
}

func nullableStringPtr(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func (repository *SQLiteLibraryRepository) CreateUploadedAsset(
	ctx context.Context,
	record createUploadedAssetRecord,
) (AssetVersion, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return AssetVersion{}, fmt.Errorf("begin create uploaded asset: %w", err)
	}
	defer tx.Rollback()

	if record.ProjectID != nil {
		var status string
		err := tx.QueryRowContext(ctx, `
			SELECT status FROM projects
			WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
		`, *record.ProjectID, record.WorkspaceID).Scan(&status)
		if err != nil || status != "active" {
			return AssetVersion{}, ErrLibraryProjectInvalid
		}
	}

	rootID, providerID, err := repository.ensureManagedRoot(ctx, tx, managedRootRecord{
		WorkspaceID:     record.WorkspaceID,
		ProviderID:      record.ProviderID,
		RootID:          record.RootID,
		PathSecretID:    record.PathSecretID,
		ManagedRootPath: record.ManagedRootPath,
		Now:             record.Now,
	})
	if err != nil {
		return AssetVersion{}, err
	}

	timestamp := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO storage_objects (
			id, workspace_id, storage_provider_id, authorized_root_id,
			object_key, kind, status, size_bytes, modified_at,
			quick_fingerprint, mime_type, first_discovered_at,
			last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'source', 'available', ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.StorageObjectID, record.WorkspaceID, providerID, rootID,
		record.ObjectKey, record.Observed.SizeBytes,
		formatTime(record.Observed.ModifiedAt), record.Observed.QuickFingerprint,
		record.MIMEType, timestamp, timestamp, timestamp, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("create uploaded asset object: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO assets (
			id, workspace_id, project_id, type, name, current_version_id,
			origin_storage_object_id, status, created_by, revision,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, 1, ?, ?)
	`, record.AssetID, record.WorkspaceID, record.ProjectID,
		libraryAssetType(record.MIMEType), record.Filename, record.VersionID,
		record.StorageObjectID, record.UserID, timestamp, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("create uploaded asset: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions (
			id, workspace_id, asset_id, version_number, label, note,
			processing_status, source_filename, source_mime,
			source_size_bytes, source_fingerprint, media_metadata_json,
			created_by, created_at
		) VALUES (?, ?, ?, 1, ?, ?, 'pending', ?, ?, ?, ?, '{}', ?, ?)
	`, record.VersionID, record.WorkspaceID, record.AssetID,
		record.Label, record.Note, record.Filename, record.MIMEType,
		record.Observed.SizeBytes, record.Observed.QuickFingerprint,
		record.UserID, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("create uploaded asset version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO version_files (
			id, workspace_id, asset_version_id, storage_object_id, role, created_at
		) VALUES (?, ?, ?, ?, 'primary', ?)
	`, record.VersionFileID, record.WorkspaceID, record.VersionID,
		record.StorageObjectID, timestamp); err != nil {
		return AssetVersion{}, fmt.Errorf("link uploaded asset version: %w", err)
	}
	if record.ProjectID != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_assets (
				id, workspace_id, project_id, asset_id, added_by, status,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 'active', ?, ?)
		`, record.ProjectAssetID, record.WorkspaceID, *record.ProjectID,
			record.AssetID, record.UserID, timestamp, timestamp); err != nil {
			return AssetVersion{}, fmt.Errorf("link uploaded asset to project: %w", err)
		}
	}
	if err := insertUploadCheck(ctx, tx, uploadCheckRecord{
		ID:                   record.UploadCheckID,
		WorkspaceID:          record.WorkspaceID,
		ProjectID:            record.ProjectID,
		AssetID:              record.AssetID,
		AssetVersionID:       record.VersionID,
		StorageObjectID:      record.StorageObjectID,
		UploadedByUserID:     &record.UserID,
		SourceType:           "user",
		UploadSecurityPolicy: record.UploadSecurityPolicy,
		Status:               record.UploadCheckStatus,
		ResultCode:           record.UploadCheckResultCode,
		Message:              record.UploadCheckMessage,
		Now:                  record.Now,
	}); err != nil {
		return AssetVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return AssetVersion{}, fmt.Errorf("commit uploaded asset: %w", err)
	}

	items, err := repository.ListVersions(ctx, record.WorkspaceID, record.AssetID)
	if err != nil {
		return AssetVersion{}, err
	}
	for _, item := range items {
		if item.ID == record.VersionID {
			return item, nil
		}
	}
	return AssetVersion{}, ErrLibraryVersionNotFound
}

func (repository *SQLiteLibraryRepository) SetVersionProcessingStatus(
	ctx context.Context,
	workspaceID string,
	versionID string,
	status string,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE asset_versions
		SET processing_status = ?
		WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL
	`, status, versionID, workspaceID)
	if err != nil {
		return fmt.Errorf("update asset version processing status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read asset version processing update: %w", err)
	}
	if affected == 0 {
		return ErrLibraryVersionNotFound
	}
	return nil
}

func libraryAssetType(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case mimeType == "application/pdf":
		return "pdf"
	default:
		return "other"
	}
}

func (repository *SQLiteLibraryRepository) asset(
	ctx context.Context,
	workspaceID string,
	storageObjectID string,
) (LibraryAsset, error) {
	var item LibraryAsset
	var projectID, projectName sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			a.id,
			COALESCE((
				SELECT pa.project_id
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), a.project_id),
			COALESCE((
				SELECT pp.name
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), p.name),
			a.name, a.type, a.revision,
			av.id, av.version_number
		FROM version_files vf
		JOIN asset_versions av ON av.id = vf.asset_version_id
		JOIN assets a
			ON a.id = av.asset_id
			AND a.current_version_id = av.id
		LEFT JOIN projects p ON p.id = a.project_id AND p.deleted_at IS NULL
		WHERE vf.storage_object_id = ?
			AND vf.role = 'primary'
			AND a.workspace_id = ?
			AND a.deleted_at IS NULL
	`, storageObjectID, workspaceID).Scan(
		&item.ID,
		&projectID,
		&projectName,
		&item.Name,
		&item.Type,
		&item.Revision,
		&item.VersionID,
		&item.VersionNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryAsset{}, ErrLibraryAssetNotFound
	}
	if err != nil {
		return LibraryAsset{}, fmt.Errorf("read media library asset: %w", err)
	}
	if projectID.Valid {
		item.ProjectID = &projectID.String
	}
	if projectName.Valid {
		item.ProjectName = &projectName.String
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) assetByID(
	ctx context.Context,
	workspaceID string,
	assetID string,
) (LibraryAsset, error) {
	var item LibraryAsset
	var projectID, projectName sql.NullString
	err := repository.db.QueryRowContext(ctx, `
		SELECT
			a.id,
			COALESCE((
				SELECT pa.project_id
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), a.project_id),
			COALESCE((
				SELECT pp.name
				FROM project_assets pa
				JOIN projects pp ON pp.id = pa.project_id
				WHERE pa.asset_id = a.id
					AND pa.workspace_id = a.workspace_id
					AND pa.status = 'active'
					AND pp.deleted_at IS NULL
				ORDER BY pa.created_at
				LIMIT 1
			), p.name),
			a.name, a.type, a.revision,
			av.id, av.version_number
		FROM assets a
		JOIN asset_versions av ON av.id = a.current_version_id
		LEFT JOIN projects p ON p.id = a.project_id AND p.deleted_at IS NULL
		WHERE a.id = ?
			AND a.workspace_id = ?
			AND a.deleted_at IS NULL
			AND av.deleted_at IS NULL
	`, assetID, workspaceID).Scan(
		&item.ID,
		&projectID,
		&projectName,
		&item.Name,
		&item.Type,
		&item.Revision,
		&item.VersionID,
		&item.VersionNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryAsset{}, ErrLibraryAssetNotFound
	}
	if err != nil {
		return LibraryAsset{}, fmt.Errorf("read media library asset: %w", err)
	}
	if projectID.Valid {
		item.ProjectID = &projectID.String
	}
	if projectName.Valid {
		item.ProjectName = &projectName.String
	}
	return item, nil
}

func (repository *SQLiteLibraryRepository) loadVersionProbes(
	ctx context.Context,
	workspaceID string,
	ids []string,
	versions []AssetVersion,
	index map[string][]int,
) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			storage_object_id, workspace_id, status, source_fingerprint,
			media_type, format_name, format_long_name, duration_us,
			bit_rate, width, height, rotation_degrees, frame_rate,
			video_codec, audio_codec, raw_metadata_json, error_code,
			error_message, probed_at
		FROM media_probes
		WHERE workspace_id = ? AND storage_object_id IN (`+placeholders(len(ids))+`)
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load asset version probes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		probe, scanErr := scanMetadata(rows)
		if scanErr != nil {
			return scanErr
		}
		for _, position := range index[probe.StorageObjectID] {
			if versions[position].SourceFingerprint == probe.SourceFingerprint {
				item := probe
				versions[position].Probe = &item
			}
		}
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadVersionRenditions(
	ctx context.Context,
	workspaceID string,
	ids []string,
	versions []AssetVersion,
	index map[string][]int,
) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := repository.db.QueryContext(
		ctx,
		renditionSelect+`
		WHERE renditions.workspace_id = ?
			AND renditions.source_storage_object_id IN (`+placeholders(len(ids))+`)
		ORDER BY renditions.updated_at DESC
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load asset version renditions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		rendition, scanErr := scanRendition(rows)
		if scanErr != nil {
			return scanErr
		}
		for _, position := range index[rendition.SourceStorageObjectID] {
			if versions[position].SourceFingerprint == rendition.SourceFingerprint {
				versions[position].Renditions = append(
					versions[position].Renditions,
					rendition,
				)
			}
		}
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadVersionUploadChecks(
	ctx context.Context,
	workspaceID string,
	ids []string,
	versions []AssetVersion,
	index map[string][]int,
) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		FROM upload_checks
		WHERE workspace_id = ?
			AND storage_object_id IN (`+placeholders(len(ids))+`)
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load asset version upload checks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		check, scanErr := scanUploadCheck(rows)
		if scanErr != nil {
			return scanErr
		}
		for _, position := range index[check.StorageObjectID] {
			if versions[position].ID == check.AssetVersionID {
				item := check
				versions[position].UploadCheck = &item
			}
		}
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadProbes(
	ctx context.Context,
	workspaceID string,
	ids []string,
	items []LibraryItem,
	index map[string]int,
) error {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			storage_object_id, workspace_id, status, source_fingerprint,
			media_type, format_name, format_long_name, duration_us,
			bit_rate, width, height, rotation_degrees, frame_rate,
			video_codec, audio_codec, raw_metadata_json, error_code,
			error_message, probed_at
		FROM media_probes
		WHERE workspace_id = ? AND storage_object_id IN (`+placeholders(len(ids))+`)
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load media library probes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		probe, scanErr := scanMetadata(rows)
		if scanErr != nil {
			return scanErr
		}
		position := index[probe.StorageObjectID]
		items[position].Probe = &probe
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadRenditions(
	ctx context.Context,
	workspaceID string,
	ids []string,
	items []LibraryItem,
	index map[string]int,
) error {
	rows, err := repository.db.QueryContext(
		ctx,
		renditionSelect+`
		WHERE renditions.workspace_id = ?
			AND renditions.source_storage_object_id IN (`+placeholders(len(ids))+`)
		ORDER BY renditions.updated_at DESC
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load media library renditions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		rendition, scanErr := scanRendition(rows)
		if scanErr != nil {
			return scanErr
		}
		position := index[rendition.SourceStorageObjectID]
		items[position].Renditions = append(items[position].Renditions, rendition)
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadJobs(
	ctx context.Context,
	workspaceID string,
	ids []string,
	items []LibraryItem,
	index map[string]int,
) error {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			id, type, status, priority, subject_type, subject_id,
			progress_current, progress_total, progress_unit,
			last_error_code, last_error_message, attempt_count, max_attempts,
			created_at, updated_at, started_at, completed_at
		FROM jobs
		WHERE workspace_id = ?
			AND subject_type = 'storageObject'
			AND subject_id IN (`+placeholders(len(ids))+`)
			AND type IN (
				'media.generate_image_renditions',
				'media.generate_video_renditions',
				'media.generate_video_enhancements',
				'media.process_asset_version'
			)
		ORDER BY updated_at DESC
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load media library jobs: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]map[string]bool)
	for rows.Next() {
		item, scanErr := scanLibraryJob(rows)
		if scanErr != nil {
			return scanErr
		}
		if seen[item.SubjectID] == nil {
			seen[item.SubjectID] = make(map[string]bool)
		}
		if seen[item.SubjectID][item.Type] {
			continue
		}
		seen[item.SubjectID][item.Type] = true
		position := index[item.SubjectID]
		items[position].Jobs = append(items[position].Jobs, item)
	}
	return rows.Err()
}

func (repository *SQLiteLibraryRepository) loadUploadChecks(
	ctx context.Context,
	workspaceID string,
	ids []string,
	items []LibraryItem,
	index map[string]int,
) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := repository.db.QueryContext(ctx, `
		SELECT
			id, workspace_id, project_id, asset_id, asset_version_id,
			storage_object_id, uploaded_by_user_id, share_visitor_id,
			source_type, upload_security_policy, status, result_code,
			message, created_at, updated_at, completed_at, quarantined_at,
			rejected_at
		FROM upload_checks
		WHERE workspace_id = ?
			AND storage_object_id IN (`+placeholders(len(ids))+`)
	`, objectArguments(workspaceID, ids)...)
	if err != nil {
		return fmt.Errorf("load upload checks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		check, scanErr := scanUploadCheck(rows)
		if scanErr != nil {
			return scanErr
		}
		position, ok := index[check.StorageObjectID]
		if !ok {
			continue
		}
		item := check
		items[position].UploadCheck = &item
	}
	return rows.Err()
}

func scanLibraryObject(scanner rowScanner) (storage.StoredObject, error) {
	var item storage.StoredObject
	var modifiedAt, firstDiscoveredAt, createdAt, updatedAt string
	var lastSeenAt, missingSince sql.NullString
	if err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.StorageProviderID,
		&item.AuthorizedRootID, &item.ObjectKey, &item.Status,
		&item.SizeBytes, &modifiedAt, &item.QuickFingerprint,
		&item.MIMEType, &firstDiscoveredAt, &lastSeenAt, &missingSince,
		&createdAt, &updatedAt,
	); err != nil {
		return storage.StoredObject{}, fmt.Errorf("scan media library object: %w", err)
	}
	var err error
	if item.ModifiedAt, err = time.Parse(time.RFC3339Nano, modifiedAt); err != nil {
		return storage.StoredObject{}, fmt.Errorf("parse object modification time: %w", err)
	}
	if item.FirstDiscoveredAt, err = time.Parse(time.RFC3339Nano, firstDiscoveredAt); err != nil {
		return storage.StoredObject{}, fmt.Errorf("parse object discovery time: %w", err)
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return storage.StoredObject{}, fmt.Errorf("parse object creation time: %w", err)
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return storage.StoredObject{}, fmt.Errorf("parse object update time: %w", err)
	}
	item.LastSeenAt, err = parseNullableTime(lastSeenAt)
	if err != nil {
		return storage.StoredObject{}, err
	}
	item.MissingSince, err = parseNullableTime(missingSince)
	return item, err
}

func scanLibraryJob(scanner rowScanner) (LibraryJob, error) {
	var item LibraryJob
	var createdAt, updatedAt string
	var startedAt, completedAt sql.NullString
	err := scanner.Scan(
		&item.ID, &item.Type, &item.Status, &item.Priority,
		&item.SubjectType, &item.SubjectID,
		&item.ProgressCurrent, &item.ProgressTotal, &item.ProgressUnit,
		&item.ErrorCode, &item.ErrorMessage, &item.AttemptCount,
		&item.MaxAttempts, &createdAt, &updatedAt, &startedAt, &completedAt,
	)
	if err != nil {
		return LibraryJob{}, fmt.Errorf("scan media library job: %w", err)
	}
	if item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return LibraryJob{}, err
	}
	if item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return LibraryJob{}, err
	}
	item.StartedAt, err = parseNullableTime(startedAt)
	if err != nil {
		return LibraryJob{}, err
	}
	item.CompletedAt, err = parseNullableTime(completedAt)
	return item, err
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
