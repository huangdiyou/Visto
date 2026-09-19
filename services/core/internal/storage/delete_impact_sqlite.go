package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (repository *SQLiteRepository) ProviderDeleteImpact(
	ctx context.Context,
	workspaceID string,
	providerID string,
) (StorageLocationDeleteImpact, error) {
	provider, err := repository.Provider(ctx, workspaceID, providerID)
	if err != nil {
		return StorageLocationDeleteImpact{}, err
	}
	counts, err := repository.storageLocationImpactCounts(
		ctx,
		workspaceID,
		providerID,
		nil,
	)
	if err != nil {
		return StorageLocationDeleteImpact{}, err
	}
	impact := StorageLocationDeleteImpact{
		TargetType:   "provider",
		ProviderID:   provider.ID,
		ProviderName: provider.Name,
		ProviderKind: provider.Kind,
		CanDisable:   true,
		Counts:       counts,
	}
	impact.CanDelete = providerCanDelete(counts)
	impact.BlockingReasons = storageLocationBlockingReasons(
		impact.TargetType,
		counts,
	)
	return impact, nil
}

func (repository *SQLiteRepository) LocalManagedBucketDeleteImpact(
	ctx context.Context,
	workspaceID string,
	bucketID string,
) (StorageLocationDeleteImpact, error) {
	bucket, err := repository.LocalManagedBucket(ctx, workspaceID, bucketID)
	if err != nil {
		return StorageLocationDeleteImpact{}, err
	}
	provider, err := repository.Provider(
		ctx,
		workspaceID,
		bucket.StorageProviderID,
	)
	if err != nil {
		return StorageLocationDeleteImpact{}, err
	}
	rootID := bucket.AuthorizedRootID
	counts, err := repository.storageLocationImpactCounts(
		ctx,
		workspaceID,
		bucket.StorageProviderID,
		&rootID,
	)
	if err != nil {
		return StorageLocationDeleteImpact{}, err
	}
	bucketIDCopy := bucket.ID
	bucketName := bucket.DisplayName
	impact := StorageLocationDeleteImpact{
		TargetType:       "local_managed_bucket",
		ProviderID:       provider.ID,
		ProviderName:     provider.Name,
		ProviderKind:     provider.Kind,
		AuthorizedRootID: &rootID,
		BucketID:         &bucketIDCopy,
		BucketName:       &bucketName,
		CanDisable:       true,
		Counts:           counts,
	}
	impact.CanDelete = rootTargetCanDelete(counts)
	impact.BlockingReasons = storageLocationBlockingReasons(
		impact.TargetType,
		counts,
	)
	return impact, nil
}

func (repository *SQLiteRepository) DeleteLocalManagedBucket(
	ctx context.Context,
	input LocalManagedBucketStateInput,
	now time.Time,
) error {
	bucket, err := repository.LocalManagedBucket(ctx, input.WorkspaceID, input.ID)
	if err != nil {
		return err
	}
	impact, err := repository.LocalManagedBucketDeleteImpact(
		ctx,
		input.WorkspaceID,
		input.ID,
	)
	if err != nil {
		return err
	}
	if !impact.CanDelete {
		return ErrProviderInUse
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin local bucket deletion: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatDatabaseTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE local_managed_buckets
		SET deleted_at = ?,
			project_available = 0,
			status = 'disabled',
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND revision = ?
			AND deleted_at IS NULL
	`, timestamp, timestamp, input.ID, input.WorkspaceID, input.Revision)
	if err != nil {
		return fmt.Errorf("delete local managed bucket: %w", err)
	}
	if err := requireChangedBucket(
		ctx,
		tx,
		result,
		input.WorkspaceID,
		input.ID,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE project_storage_grants
		SET status = 'disabled',
			updated_at = ?
		WHERE workspace_id = ?
			AND authorized_root_id = ?
	`, timestamp, input.WorkspaceID, bucket.AuthorizedRootID); err != nil {
		return fmt.Errorf("disable deleted bucket grants: %w", err)
	}

	rootResult, err := tx.ExecContext(ctx, `
		UPDATE authorized_roots
		SET deleted_at = ?,
			revision = revision + 1,
			updated_at = ?
		WHERE id = ?
			AND workspace_id = ?
			AND deleted_at IS NULL
	`, timestamp, timestamp, bucket.AuthorizedRootID, input.WorkspaceID)
	if err != nil {
		return fmt.Errorf("delete local bucket root: %w", err)
	}
	changed, err := rootResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted root result: %w", err)
	}
	if changed == 0 {
		return ErrRootNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit local bucket deletion: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) storageLocationImpactCounts(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (StorageLocationImpactCounts, error) {
	var counts StorageLocationImpactCounts
	var err error
	if rootID == nil {
		counts.AuthorizedRoots, err = countInt(ctx, repository.db, `
			SELECT COUNT(*)
			FROM authorized_roots
			WHERE workspace_id = ?
				AND storage_provider_id = ?
				AND deleted_at IS NULL
		`, workspaceID, providerID)
	} else {
		counts.AuthorizedRoots, err = countInt(ctx, repository.db, `
			SELECT COUNT(*)
			FROM authorized_roots
			WHERE workspace_id = ?
				AND id = ?
				AND storage_provider_id = ?
				AND deleted_at IS NULL
		`, workspaceID, *rootID, providerID)
	}
	if err != nil {
		return counts, fmt.Errorf("count storage roots: %w", err)
	}

	counts.ProjectGrants, err = repository.countProjectStorageGrants(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.ProjectSelections, err = repository.countProjectStorageSelections(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.StorageObjects, err = repository.countStorageObjects(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.AssetVersions, err = repository.countAssetVersions(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.Renditions, err = repository.countRenditions(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.ReviewSessions, err = repository.countReviewSessions(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.PendingJobs, err = repository.countPendingJobs(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.StorageCopyTasks, err = repository.countStorageCopyTasks(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	counts.UploadChecks, err = repository.countUploadChecks(
		ctx,
		workspaceID,
		providerID,
		rootID,
	)
	if err != nil {
		return counts, err
	}
	return counts, nil
}

func (repository *SQLiteRepository) countRenditions(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(*)
			FROM renditions rendition
			JOIN authorized_roots root
				ON root.id = rendition.authorized_root_id
				AND root.workspace_id = rendition.workspace_id
			WHERE rendition.workspace_id = ?
				AND root.storage_provider_id = ?
				AND rendition.status = 'ready'
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(*)
		FROM renditions
		WHERE workspace_id = ?
			AND authorized_root_id = ?
			AND status = 'ready'
	`, workspaceID, *rootID)
}

func (repository *SQLiteRepository) countProjectStorageGrants(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(*)
			FROM project_storage_grants
			WHERE workspace_id = ?
				AND storage_provider_id = ?
				AND status = 'active'
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(*)
		FROM project_storage_grants
		WHERE workspace_id = ?
			AND storage_provider_id = ?
			AND authorized_root_id = ?
			AND status = 'active'
	`, workspaceID, providerID, *rootID)
}

func (repository *SQLiteRepository) countProjectStorageSelections(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT s.id)
			FROM project_storage_selections s
			JOIN project_storage_grants g
				ON g.id = s.grant_id
				AND g.workspace_id = s.workspace_id
			WHERE s.workspace_id = ?
				AND g.storage_provider_id = ?
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT s.id)
		FROM project_storage_selections s
		JOIN project_storage_grants g
			ON g.id = s.grant_id
			AND g.workspace_id = s.workspace_id
		WHERE s.workspace_id = ?
			AND g.storage_provider_id = ?
			AND g.authorized_root_id = ?
	`, workspaceID, providerID, *rootID)
}

func (repository *SQLiteRepository) countStorageObjects(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(*)
			FROM storage_objects
			WHERE workspace_id = ?
				AND storage_provider_id = ?
				AND deleted_at IS NULL
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(*)
		FROM storage_objects
		WHERE workspace_id = ?
			AND storage_provider_id = ?
			AND authorized_root_id = ?
			AND deleted_at IS NULL
	`, workspaceID, providerID, *rootID)
}

func (repository *SQLiteRepository) countAssetVersions(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT av.id)
			FROM asset_versions av
			JOIN version_files vf
				ON vf.asset_version_id = av.id
				AND vf.workspace_id = av.workspace_id
			JOIN storage_objects so
				ON so.id = vf.storage_object_id
				AND so.workspace_id = av.workspace_id
			WHERE av.workspace_id = ?
				AND av.deleted_at IS NULL
				AND so.storage_provider_id = ?
				AND so.deleted_at IS NULL
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT av.id)
		FROM asset_versions av
		JOIN version_files vf
			ON vf.asset_version_id = av.id
			AND vf.workspace_id = av.workspace_id
		JOIN storage_objects so
			ON so.id = vf.storage_object_id
			AND so.workspace_id = av.workspace_id
		WHERE av.workspace_id = ?
			AND av.deleted_at IS NULL
			AND so.storage_provider_id = ?
			AND so.authorized_root_id = ?
			AND so.deleted_at IS NULL
	`, workspaceID, providerID, *rootID)
}

func (repository *SQLiteRepository) countReviewSessions(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT rs.id)
			FROM review_sessions rs
			JOIN review_items ri
				ON ri.review_session_id = rs.id
				AND ri.workspace_id = rs.workspace_id
			JOIN version_files vf
				ON vf.asset_version_id = ri.asset_version_id
				AND vf.workspace_id = ri.workspace_id
			JOIN storage_objects so
				ON so.id = vf.storage_object_id
				AND so.workspace_id = ri.workspace_id
			WHERE rs.workspace_id = ?
				AND rs.status <> 'closed'
				AND so.storage_provider_id = ?
				AND so.deleted_at IS NULL
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT rs.id)
		FROM review_sessions rs
		JOIN review_items ri
			ON ri.review_session_id = rs.id
			AND ri.workspace_id = rs.workspace_id
		JOIN version_files vf
			ON vf.asset_version_id = ri.asset_version_id
			AND vf.workspace_id = ri.workspace_id
		JOIN storage_objects so
			ON so.id = vf.storage_object_id
			AND so.workspace_id = ri.workspace_id
		WHERE rs.workspace_id = ?
			AND rs.status <> 'closed'
			AND so.storage_provider_id = ?
			AND so.authorized_root_id = ?
			AND so.deleted_at IS NULL
	`, workspaceID, providerID, *rootID)
}

func (repository *SQLiteRepository) countPendingJobs(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT j.id)
			FROM jobs j
			WHERE j.workspace_id = ?
				AND j.status IN ('queued', 'leased', 'running', 'cancel_requested')
				AND (
					EXISTS (
						SELECT 1
						FROM storage_objects so
						WHERE so.id = j.subject_id
							AND so.workspace_id = j.workspace_id
							AND so.storage_provider_id = ?
							AND so.deleted_at IS NULL
					)
					OR EXISTS (
						SELECT 1
						FROM asset_versions av
						JOIN version_files vf
							ON vf.asset_version_id = av.id
							AND vf.workspace_id = av.workspace_id
						JOIN storage_objects so
							ON so.id = vf.storage_object_id
							AND so.workspace_id = av.workspace_id
						WHERE av.id = j.subject_id
							AND av.workspace_id = j.workspace_id
							AND av.deleted_at IS NULL
							AND so.storage_provider_id = ?
							AND so.deleted_at IS NULL
					)
				)
		`, workspaceID, providerID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT j.id)
		FROM jobs j
		WHERE j.workspace_id = ?
			AND j.status IN ('queued', 'leased', 'running', 'cancel_requested')
			AND (
				EXISTS (
					SELECT 1
					FROM storage_objects so
					WHERE so.id = j.subject_id
						AND so.workspace_id = j.workspace_id
						AND so.storage_provider_id = ?
						AND so.authorized_root_id = ?
						AND so.deleted_at IS NULL
				)
				OR EXISTS (
					SELECT 1
					FROM asset_versions av
					JOIN version_files vf
						ON vf.asset_version_id = av.id
						AND vf.workspace_id = av.workspace_id
					JOIN storage_objects so
						ON so.id = vf.storage_object_id
						AND so.workspace_id = av.workspace_id
					WHERE av.id = j.subject_id
						AND av.workspace_id = j.workspace_id
						AND av.deleted_at IS NULL
						AND so.storage_provider_id = ?
						AND so.authorized_root_id = ?
						AND so.deleted_at IS NULL
				)
			)
	`, workspaceID, providerID, *rootID, providerID, *rootID)
}

func (repository *SQLiteRepository) countStorageCopyTasks(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT task.id)
			FROM storage_copy_tasks task
			LEFT JOIN authorized_roots source_root
				ON source_root.id = task.source_root_id
				AND source_root.workspace_id = task.workspace_id
				AND source_root.deleted_at IS NULL
			LEFT JOIN authorized_roots target_root
				ON target_root.id = task.target_root_id
				AND target_root.workspace_id = task.workspace_id
				AND target_root.deleted_at IS NULL
			WHERE task.workspace_id = ?
				AND task.status IN ('queued', 'running')
				AND (
					source_root.storage_provider_id = ?
					OR target_root.storage_provider_id = ?
				)
		`, workspaceID, providerID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT task.id)
		FROM storage_copy_tasks task
		WHERE task.workspace_id = ?
			AND task.status IN ('queued', 'running')
			AND (task.source_root_id = ? OR task.target_root_id = ?)
	`, workspaceID, *rootID, *rootID)
}

func (repository *SQLiteRepository) countUploadChecks(
	ctx context.Context,
	workspaceID string,
	providerID string,
	rootID *string,
) (int, error) {
	if rootID == nil {
		return countInt(ctx, repository.db, `
			SELECT COUNT(DISTINCT uc.id)
			FROM upload_checks uc
			JOIN storage_objects so
				ON so.id = uc.storage_object_id
				AND so.workspace_id = uc.workspace_id
			WHERE uc.workspace_id = ?
				AND uc.status IN ('uploaded', 'checking', 'processing', 'quarantined')
				AND so.storage_provider_id = ?
				AND so.deleted_at IS NULL
		`, workspaceID, providerID)
	}
	return countInt(ctx, repository.db, `
		SELECT COUNT(DISTINCT uc.id)
		FROM upload_checks uc
		JOIN storage_objects so
			ON so.id = uc.storage_object_id
			AND so.workspace_id = uc.workspace_id
		WHERE uc.workspace_id = ?
			AND uc.status IN ('uploaded', 'checking', 'processing', 'quarantined')
			AND so.storage_provider_id = ?
			AND so.authorized_root_id = ?
			AND so.deleted_at IS NULL
	`, workspaceID, providerID, *rootID)
}

func countInt(
	ctx context.Context,
	db *sql.DB,
	query string,
	args ...any,
) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func providerCanDelete(counts StorageLocationImpactCounts) bool {
	return counts.AuthorizedRoots == 0 && rootTargetCanDelete(counts)
}

func rootTargetCanDelete(counts StorageLocationImpactCounts) bool {
	return counts.ProjectGrants == 0 &&
		counts.ProjectSelections == 0 &&
		counts.StorageObjects == 0 &&
		counts.AssetVersions == 0 &&
		counts.Renditions == 0 &&
		counts.ReviewSessions == 0 &&
		counts.PendingJobs == 0 &&
		counts.StorageCopyTasks == 0 &&
		counts.UploadChecks == 0
}

func storageLocationBlockingReasons(
	targetType string,
	counts StorageLocationImpactCounts,
) []string {
	reasons := make([]string, 0)
	if targetType == "provider" && counts.AuthorizedRoots > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个授权目录或托管位置", counts.AuthorizedRoots),
		)
	}
	if counts.ProjectGrants > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个项目可用授权", counts.ProjectGrants),
		)
	}
	if counts.ProjectSelections > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个项目存储选择正在引用", counts.ProjectSelections),
		)
	}
	if counts.StorageObjects > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个文件对象未迁移或清理", counts.StorageObjects),
		)
	}
	if counts.AssetVersions > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个资产版本引用这里的文件", counts.AssetVersions),
		)
	}
	if counts.Renditions > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个预览文件保存在这里", counts.Renditions),
		)
	}
	if counts.ReviewSessions > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个未关闭审阅引用这里的版本", counts.ReviewSessions),
		)
	}
	if counts.PendingJobs > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个处理中任务依赖这里的文件", counts.PendingJobs),
		)
	}
	if counts.StorageCopyTasks > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个存储复制任务未完成", counts.StorageCopyTasks),
		)
	}
	if counts.UploadChecks > 0 {
		reasons = append(
			reasons,
			fmt.Sprintf("还有 %d 个上传检查记录未完成处理", counts.UploadChecks),
		)
	}
	return reasons
}
