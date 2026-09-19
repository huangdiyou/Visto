package review

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
	filter ListFilter,
) (ListPage, error) {
	query := `
		SELECT
			rs.id, rs.workspace_id, rs.project_id, projects.name,
			rs.collection_id, rs.name, rs.status, rs.due_at, rs.created_by,
			rs.template_id, rs.template_revision, rs.responsible_user_id,
			COALESCE(responsible.display_name, ''),
			rs.allow_download, rs.decision_rule,
			rs.revision, rs.created_at, rs.updated_at, rs.closed_at
		FROM review_sessions rs
		JOIN projects ON projects.id = rs.project_id
		LEFT JOIN users responsible ON responsible.id = rs.responsible_user_id
	WHERE rs.workspace_id = ?`
	args := []any{workspaceID}
	if !filter.WorkspaceOwner {
		query += `
			AND EXISTS (
				SELECT 1
				FROM project_memberships membership
				WHERE membership.workspace_id = rs.workspace_id
					AND membership.project_id = rs.project_id
					AND membership.user_id = ?
					AND membership.status = 'active'
					AND (membership.expires_at IS NULL OR membership.expires_at > ?)
					AND COALESCE(
						json_extract(membership.permissions_json, '$."project.read"'),
						1
					) = 1
			)`
		args = append(args, filter.UserID, formatTime(filter.At.UTC()))
	}
	if filter.ProjectID != "" {
		query += `
			AND rs.project_id = ?`
		args = append(args, filter.ProjectID)
	}
	if filter.AssetVersionID != "" {
		query += `
			AND EXISTS (
				SELECT 1 FROM review_items ri
				WHERE ri.review_session_id = rs.id
					AND ri.asset_version_id = ?
			)`
		args = append(args, filter.AssetVersionID)
	}
	query += ` ORDER BY rs.updated_at DESC, rs.created_at DESC, rs.id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit+1, filter.Offset)
	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ListPage{}, fmt.Errorf("list review sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		session, scanErr := scanSession(rows)
		if scanErr != nil {
			return ListPage{}, scanErr
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, fmt.Errorf("iterate review sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ListPage{}, fmt.Errorf("close review session rows: %w", err)
	}
	var nextOffset *int
	if len(sessions) > filter.Limit {
		next := filter.Offset + filter.Limit
		nextOffset = &next
		sessions = sessions[:filter.Limit]
	}
	for index := range sessions {
		if err := repository.loadChildren(ctx, &sessions[index]); err != nil {
			return ListPage{}, err
		}
	}
	return ListPage{Items: sessions, NextOffset: nextOffset}, nil
}

func (repository *SQLiteRepository) Get(
	ctx context.Context,
	workspaceID string,
	id string,
) (Session, error) {
	session, err := repository.get(ctx, repository.db, workspaceID, id)
	if err != nil {
		return Session{}, err
	}
	if err := repository.loadChildren(ctx, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (repository *SQLiteRepository) Create(
	ctx context.Context,
	record sessionRecord,
) (Session, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin review session creation: %w", err)
	}
	defer tx.Rollback()

	if err := validateProject(ctx, tx, record.Session); err != nil {
		return Session{}, err
	}
	if err := ensureNameAvailable(
		ctx,
		tx,
		record.WorkspaceID,
		record.ProjectID,
		record.Name,
		"",
	); err != nil {
		return Session{}, err
	}
	if err := validateResponsible(
		ctx,
		tx,
		record.WorkspaceID,
		record.ResponsibleUserID,
	); err != nil {
		return Session{}, err
	}
	for _, item := range record.Items {
		if err := validateItem(ctx, tx, record.Session, item); err != nil {
			return Session{}, err
		}
	}
	now := formatTime(record.Now)
	var dueAt any
	if record.DueAt != nil {
		dueAt = formatTime(record.DueAt.UTC())
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO review_sessions (
			id, workspace_id, project_id, collection_id, name, status,
			due_at, template_id, template_revision, responsible_user_id,
			allow_download, decision_rule, created_by, revision,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 'draft', ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, record.ID, record.WorkspaceID, record.ProjectID, record.CollectionID,
		record.Name, dueAt, record.TemplateID, record.TemplateRevision,
		record.ResponsibleUserID, record.AllowDownload, record.DecisionRule,
		record.CreatedBy, now, now); err != nil {
		return Session{}, fmt.Errorf("create review session: %w", err)
	}
	for _, item := range record.Items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO review_items (
				id, workspace_id, review_session_id, asset_id,
				asset_version_id, position, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)
		`, item.ID, record.WorkspaceID, record.ID, item.AssetID,
			item.AssetVersionID, item.Position, now, now); err != nil {
			return Session{}, fmt.Errorf("create review item: %w", err)
		}
	}
	if err := replaceParticipants(
		ctx,
		tx,
		record.WorkspaceID,
		record.ID,
		record.Participants,
		now,
	); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit review session creation: %w", err)
	}
	return repository.Get(ctx, record.WorkspaceID, record.ID)
}

func (repository *SQLiteRepository) Update(
	ctx context.Context,
	input UpdateSessionInput,
	now time.Time,
) (Session, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin review session update: %w", err)
	}
	defer tx.Rollback()
	if err := validateResponsible(
		ctx,
		tx,
		input.WorkspaceID,
		input.ResponsibleUserID,
	); err != nil {
		return Session{}, err
	}
	var projectID string
	err = tx.QueryRowContext(ctx, `
		SELECT project_id
		FROM review_sessions
		WHERE id = ? AND workspace_id = ?
	`, input.ID, input.WorkspaceID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read review project for name check: %w", err)
	}
	if err := ensureNameAvailable(
		ctx,
		tx,
		input.WorkspaceID,
		projectID,
		input.Name,
		input.ID,
	); err != nil {
		return Session{}, err
	}
	var dueAt any
	if input.DueAt != nil {
		dueAt = formatTime(input.DueAt.UTC())
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE review_sessions
		SET name = ?, due_at = ?, responsible_user_id = ?,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND status != 'closed'
	`, input.Name, dueAt, input.ResponsibleUserID, formatTime(now), input.ID,
		input.WorkspaceID, input.Revision)
	if err != nil {
		return Session{}, fmt.Errorf("update review session: %w", err)
	}
	if err := requireChanged(ctx, tx, result, input.WorkspaceID, input.ID); err != nil {
		return Session{}, err
	}
	participants := make([]Participant, 0, len(input.Participants))
	for _, item := range input.Participants {
		id, idErr := newID()
		if idErr != nil {
			return Session{}, idErr
		}
		participants = append(participants, Participant{
			ID: id, UserID: item.UserID, DisplayName: item.DisplayName,
			Role: item.Role,
		})
	}
	if err := replaceParticipants(
		ctx,
		tx,
		input.WorkspaceID,
		input.ID,
		participants,
		formatTime(now),
	); err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit review session update: %w", err)
	}
	return repository.Get(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) SetStatus(
	ctx context.Context,
	input SessionStateInput,
	status string,
	now time.Time,
) (Session, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin review status update: %w", err)
	}
	defer tx.Rollback()
	var currentStatus string
	var currentRevision int
	err = tx.QueryRowContext(ctx, `
		SELECT status, revision
		FROM review_sessions
		WHERE id = ? AND workspace_id = ?
	`, input.ID, input.WorkspaceID).Scan(&currentStatus, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("read review status: %w", err)
	}
	if currentRevision != input.Revision {
		return Session{}, ErrRevisionConflict
	}
	if (status == "open" && currentStatus == "open") ||
		(status == "closed" && currentStatus == "closed") {
		return Session{}, ErrInvalidState
	}
	var closedAt any
	if status == "closed" {
		closedAt = formatTime(now)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE review_sessions
		SET status = ?, closed_at = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
	`, status, closedAt, formatTime(now), input.ID, input.WorkspaceID,
		input.Revision); err != nil {
		return Session{}, fmt.Errorf("set review status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit review status update: %w", err)
	}
	return repository.Get(ctx, input.WorkspaceID, input.ID)
}

func (repository *SQLiteRepository) ListDecisions(
	ctx context.Context,
	filter DecisionListFilter,
) ([]Decision, error) {
	query := decisionSelect + `
		WHERE rd.workspace_id = ? AND rd.review_session_id = ?`
	args := []any{filter.WorkspaceID, filter.ReviewSessionID}
	if filter.ReviewItemID != "" {
		query += ` AND rd.review_item_id = ?`
		args = append(args, filter.ReviewItemID)
	}
	query += ` ORDER BY rd.created_at DESC, rd.id DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)
	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list review decisions: %w", err)
	}
	defer rows.Close()
	items := make([]Decision, 0)
	for rows.Next() {
		item, scanErr := scanDecision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review decisions: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) CreateDecision(
	ctx context.Context,
	record decisionRecord,
) (Decision, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Decision{}, fmt.Errorf("begin review decision creation: %w", err)
	}
	defer tx.Rollback()

	var reviewStatus, decisionRule string
	var responsibleUserID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT status, decision_rule, responsible_user_id
		FROM review_sessions
		WHERE id = ? AND workspace_id = ?
	`, record.ReviewSessionID, record.WorkspaceID).Scan(
		&reviewStatus,
		&decisionRule,
		&responsibleUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Decision{}, ErrNotFound
	}
	if err != nil {
		return Decision{}, fmt.Errorf("read review decision session: %w", err)
	}
	if reviewStatus == "draft" || reviewStatus == "closed" {
		return Decision{}, ErrInvalidState
	}
	if record.ReviewItemID != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM review_items
			WHERE id = ? AND review_session_id = ? AND workspace_id = ?
		`, *record.ReviewItemID, record.ReviewSessionID,
			record.WorkspaceID).Scan(&count); err != nil {
			return Decision{}, fmt.Errorf("validate review decision item: %w", err)
		}
		if count != 1 {
			return Decision{}, ErrInvalidItem
		}
	}
	displayName, err := validateDecisionAuthor(ctx, tx, record)
	if err != nil {
		return Decision{}, err
	}
	if err := validateDecisionPolicy(
		ctx,
		tx,
		record,
		decisionRule,
		responsibleUserID,
	); err != nil {
		return Decision{}, err
	}

	now := formatTime(record.Now)
	var actorUserID, actorVisitorID any
	if record.Actor.Kind == "user" {
		actorUserID = record.Actor.ID
	} else {
		actorVisitorID = record.Actor.ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO review_decisions (
			id, workspace_id, review_session_id, review_item_id,
			actor_kind, actor_user_id, actor_visitor_id,
			decision, note, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ReviewSessionID,
		record.ReviewItemID, record.Actor.Kind, actorUserID,
		actorVisitorID, record.Decision.Decision, record.Note, now); err != nil {
		return Decision{}, fmt.Errorf("create review decision: %w", err)
	}

	targetStatus := "changes_requested"
	if record.Decision.Decision == "approved" {
		targetStatus = "approved"
		if decisionRule == "all_reviewers" {
			approved, err := allReviewersApproved(ctx, tx, record)
			if err != nil {
				return Decision{}, err
			}
			if !approved {
				targetStatus = "in_review"
			}
		}
	}
	sessionStatus := targetStatus
	if targetStatus == "in_review" {
		sessionStatus = "open"
	}
	if record.ReviewItemID == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE review_items
			SET status = ?, updated_at = ?
			WHERE review_session_id = ? AND workspace_id = ?
		`, targetStatus, now, record.ReviewSessionID,
			record.WorkspaceID); err != nil {
			return Decision{}, fmt.Errorf(
				"update review items after decision: %w",
				err,
			)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE review_items
			SET status = ?, updated_at = ?
			WHERE id = ? AND review_session_id = ? AND workspace_id = ?
		`, targetStatus, now, *record.ReviewItemID,
			record.ReviewSessionID, record.WorkspaceID); err != nil {
			return Decision{}, fmt.Errorf(
				"update review item after decision: %w",
				err,
			)
		}
		var total, approved, changesRequested int
		if err := tx.QueryRowContext(ctx, `
			SELECT
				COUNT(*),
				SUM(CASE WHEN status = 'approved' THEN 1 ELSE 0 END),
				SUM(CASE WHEN status = 'changes_requested' THEN 1 ELSE 0 END)
			FROM review_items
			WHERE review_session_id = ? AND workspace_id = ?
		`, record.ReviewSessionID, record.WorkspaceID).Scan(
			&total,
			&approved,
			&changesRequested,
		); err != nil {
			return Decision{}, fmt.Errorf(
				"summarize review items after decision: %w",
				err,
			)
		}
		switch {
		case changesRequested > 0:
			sessionStatus = "changes_requested"
		case total > 0 && approved == total:
			sessionStatus = "approved"
		default:
			sessionStatus = "open"
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE review_sessions
		SET status = ?, closed_at = NULL,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ?
	`, sessionStatus, now, record.ReviewSessionID,
		record.WorkspaceID); err != nil {
		return Decision{}, fmt.Errorf(
			"update review session after decision: %w",
			err,
		)
	}
	if err := tx.Commit(); err != nil {
		return Decision{}, fmt.Errorf("commit review decision creation: %w", err)
	}
	record.Actor.DisplayName = displayName
	record.CreatedAt = record.Now
	return record.Decision, nil
}

func (repository *SQLiteRepository) ListThreads(
	ctx context.Context,
	filter ThreadListFilter,
) ([]CommentThread, error) {
	query := threadSelect + `
		WHERE ct.workspace_id = ? AND ct.review_session_id = ?
			AND ct.deleted_at IS NULL`
	args := []any{filter.WorkspaceID, filter.ReviewSessionID}
	if filter.ReviewItemID != "" {
		query += ` AND ct.review_item_id = ?`
		args = append(args, filter.ReviewItemID)
	}
	query += `
		ORDER BY a.time_start_us, ct.created_at
		LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)
	rows, err := repository.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list comment threads: %w", err)
	}
	items := make([]CommentThread, 0)
	for rows.Next() {
		item, scanErr := scanCommentThread(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate comment threads: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close comment thread rows: %w", err)
	}
	for index := range items {
		comments, err := repository.loadComments(ctx, repository.db, items[index].ID)
		if err != nil {
			return nil, err
		}
		items[index].Comments = comments
	}
	return items, nil
}

const (
	maxPublicAttachmentBytesPerVisitor = 100 * 1024 * 1024
	maxPublicAttachmentCountPerVisitor = 40
	maxAttachmentBytesPerReview        = 1024 * 1024 * 1024
	maxAttachmentCountPerReview        = 500
)

func (repository *SQLiteRepository) CreatePendingAttachment(
	ctx context.Context,
	record attachmentRecord,
) (CommentAttachment, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CommentAttachment{}, fmt.Errorf("begin comment attachment upload: %w", err)
	}
	defer tx.Rollback()

	if err := validateAttachmentReviewItem(
		ctx,
		tx,
		record.WorkspaceID,
		record.ReviewSessionID,
		record.ReviewItemID,
	); err != nil {
		return CommentAttachment{}, err
	}
	_, err = validateThreadAuthor(ctx, tx, threadRecord{
		CommentThread: CommentThread{
			WorkspaceID:     record.WorkspaceID,
			ReviewSessionID: record.ReviewSessionID,
			Author: CommentAuthor{
				Kind: record.SourceType,
				ID:   attachmentActorID(record.CommentAttachment),
			},
		},
	})
	if err != nil {
		return CommentAttachment{}, err
	}
	var pendingCount int
	var countErr error
	if record.SourceType == "user" {
		countErr = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM comment_attachments
			WHERE workspace_id = ? AND review_session_id = ? AND review_item_id = ?
				AND source_type = 'user' AND uploaded_by_user_id = ?
				AND comment_id IS NULL AND status = 'ready'
		`, record.WorkspaceID, record.ReviewSessionID, record.ReviewItemID,
			attachmentActorID(record.CommentAttachment)).Scan(&pendingCount)
	} else {
		countErr = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM comment_attachments
			WHERE workspace_id = ? AND review_session_id = ? AND review_item_id = ?
				AND source_type = 'share_visitor' AND share_visitor_id = ?
				AND comment_id IS NULL AND status = 'ready'
		`, record.WorkspaceID, record.ReviewSessionID, record.ReviewItemID,
			attachmentActorID(record.CommentAttachment)).Scan(&pendingCount)
	}
	if countErr != nil {
		return CommentAttachment{}, fmt.Errorf("count pending comment attachments: %w", countErr)
	}
	if pendingCount >= 4 {
		return CommentAttachment{}, ErrInvalidAttachment
	}
	var reviewAttachmentCount int
	var reviewAttachmentBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM comment_attachments
		WHERE workspace_id = ? AND review_session_id = ?
	`, record.WorkspaceID, record.ReviewSessionID).Scan(
		&reviewAttachmentCount,
		&reviewAttachmentBytes,
	); err != nil {
		return CommentAttachment{}, fmt.Errorf("measure review comment attachment quota: %w", err)
	}
	if reviewAttachmentCount >= maxAttachmentCountPerReview ||
		reviewAttachmentBytes > maxAttachmentBytesPerReview-record.SizeBytes {
		return CommentAttachment{}, ErrAttachmentQuotaExceeded
	}
	if record.SourceType == "share_visitor" {
		var visitorAttachmentCount int
		var visitorAttachmentBytes int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
			FROM comment_attachments
			WHERE workspace_id = ? AND review_session_id = ?
				AND source_type = 'share_visitor' AND share_visitor_id = ?
		`, record.WorkspaceID, record.ReviewSessionID,
			attachmentActorID(record.CommentAttachment)).Scan(
			&visitorAttachmentCount,
			&visitorAttachmentBytes,
		); err != nil {
			return CommentAttachment{}, fmt.Errorf("measure visitor comment attachment quota: %w", err)
		}
		if visitorAttachmentCount >= maxPublicAttachmentCountPerVisitor ||
			visitorAttachmentBytes > maxPublicAttachmentBytesPerVisitor-record.SizeBytes {
			return CommentAttachment{}, ErrAttachmentQuotaExceeded
		}
	}
	now := formatTime(record.Now)
	var userID, visitorID any
	if record.SourceType == "user" {
		userID = attachmentActorID(record.CommentAttachment)
	} else {
		visitorID = attachmentActorID(record.CommentAttachment)
	}
	var rejectedAt, quarantinedAt any
	if record.Status == "rejected" {
		rejectedAt = now
	}
	if record.Status == "quarantined" {
		quarantinedAt = now
	}
	width := nullableInt(record.Width)
	height := nullableInt(record.Height)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comment_attachments (
			id, workspace_id, review_session_id, review_item_id,
			uploaded_by_user_id, share_visitor_id, source_type,
			authorized_root_id, object_key, original_filename,
			mime_type, size_bytes, width, height, upload_security_policy,
			status, result_code, message, created_at, updated_at,
			rejected_at, quarantined_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ReviewSessionID,
		record.ReviewItemID, userID, visitorID, record.SourceType,
		record.AuthorizedRootID, record.ObjectKey, record.OriginalFilename,
		record.MIMEType, record.SizeBytes, width, height,
		record.UploadSecurityPolicy, record.Status, nullIfEmpty(record.ResultCode),
		record.Message, now, now, rejectedAt, quarantinedAt); err != nil {
		return CommentAttachment{}, fmt.Errorf("create comment attachment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommentAttachment{}, fmt.Errorf("commit comment attachment upload: %w", err)
	}
	record.CreatedAt = record.Now
	record.UpdatedAt = record.Now
	return record.CommentAttachment, nil
}

func (repository *SQLiteRepository) ListExpiredPendingAttachments(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]CommentAttachment, error) {
	rows, err := repository.db.QueryContext(ctx, attachmentSelect+`
		WHERE ca.comment_id IS NULL AND ca.status = 'ready' AND ca.created_at <= ?
		ORDER BY ca.created_at ASC, ca.id ASC
		LIMIT ?
	`, formatTime(before), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired pending comment attachments: %w", err)
	}
	defer rows.Close()
	items := make([]CommentAttachment, 0)
	for rows.Next() {
		item, scanErr := scanCommentAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired pending comment attachments: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) DeleteExpiredPendingAttachment(
	ctx context.Context,
	id string,
	before time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		DELETE FROM comment_attachments
		WHERE id = ? AND comment_id IS NULL AND status = 'ready' AND created_at <= ?
	`, id, formatTime(before))
	if err != nil {
		return fmt.Errorf("delete expired pending comment attachment: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("count deleted expired pending comment attachment: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) CommentAttachment(
	ctx context.Context,
	input CommentAttachmentLookup,
) (CommentAttachment, error) {
	args := []any{
		input.ID,
		input.WorkspaceID,
		input.ReviewSessionID,
		input.ReviewItemID,
	}
	ownershipCondition := ""
	if input.ActorKind == "user" {
		ownershipCondition = `
			AND (
				ca.comment_id IS NOT NULL
				OR ca.uploaded_by_user_id = ?
			)`
		args = append(args, input.ActorUserID)
	} else {
		ownershipCondition = `
			AND (
				ca.comment_id IS NOT NULL
				OR ca.share_visitor_id = ?
			)`
		args = append(args, input.ActorVisitorID)
	}
	row := repository.db.QueryRowContext(ctx, attachmentSelect+`
		WHERE ca.id = ? AND ca.workspace_id = ?
			AND ca.review_session_id = ? AND ca.review_item_id = ?
			AND ca.status = 'ready'`+ownershipCondition, args...)
	item, err := scanCommentAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CommentAttachment{}, ErrAttachmentNotFound
	}
	if err != nil {
		return CommentAttachment{}, err
	}
	return item, nil
}

func (repository *SQLiteRepository) CreateThread(
	ctx context.Context,
	record threadRecord,
) (CommentThread, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CommentThread{}, fmt.Errorf("begin comment thread creation: %w", err)
	}
	defer tx.Rollback()

	var assetVersionID, reviewStatus, mediaType string
	var durationUs sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT ri.asset_version_id, rs.status, assets.type,
			media_probes.duration_us
		FROM review_items ri
		JOIN review_sessions rs ON rs.id = ri.review_session_id
		JOIN assets ON assets.id = ri.asset_id
		JOIN version_files vf
			ON vf.asset_version_id = ri.asset_version_id
			AND vf.role = 'primary'
		LEFT JOIN media_probes
			ON media_probes.storage_object_id = vf.storage_object_id
			AND media_probes.status = 'succeeded'
		WHERE ri.id = ? AND ri.review_session_id = ?
			AND ri.workspace_id = ? AND rs.workspace_id = ?
	`, record.ReviewItemID, record.ReviewSessionID, record.WorkspaceID,
		record.WorkspaceID).Scan(
		&assetVersionID,
		&reviewStatus,
		&mediaType,
		&durationUs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CommentThread{}, ErrInvalidItem
	}
	if err != nil {
		return CommentThread{}, fmt.Errorf("validate comment review item: %w", err)
	}
	if assetVersionID != record.AssetVersionID {
		return CommentThread{}, ErrInvalidItem
	}
	if reviewStatus != "open" {
		return CommentThread{}, ErrInvalidState
	}
	if (mediaType == "image" &&
		(record.Annotation.Kind == "time_point" ||
			record.Annotation.Kind == "time_range")) ||
		(mediaType == "video" &&
			(record.Annotation.Kind == "point" ||
				record.Annotation.Kind == "region" ||
				record.Annotation.Kind == "drawing")) {
		return CommentThread{}, ErrInvalidAnnotation
	}
	if durationUs.Valid {
		if record.Annotation.TimeStartUs != nil &&
			*record.Annotation.TimeStartUs > durationUs.Int64 {
			return CommentThread{}, ErrInvalidAnnotation
		}
		if record.Annotation.TimeEndUs != nil &&
			*record.Annotation.TimeEndUs > durationUs.Int64 {
			return CommentThread{}, ErrInvalidAnnotation
		}
	}
	displayName, err := validateThreadAuthor(ctx, tx, record)
	if err != nil {
		return CommentThread{}, err
	}
	geometryJSON, err := encodeAnnotationGeometry(record.Annotation.Geometry)
	if err != nil {
		return CommentThread{}, err
	}

	now := formatTime(record.Now)
	var authorUserID, authorVisitorID any
	if record.Author.Kind == "user" {
		authorUserID = record.Author.ID
	} else {
		authorVisitorID = record.Author.ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comment_threads (
			id, workspace_id, review_session_id, review_item_id,
			asset_version_id, author_kind, author_user_id,
			author_visitor_id, status, revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', 1, ?, ?)
	`, record.ID, record.WorkspaceID, record.ReviewSessionID,
		record.ReviewItemID, record.AssetVersionID, record.Author.Kind,
		authorUserID, authorVisitorID, now, now); err != nil {
		return CommentThread{}, fmt.Errorf("create comment thread: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comments (
			id, workspace_id, thread_id, author_kind, author_user_id,
			author_visitor_id, body, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, record.CommentID, record.WorkspaceID, record.ID, record.Author.Kind,
		authorUserID, authorVisitorID, record.Body, now); err != nil {
		return CommentThread{}, fmt.Errorf("create initial comment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO annotations (
			id, workspace_id, thread_id, kind, time_start_us,
			time_end_us, geometry_version, geometry_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.Annotation.ID, record.WorkspaceID, record.ID,
		record.Annotation.Kind, record.Annotation.TimeStartUs,
		record.Annotation.TimeEndUs, record.Annotation.GeometryVersion,
		geometryJSON, now, now); err != nil {
		return CommentThread{}, fmt.Errorf("create comment annotation: %w", err)
	}
	if err := attachCommentAttachments(
		ctx,
		tx,
		record.WorkspaceID,
		record.ReviewSessionID,
		record.ReviewItemID,
		record.ID,
		record.CommentID,
		record.Author,
		record.AttachmentIDs,
		now,
	); err != nil {
		return CommentThread{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommentThread{}, fmt.Errorf("commit comment thread creation: %w", err)
	}
	_ = displayName
	return repository.getThread(
		ctx,
		record.WorkspaceID,
		record.ReviewSessionID,
		record.ID,
	)
}

func (repository *SQLiteRepository) AddComment(
	ctx context.Context,
	record threadCommentRecord,
) (CommentThread, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CommentThread{}, fmt.Errorf("begin comment reply: %w", err)
	}
	defer tx.Rollback()

	if err := requireOpenThreadForComment(
		ctx,
		tx,
		record.WorkspaceID,
		record.ReviewSessionID,
		record.ReviewItemID,
		record.ThreadID,
	); err != nil {
		return CommentThread{}, err
	}
	displayName, err := validateThreadAuthor(ctx, tx, threadRecord{
		CommentThread: CommentThread{
			WorkspaceID:     record.WorkspaceID,
			ReviewSessionID: record.ReviewSessionID,
			Author:          record.Author,
		},
	})
	if err != nil {
		return CommentThread{}, err
	}
	reviewItemID := record.ReviewItemID
	if strings.TrimSpace(reviewItemID) == "" {
		if err := tx.QueryRowContext(ctx, `
			SELECT review_item_id
			FROM comment_threads
			WHERE id = ? AND workspace_id = ? AND review_session_id = ?
				AND deleted_at IS NULL
		`, record.ThreadID, record.WorkspaceID,
			record.ReviewSessionID).Scan(&reviewItemID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CommentThread{}, ErrCommentNotFound
			}
			return CommentThread{}, fmt.Errorf("read reply thread item: %w", err)
		}
	}
	now := formatTime(record.Now)
	var authorUserID, authorVisitorID any
	if record.Author.Kind == "user" {
		authorUserID = record.Author.ID
	} else {
		authorVisitorID = record.Author.ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO comments (
			id, workspace_id, thread_id, author_kind, author_user_id,
			author_visitor_id, body, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, record.CommentID, record.WorkspaceID, record.ThreadID,
		record.Author.Kind, authorUserID, authorVisitorID, record.Body,
		now); err != nil {
		return CommentThread{}, fmt.Errorf("create comment reply: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE comment_threads
		SET revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND review_session_id = ?
	`, now, record.ThreadID, record.WorkspaceID,
		record.ReviewSessionID); err != nil {
		return CommentThread{}, fmt.Errorf("touch comment thread: %w", err)
	}
	if err := attachCommentAttachments(
		ctx,
		tx,
		record.WorkspaceID,
		record.ReviewSessionID,
		reviewItemID,
		record.ThreadID,
		record.CommentID,
		record.Author,
		record.AttachmentIDs,
		now,
	); err != nil {
		return CommentThread{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommentThread{}, fmt.Errorf("commit comment reply: %w", err)
	}
	item, err := repository.getThread(
		ctx,
		record.WorkspaceID,
		record.ReviewSessionID,
		record.ThreadID,
	)
	if err == nil {
		for index := range item.Comments {
			if item.Comments[index].ID == record.CommentID {
				item.Comments[index].Author.DisplayName = displayName
			}
		}
	}
	return item, err
}

func (repository *SQLiteRepository) UpdateComment(
	ctx context.Context,
	input UpdateCommentInput,
	now time.Time,
) (CommentThread, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CommentThread{}, fmt.Errorf("begin comment edit: %w", err)
	}
	defer tx.Rollback()

	if _, err := requireMutableComment(
		ctx,
		tx,
		input.WorkspaceID,
		input.ReviewSessionID,
		input.ReviewItemID,
		input.ThreadID,
		input.CommentID,
		input.AuthorKind,
		authorIDFromFields(
			input.AuthorKind,
			input.AuthorUserID,
			input.AuthorVisitorID,
		),
	); err != nil {
		return CommentThread{}, err
	}
	nowText := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE comments
		SET body = ?, edited_at = ?
		WHERE id = ? AND workspace_id = ? AND thread_id = ?
	`, input.Body, nowText, input.CommentID, input.WorkspaceID,
		input.ThreadID); err != nil {
		return CommentThread{}, fmt.Errorf("update comment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE comment_threads
		SET revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND review_session_id = ?
	`, nowText, input.ThreadID, input.WorkspaceID,
		input.ReviewSessionID); err != nil {
		return CommentThread{}, fmt.Errorf("touch edited comment thread: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommentThread{}, fmt.Errorf("commit comment edit: %w", err)
	}
	return repository.getThread(
		ctx,
		input.WorkspaceID,
		input.ReviewSessionID,
		input.ThreadID,
	)
}

func (repository *SQLiteRepository) DeleteComment(
	ctx context.Context,
	input DeleteCommentInput,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin comment deletion: %w", err)
	}
	defer tx.Rollback()

	visibleCount, err := requireMutableComment(
		ctx,
		tx,
		input.WorkspaceID,
		input.ReviewSessionID,
		input.ReviewItemID,
		input.ThreadID,
		input.CommentID,
		input.AuthorKind,
		authorIDFromFields(
			input.AuthorKind,
			input.AuthorUserID,
			input.AuthorVisitorID,
		),
	)
	if err != nil {
		return err
	}
	nowText := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE comments
		SET deleted_at = ?
		WHERE id = ? AND workspace_id = ? AND thread_id = ?
	`, nowText, input.CommentID, input.WorkspaceID,
		input.ThreadID); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if visibleCount <= 1 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE comment_threads
			SET deleted_at = ?, revision = revision + 1, updated_at = ?
			WHERE id = ? AND workspace_id = ? AND review_session_id = ?
		`, nowText, nowText, input.ThreadID, input.WorkspaceID,
			input.ReviewSessionID); err != nil {
			return fmt.Errorf("delete empty comment thread: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE comment_threads
		SET revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND review_session_id = ?
	`, nowText, input.ThreadID, input.WorkspaceID,
		input.ReviewSessionID); err != nil {
		return fmt.Errorf("touch deleted comment thread: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit comment deletion: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) SetThreadStatus(
	ctx context.Context,
	input ThreadStateInput,
	status string,
	now time.Time,
) (CommentThread, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return CommentThread{}, fmt.Errorf("begin thread status update: %w", err)
	}
	defer tx.Rollback()

	if err := validateThreadStateUser(
		ctx,
		tx,
		input.WorkspaceID,
		input.UserID,
	); err != nil {
		return CommentThread{}, err
	}
	var currentStatus string
	var currentRevision int
	err = tx.QueryRowContext(ctx, `
		SELECT status, revision
		FROM comment_threads
		WHERE id = ? AND workspace_id = ? AND review_session_id = ?
			AND deleted_at IS NULL
	`, input.ThreadID, input.WorkspaceID,
		input.ReviewSessionID).Scan(&currentStatus, &currentRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return CommentThread{}, ErrCommentNotFound
	}
	if err != nil {
		return CommentThread{}, fmt.Errorf("read comment thread status: %w", err)
	}
	if currentRevision != input.Revision {
		return CommentThread{}, ErrRevisionConflict
	}
	if (status == "resolved" && currentStatus != "open") ||
		(status == "open" && currentStatus != "resolved") {
		return CommentThread{}, ErrInvalidState
	}
	nowText := formatTime(now)
	var resolvedBy any
	var resolvedAt any
	if status == "resolved" {
		resolvedBy = input.UserID
		resolvedAt = nowText
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE comment_threads
		SET status = ?, resolved_by_user_id = ?, resolved_at = ?,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND review_session_id = ?
	`, status, resolvedBy, resolvedAt, nowText, input.ThreadID,
		input.WorkspaceID, input.ReviewSessionID); err != nil {
		return CommentThread{}, fmt.Errorf("update comment thread status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommentThread{}, fmt.Errorf("commit thread status update: %w", err)
	}
	return repository.getThread(
		ctx,
		input.WorkspaceID,
		input.ReviewSessionID,
		input.ThreadID,
	)
}

func (repository *SQLiteRepository) get(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	workspaceID string,
	id string,
) (Session, error) {
	session, err := scanSession(queryer.QueryRowContext(ctx, `
		SELECT
			rs.id, rs.workspace_id, rs.project_id, projects.name,
			rs.collection_id, rs.name, rs.status, rs.due_at, rs.created_by,
			rs.template_id, rs.template_revision, rs.responsible_user_id,
			COALESCE(responsible.display_name, ''),
			rs.allow_download, rs.decision_rule,
			rs.revision, rs.created_at, rs.updated_at, rs.closed_at
		FROM review_sessions rs
		JOIN projects ON projects.id = rs.project_id
		LEFT JOIN users responsible ON responsible.id = rs.responsible_user_id
		WHERE rs.id = ? AND rs.workspace_id = ?
	`, id, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

func (repository *SQLiteRepository) getThread(
	ctx context.Context,
	workspaceID string,
	reviewSessionID string,
	threadID string,
) (CommentThread, error) {
	item, err := scanCommentThread(repository.db.QueryRowContext(ctx, threadSelect+`
		WHERE ct.id = ? AND ct.workspace_id = ?
			AND ct.review_session_id = ? AND ct.deleted_at IS NULL
	`, threadID, workspaceID, reviewSessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return CommentThread{}, ErrCommentNotFound
	}
	if err != nil {
		return CommentThread{}, err
	}
	comments, err := repository.loadComments(ctx, repository.db, item.ID)
	if err != nil {
		return CommentThread{}, err
	}
	item.Comments = comments
	return item, nil
}

func requireOpenThreadForComment(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	reviewSessionID string,
	reviewItemID string,
	threadID string,
) error {
	itemCondition := ""
	args := []any{threadID, workspaceID, reviewSessionID, workspaceID}
	if reviewItemID != "" {
		itemCondition = " AND ct.review_item_id = ?"
		args = append(args, reviewItemID)
	}
	var reviewStatus string
	var threadStatus string
	err := tx.QueryRowContext(ctx, `
		SELECT rs.status, ct.status
		FROM comment_threads ct
		JOIN review_sessions rs ON rs.id = ct.review_session_id
		WHERE ct.id = ? AND ct.workspace_id = ?
			AND ct.review_session_id = ? AND rs.workspace_id = ?
			AND ct.deleted_at IS NULL`+itemCondition,
		args...).Scan(&reviewStatus, &threadStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCommentNotFound
	}
	if err != nil {
		return fmt.Errorf("read comment thread mutability: %w", err)
	}
	if reviewStatus != "open" || threadStatus != "open" {
		return ErrInvalidState
	}
	return nil
}

func requireMutableComment(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	reviewSessionID string,
	reviewItemID string,
	threadID string,
	commentID string,
	authorKind string,
	authorID string,
) (int, error) {
	itemCondition := ""
	args := []any{
		commentID,
		threadID,
		workspaceID,
		workspaceID,
		reviewSessionID,
		workspaceID,
	}
	if reviewItemID != "" {
		itemCondition = " AND ct.review_item_id = ?"
		args = append(args, reviewItemID)
	}
	var reviewStatus string
	var threadStatus string
	var commentAuthorKind string
	var commentAuthorID string
	err := tx.QueryRowContext(ctx, `
		SELECT rs.status, ct.status, c.author_kind,
			COALESCE(c.author_user_id, c.author_visitor_id, '')
		FROM comments c
		JOIN comment_threads ct ON ct.id = c.thread_id
		JOIN review_sessions rs ON rs.id = ct.review_session_id
		WHERE c.id = ? AND c.thread_id = ? AND c.workspace_id = ?
			AND ct.workspace_id = ? AND ct.review_session_id = ?
			AND rs.workspace_id = ? AND c.deleted_at IS NULL
			AND ct.deleted_at IS NULL`+itemCondition,
		args...).Scan(
		&reviewStatus,
		&threadStatus,
		&commentAuthorKind,
		&commentAuthorID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrCommentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read comment mutability: %w", err)
	}
	if commentAuthorKind != authorKind || commentAuthorID != authorID {
		return 0, ErrCommentForbidden
	}
	if reviewStatus != "open" || threadStatus != "open" {
		return 0, ErrInvalidState
	}
	var visibleCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM comments
		WHERE workspace_id = ? AND thread_id = ? AND deleted_at IS NULL
	`, workspaceID, threadID).Scan(&visibleCount); err != nil {
		return 0, fmt.Errorf("count visible comments: %w", err)
	}
	return visibleCount, nil
}

func validateThreadStateUser(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	userID string,
) error {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM users
		JOIN memberships ON memberships.user_id = users.id
		WHERE users.id = ? AND memberships.workspace_id = ?
			AND users.status = 'active'
			AND memberships.status = 'active'
	`, userID, workspaceID).Scan(&count)
	if err != nil {
		return fmt.Errorf("validate thread status user: %w", err)
	}
	if count != 1 {
		return ErrInvalidAuthor
	}
	return nil
}

func validateAttachmentReviewItem(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	reviewSessionID string,
	reviewItemID string,
) error {
	var reviewStatus string
	err := tx.QueryRowContext(ctx, `
		SELECT rs.status
		FROM review_items ri
		JOIN review_sessions rs ON rs.id = ri.review_session_id
		WHERE ri.id = ? AND ri.review_session_id = ?
			AND ri.workspace_id = ? AND rs.workspace_id = ?
	`, reviewItemID, reviewSessionID, workspaceID, workspaceID).Scan(&reviewStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidItem
	}
	if err != nil {
		return fmt.Errorf("validate comment attachment review item: %w", err)
	}
	if reviewStatus != "open" {
		return ErrInvalidState
	}
	return nil
}

func attachCommentAttachments(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	reviewSessionID string,
	reviewItemID string,
	threadID string,
	commentID string,
	author CommentAuthor,
	attachmentIDs []string,
	now string,
) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(attachmentIDs))
	args := []any{threadID, commentID, now, now, workspaceID,
		reviewSessionID, reviewItemID}
	for _, id := range attachmentIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	var actorCondition string
	switch author.Kind {
	case "user":
		actorCondition = "uploaded_by_user_id = ? AND share_visitor_id IS NULL"
	case "share_visitor":
		actorCondition = "share_visitor_id = ? AND uploaded_by_user_id IS NULL"
	default:
		return ErrInvalidAuthor
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE comment_attachments
		SET thread_id = ?, comment_id = ?, attached_at = ?, updated_at = ?
		WHERE workspace_id = ? AND review_session_id = ?
			AND review_item_id = ? AND id IN (`+strings.Join(placeholders, ",")+`)
			AND comment_id IS NULL AND status = 'ready'
			AND source_type = ? AND `+actorCondition,
		append(args, author.Kind, author.ID)...,
	)
	if err != nil {
		return fmt.Errorf("attach comment attachments: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count attached comment attachments: %w", err)
	}
	if affected != int64(len(attachmentIDs)) {
		return ErrInvalidAttachment
	}
	return nil
}

func attachmentActorID(item CommentAttachment) string {
	if item.SourceType == "user" && item.UploadedByUserID != nil {
		return *item.UploadedByUserID
	}
	if item.SourceType == "share_visitor" && item.ShareVisitorID != nil {
		return *item.ShareVisitorID
	}
	return ""
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func (repository *SQLiteRepository) loadChildren(
	ctx context.Context,
	session *Session,
) error {
	itemRows, err := repository.db.QueryContext(ctx, `
		SELECT
			ri.id, ri.asset_id, ri.asset_version_id, assets.name,
			asset_versions.version_number, ri.position, ri.status,
			ri.created_at, ri.updated_at
		FROM review_items ri
		JOIN assets ON assets.id = ri.asset_id
		JOIN asset_versions ON asset_versions.id = ri.asset_version_id
		WHERE ri.review_session_id = ? AND ri.workspace_id = ?
		ORDER BY ri.position
	`, session.ID, session.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list review items: %w", err)
	}
	defer itemRows.Close()
	session.Items = make([]Item, 0)
	for itemRows.Next() {
		var item Item
		var createdAt, updatedAt string
		if err := itemRows.Scan(
			&item.ID, &item.AssetID, &item.AssetVersionID, &item.AssetName,
			&item.VersionNumber, &item.Position, &item.Status,
			&createdAt, &updatedAt,
		); err != nil {
			return fmt.Errorf("scan review item: %w", err)
		}
		item.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return err
		}
		item.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return err
		}
		session.Items = append(session.Items, item)
	}
	if err := itemRows.Err(); err != nil {
		return fmt.Errorf("iterate review items: %w", err)
	}

	participantRows, err := repository.db.QueryContext(ctx, `
		SELECT id, user_id, display_name, role, created_at
		FROM review_participants
		WHERE review_session_id = ? AND workspace_id = ?
		ORDER BY created_at, display_name
	`, session.ID, session.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list review participants: %w", err)
	}
	defer participantRows.Close()
	session.Participants = make([]Participant, 0)
	for participantRows.Next() {
		var participant Participant
		var userID sql.NullString
		var createdAt string
		if err := participantRows.Scan(
			&participant.ID, &userID, &participant.DisplayName,
			&participant.Role, &createdAt,
		); err != nil {
			return fmt.Errorf("scan review participant: %w", err)
		}
		if userID.Valid {
			participant.UserID = &userID.String
		}
		participant.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return err
		}
		session.Participants = append(session.Participants, participant)
	}
	return participantRows.Err()
}

func validateThreadAuthor(
	ctx context.Context,
	tx *sql.Tx,
	record threadRecord,
) (string, error) {
	var displayName string
	switch record.Author.Kind {
	case "user":
		err := tx.QueryRowContext(ctx, `
			SELECT users.display_name
			FROM users
			JOIN memberships ON memberships.user_id = users.id
			WHERE users.id = ? AND memberships.workspace_id = ?
				AND users.status = 'active'
				AND memberships.status = 'active'
		`, record.Author.ID, record.WorkspaceID).Scan(&displayName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidAuthor
		}
		if err != nil {
			return "", fmt.Errorf("validate comment user author: %w", err)
		}
	case "share_visitor":
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(share_visitors.display_name, '匿名访客')
			FROM share_visitors
			JOIN shares ON shares.id = share_visitors.share_id
			WHERE share_visitors.id = ? AND share_visitors.workspace_id = ?
				AND shares.review_session_id = ?
		`, record.Author.ID, record.WorkspaceID,
			record.ReviewSessionID).Scan(&displayName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidAuthor
		}
		if err != nil {
			return "", fmt.Errorf("validate comment visitor author: %w", err)
		}
	default:
		return "", ErrInvalidAuthor
	}
	return displayName, nil
}

func validateDecisionAuthor(
	ctx context.Context,
	tx *sql.Tx,
	record decisionRecord,
) (string, error) {
	var displayName string
	switch record.Actor.Kind {
	case "user":
		err := tx.QueryRowContext(ctx, `
			SELECT users.display_name
			FROM users
			JOIN memberships ON memberships.user_id = users.id
			WHERE users.id = ? AND memberships.workspace_id = ?
				AND users.status = 'active'
				AND memberships.status = 'active'
		`, record.Actor.ID, record.WorkspaceID).Scan(&displayName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidAuthor
		}
		if err != nil {
			return "", fmt.Errorf("validate decision user actor: %w", err)
		}
	case "share_visitor":
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(share_visitors.display_name, '匿名访客')
			FROM share_visitors
			JOIN shares ON shares.id = share_visitors.share_id
			WHERE share_visitors.id = ? AND share_visitors.workspace_id = ?
				AND shares.review_session_id = ?
		`, record.Actor.ID, record.WorkspaceID,
			record.ReviewSessionID).Scan(&displayName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidAuthor
		}
		if err != nil {
			return "", fmt.Errorf("validate decision visitor actor: %w", err)
		}
	default:
		return "", ErrInvalidAuthor
	}
	return displayName, nil
}

func validateDecisionPolicy(
	ctx context.Context,
	tx *sql.Tx,
	record decisionRecord,
	decisionRule string,
	responsibleUserID sql.NullString,
) error {
	switch decisionRule {
	case "any_reviewer":
		return nil
	case "responsible_only":
		if record.Actor.Kind != "user" ||
			!responsibleUserID.Valid ||
			record.Actor.ID != responsibleUserID.String {
			return ErrDecisionForbidden
		}
		return nil
	case "all_reviewers":
		if record.Actor.Kind != "user" {
			return ErrDecisionForbidden
		}
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM review_participants
			WHERE workspace_id = ? AND review_session_id = ?
				AND user_id = ? AND role = 'reviewer'
		`, record.WorkspaceID, record.ReviewSessionID,
			record.Actor.ID).Scan(&count); err != nil {
			return fmt.Errorf("validate review decision participant: %w", err)
		}
		if count != 1 {
			return ErrDecisionForbidden
		}
		return nil
	default:
		return ErrDecisionForbidden
	}
}

func allReviewersApproved(
	ctx context.Context,
	tx *sql.Tx,
	record decisionRecord,
) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id
		FROM review_participants
		WHERE workspace_id = ? AND review_session_id = ?
			AND role = 'reviewer' AND user_id IS NOT NULL
		ORDER BY created_at, id
	`, record.WorkspaceID, record.ReviewSessionID)
	if err != nil {
		return false, fmt.Errorf("list decision reviewers: %w", err)
	}
	reviewerIDs := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return false, fmt.Errorf("scan decision reviewer: %w", err)
		}
		reviewerIDs = append(reviewerIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("iterate decision reviewers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close decision reviewers: %w", err)
	}
	if len(reviewerIDs) == 0 {
		return false, ErrDecisionForbidden
	}

	for _, userID := range reviewerIDs {
		var decision string
		var query string
		args := []any{
			record.WorkspaceID,
			record.ReviewSessionID,
			userID,
		}
		if record.ReviewItemID == nil {
			query = `
				SELECT decision
				FROM review_decisions
				WHERE workspace_id = ? AND review_session_id = ?
					AND actor_user_id = ? AND review_item_id IS NULL
				ORDER BY created_at DESC, id DESC
				LIMIT 1
			`
		} else {
			query = `
				SELECT decision
				FROM review_decisions
				WHERE workspace_id = ? AND review_session_id = ?
					AND actor_user_id = ? AND review_item_id = ?
				ORDER BY created_at DESC, id DESC
				LIMIT 1
			`
			args = append(args, *record.ReviewItemID)
		}
		err := tx.QueryRowContext(ctx, query, args...).Scan(&decision)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read latest reviewer decision: %w", err)
		}
		if decision != "approved" {
			return false, nil
		}
	}
	return true, nil
}

func (repository *SQLiteRepository) loadComments(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	threadID string,
) ([]Comment, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT
			c.id, c.author_kind,
			COALESCE(c.author_user_id, c.author_visitor_id, ''),
			CASE c.author_kind
				WHEN 'user' THEN COALESCE(users.display_name, '用户')
				WHEN 'share_visitor' THEN COALESCE(share_visitors.display_name, '匿名访客')
				ELSE '系统'
			END,
			c.body, c.edited_at, c.created_at
		FROM comments c
		LEFT JOIN users ON users.id = c.author_user_id
		LEFT JOIN share_visitors ON share_visitors.id = c.author_visitor_id
		WHERE c.thread_id = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at
		LIMIT ?
	`, threadID, maxCommentsPerThread)
	if err != nil {
		return nil, fmt.Errorf("list thread comments: %w", err)
	}
	defer rows.Close()
	items := make([]Comment, 0)
	for rows.Next() {
		var item Comment
		var editedAt sql.NullString
		var createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.Author.Kind,
			&item.Author.ID,
			&item.Author.DisplayName,
			&item.Body,
			&editedAt,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan thread comment: %w", err)
		}
		item.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		if editedAt.Valid {
			value, err := parseTime(editedAt.String)
			if err != nil {
				return nil, err
			}
			item.EditedAt = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread comments: %w", err)
	}
	attachments, err := repository.loadCommentAttachments(ctx, queryer, threadID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].Attachments = attachments[items[index].ID]
	}
	return items, nil
}

func (repository *SQLiteRepository) loadCommentAttachments(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	threadID string,
) (map[string][]CommentAttachment, error) {
	rows, err := queryer.QueryContext(ctx, attachmentSelect+`
		WHERE ca.thread_id = ? AND ca.comment_id IS NOT NULL
			AND ca.status = 'ready'
		ORDER BY ca.created_at, ca.id
	`, threadID)
	if err != nil {
		return nil, fmt.Errorf("list comment attachments: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]CommentAttachment)
	for rows.Next() {
		item, scanErr := scanCommentAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if item.CommentID != nil {
			result[*item.CommentID] = append(result[*item.CommentID], item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment attachments: %w", err)
	}
	return result, nil
}

const decisionSelect = `
	SELECT
		rd.id, rd.workspace_id, rd.review_session_id, rd.review_item_id,
		rd.actor_kind,
		COALESCE(rd.actor_user_id, rd.actor_visitor_id, ''),
		CASE rd.actor_kind
			WHEN 'user' THEN COALESCE(users.display_name, '用户')
			ELSE COALESCE(share_visitors.display_name, '匿名访客')
		END,
		rd.decision, rd.note, rd.created_at
	FROM review_decisions rd
	LEFT JOIN users ON users.id = rd.actor_user_id
	LEFT JOIN share_visitors ON share_visitors.id = rd.actor_visitor_id
`

const attachmentSelect = `
	SELECT
		ca.id, ca.workspace_id, ca.review_session_id, ca.review_item_id,
		ca.thread_id, ca.comment_id, ca.source_type,
		ca.uploaded_by_user_id, ca.share_visitor_id,
		ca.authorized_root_id, ca.object_key, ca.original_filename,
		ca.mime_type, ca.size_bytes, ca.width, ca.height,
		ca.upload_security_policy, ca.status, ca.result_code, ca.message,
		ca.created_at, ca.updated_at, ca.attached_at
	FROM comment_attachments ca
`

const threadSelect = `
	SELECT
		ct.id, ct.workspace_id, ct.review_session_id, ct.review_item_id,
		ct.asset_version_id, ct.status, ct.author_kind,
		COALESCE(ct.author_user_id, ct.author_visitor_id, ''),
		CASE ct.author_kind
			WHEN 'user' THEN COALESCE(author_users.display_name, '用户')
			ELSE COALESCE(share_visitors.display_name, '匿名访客')
		END,
		ct.resolved_by_user_id,
		COALESCE(resolver_users.display_name, ''),
		ct.resolved_at,
		a.id, a.kind, a.time_start_us, a.time_end_us,
		a.geometry_version, a.geometry_json, a.created_at, a.updated_at,
		ct.revision, ct.created_at, ct.updated_at
	FROM comment_threads ct
	JOIN annotations a ON a.thread_id = ct.id
	LEFT JOIN users author_users ON author_users.id = ct.author_user_id
	LEFT JOIN users resolver_users ON resolver_users.id = ct.resolved_by_user_id
	LEFT JOIN share_visitors ON share_visitors.id = ct.author_visitor_id
`

func scanCommentAttachment(scanner rowScanner) (CommentAttachment, error) {
	var item CommentAttachment
	var threadID, commentID sql.NullString
	var userID, visitorID sql.NullString
	var width, height sql.NullInt64
	var resultCode, message sql.NullString
	var createdAt, updatedAt string
	var attachedAt sql.NullString
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ReviewSessionID,
		&item.ReviewItemID,
		&threadID,
		&commentID,
		&item.SourceType,
		&userID,
		&visitorID,
		&item.AuthorizedRootID,
		&item.ObjectKey,
		&item.OriginalFilename,
		&item.MIMEType,
		&item.SizeBytes,
		&width,
		&height,
		&item.UploadSecurityPolicy,
		&item.Status,
		&resultCode,
		&message,
		&createdAt,
		&updatedAt,
		&attachedAt,
	)
	if err != nil {
		return CommentAttachment{}, fmt.Errorf("scan comment attachment: %w", err)
	}
	if threadID.Valid {
		item.ThreadID = &threadID.String
	}
	if commentID.Valid {
		item.CommentID = &commentID.String
	}
	if userID.Valid {
		item.UploadedByUserID = &userID.String
	}
	if visitorID.Valid {
		item.ShareVisitorID = &visitorID.String
	}
	if width.Valid {
		value := int(width.Int64)
		item.Width = &value
	}
	if height.Valid {
		value := int(height.Int64)
		item.Height = &value
	}
	if resultCode.Valid {
		item.ResultCode = resultCode.String
	}
	if message.Valid {
		item.Message = &message.String
	}
	var parseErr error
	item.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return CommentAttachment{}, parseErr
	}
	item.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return CommentAttachment{}, parseErr
	}
	if attachedAt.Valid {
		value, err := parseTime(attachedAt.String)
		if err != nil {
			return CommentAttachment{}, err
		}
		item.AttachedAt = &value
	}
	return item, nil
}

func scanCommentThread(scanner rowScanner) (CommentThread, error) {
	var item CommentThread
	var timeStartUs, timeEndUs sql.NullInt64
	var geometryJSON sql.NullString
	var resolvedByUserID, resolvedByDisplayName, resolvedAt sql.NullString
	var annotationCreatedAt, annotationUpdatedAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ReviewSessionID,
		&item.ReviewItemID,
		&item.AssetVersionID,
		&item.Status,
		&item.Author.Kind,
		&item.Author.ID,
		&item.Author.DisplayName,
		&resolvedByUserID,
		&resolvedByDisplayName,
		&resolvedAt,
		&item.Annotation.ID,
		&item.Annotation.Kind,
		&timeStartUs,
		&timeEndUs,
		&item.Annotation.GeometryVersion,
		&geometryJSON,
		&annotationCreatedAt,
		&annotationUpdatedAt,
		&item.Revision,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return CommentThread{}, fmt.Errorf("scan comment thread: %w", err)
	}
	if resolvedByUserID.Valid {
		item.ResolvedBy = &CommentAuthor{
			Kind:        "user",
			ID:          resolvedByUserID.String,
			DisplayName: resolvedByDisplayName.String,
		}
	}
	if resolvedAt.Valid {
		value, err := parseTime(resolvedAt.String)
		if err != nil {
			return CommentThread{}, err
		}
		item.ResolvedAt = &value
	}
	if timeStartUs.Valid {
		value := timeStartUs.Int64
		item.Annotation.TimeStartUs = &value
	}
	if timeEndUs.Valid {
		value := timeEndUs.Int64
		item.Annotation.TimeEndUs = &value
	}
	if geometryJSON.Valid {
		var geometry AnnotationGeometry
		if err := json.Unmarshal([]byte(geometryJSON.String), &geometry); err != nil {
			return CommentThread{}, fmt.Errorf(
				"decode annotation geometry: %w",
				err,
			)
		}
		item.Annotation.Geometry = &geometry
	}
	if item.Annotation.CreatedAt, err = parseTime(annotationCreatedAt); err != nil {
		return CommentThread{}, err
	}
	if item.Annotation.UpdatedAt, err = parseTime(annotationUpdatedAt); err != nil {
		return CommentThread{}, err
	}
	if item.CreatedAt, err = parseTime(createdAt); err != nil {
		return CommentThread{}, err
	}
	if item.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return CommentThread{}, err
	}
	return item, nil
}

func encodeAnnotationGeometry(item *AnnotationGeometry) (any, error) {
	if item == nil {
		return nil, nil
	}
	value, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode annotation geometry: %w", err)
	}
	return string(value), nil
}

func ensureNameAvailable(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projectID string,
	name string,
	excludeID string,
) error {
	query := `
		SELECT COUNT(*)
		FROM review_sessions
		WHERE workspace_id = ? AND project_id = ?
			AND lower(name) = lower(?)`
	args := []any{workspaceID, projectID, name}
	if excludeID != "" {
		query += ` AND id != ?`
		args = append(args, excludeID)
	}
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return fmt.Errorf("validate review name: %w", err)
	}
	if count > 0 {
		return ErrNameConflict
	}
	return nil
}

func validateProject(
	ctx context.Context,
	tx *sql.Tx,
	session Session,
) error {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM projects
		WHERE id = ? AND workspace_id = ? AND status = 'active'
			AND deleted_at IS NULL
	`, session.ProjectID, session.WorkspaceID).Scan(&count)
	if err != nil {
		return fmt.Errorf("validate review project: %w", err)
	}
	if count != 1 {
		return ErrInvalidProject
	}
	if session.CollectionID != nil {
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM collections
			WHERE id = ? AND workspace_id = ? AND project_id = ?
				AND deleted_at IS NULL
		`, *session.CollectionID, session.WorkspaceID, session.ProjectID).Scan(&count)
		if err != nil {
			return fmt.Errorf("validate review collection: %w", err)
		}
		if count != 1 {
			return ErrInvalidProject
		}
	}
	return nil
}

func validateItem(
	ctx context.Context,
	tx *sql.Tx,
	session Session,
	item Item,
) error {
	var count int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM assets
		JOIN asset_versions
			ON asset_versions.asset_id = assets.id
			AND asset_versions.workspace_id = assets.workspace_id
		JOIN project_assets
			ON project_assets.asset_id = assets.id
			AND project_assets.workspace_id = assets.workspace_id
			AND project_assets.project_id = ?
			AND project_assets.status = 'active'
		WHERE assets.id = ? AND asset_versions.id = ?
			AND assets.workspace_id = ?
			AND assets.deleted_at IS NULL
			AND asset_versions.deleted_at IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM upload_checks
				WHERE upload_checks.workspace_id = assets.workspace_id
					AND upload_checks.asset_version_id = asset_versions.id
					AND upload_checks.status <> 'ready'
			)
	`, session.ProjectID, item.AssetID, item.AssetVersionID,
		session.WorkspaceID).Scan(&count)
	if err != nil {
		return fmt.Errorf("validate review item: %w", err)
	}
	if count != 1 {
		return ErrInvalidItem
	}
	return nil
}

func replaceParticipants(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	sessionID string,
	participants []Participant,
	now string,
) error {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM review_participants WHERE review_session_id = ?`,
		sessionID,
	); err != nil {
		return fmt.Errorf("replace review participants: %w", err)
	}
	for _, participant := range participants {
		if participant.UserID != nil {
			displayName, err := activeMemberDisplayName(
				ctx,
				tx,
				workspaceID,
				*participant.UserID,
			)
			if err != nil {
				return err
			}
			participant.DisplayName = displayName
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO review_participants (
				id, workspace_id, review_session_id, user_id,
				display_name, role, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, participant.ID, workspaceID, sessionID, participant.UserID,
			participant.DisplayName, participant.Role, now); err != nil {
			return fmt.Errorf("create review participant: %w", err)
		}
	}
	return nil
}

func validateResponsible(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	userID *string,
) error {
	if userID == nil {
		return nil
	}
	_, err := activeMemberDisplayName(ctx, tx, workspaceID, *userID)
	return err
}

func activeMemberDisplayName(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	userID string,
) (string, error) {
	var displayName string
	err := tx.QueryRowContext(ctx, `
		SELECT users.display_name
		FROM memberships
		JOIN users ON users.id = memberships.user_id
		WHERE memberships.workspace_id = ? AND memberships.user_id = ?
			AND memberships.status = 'active' AND users.status = 'active'
	`, workspaceID, userID).Scan(&displayName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidParticipant
	}
	if err != nil {
		return "", fmt.Errorf("validate review member: %w", err)
	}
	return displayName, nil
}

func requireChanged(
	ctx context.Context,
	tx *sql.Tx,
	result sql.Result,
	workspaceID string,
	id string,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read review update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM review_sessions WHERE id = ? AND workspace_id = ?
	`, id, workspaceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read review after update conflict: %w", err)
	}
	if status == "closed" {
		return ErrInvalidState
	}
	return ErrRevisionConflict
}

type rowScanner interface {
	Scan(...any) error
}

func scanDecision(scanner rowScanner) (Decision, error) {
	var item Decision
	var reviewItemID, note sql.NullString
	var createdAt string
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.ReviewSessionID,
		&reviewItemID,
		&item.Actor.Kind,
		&item.Actor.ID,
		&item.Actor.DisplayName,
		&item.Decision,
		&note,
		&createdAt,
	)
	if err != nil {
		return Decision{}, err
	}
	if reviewItemID.Valid {
		item.ReviewItemID = &reviewItemID.String
	}
	if note.Valid {
		item.Note = &note.String
	}
	var parseErr error
	item.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return Decision{}, parseErr
	}
	return item, nil
}

func scanSession(scanner rowScanner) (Session, error) {
	var session Session
	var collectionID, dueAt, closedAt sql.NullString
	var templateID, responsibleUserID sql.NullString
	var templateRevision sql.NullInt64
	var createdAt, updatedAt string
	err := scanner.Scan(
		&session.ID, &session.WorkspaceID, &session.ProjectID,
		&session.ProjectName, &collectionID, &session.Name, &session.Status,
		&dueAt, &session.CreatedBy, &templateID, &templateRevision,
		&responsibleUserID, &session.ResponsibleName, &session.AllowDownload,
		&session.DecisionRule, &session.Revision, &createdAt, &updatedAt,
		&closedAt,
	)
	if err != nil {
		return Session{}, err
	}
	if collectionID.Valid {
		session.CollectionID = &collectionID.String
	}
	if templateID.Valid {
		session.TemplateID = &templateID.String
	}
	if templateRevision.Valid {
		value := int(templateRevision.Int64)
		session.TemplateRevision = &value
	}
	if responsibleUserID.Valid {
		session.ResponsibleUserID = &responsibleUserID.String
	}
	var parseErr error
	if session.CreatedAt, parseErr = parseTime(createdAt); parseErr != nil {
		return Session{}, parseErr
	}
	if session.UpdatedAt, parseErr = parseTime(updatedAt); parseErr != nil {
		return Session{}, parseErr
	}
	if dueAt.Valid {
		parsed, err := parseTime(dueAt.String)
		if err != nil {
			return Session{}, err
		}
		session.DueAt = &parsed
	}
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return Session{}, err
		}
		session.ClosedAt = &parsed
	}
	return session, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse review time: %w", err)
	}
	return parsed, nil
}
