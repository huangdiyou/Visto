package share

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

func (repository *SQLiteRepository) List(
	ctx context.Context,
	workspaceID string,
	now time.Time,
) ([]Share, error) {
	rows, err := repository.db.QueryContext(ctx, shareSelect+`
		WHERE shares.workspace_id = ?
		ORDER BY shares.updated_at DESC, shares.created_at DESC
	`, formatTime(now), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()

	items := make([]Share, 0)
	for rows.Next() {
		item, scanErr := scanShare(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shares: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close share rows: %w", err)
	}
	for index := range items {
		links, err := repository.loadLinks(
			ctx,
			repository.db,
			items[index].WorkspaceID,
			items[index].ID,
		)
		if err != nil {
			return nil, err
		}
		items[index].Links = links
	}
	return items, nil
}

func (repository *SQLiteRepository) Get(
	ctx context.Context,
	workspaceID string,
	id string,
	now time.Time,
) (Share, error) {
	item, err := repository.get(ctx, repository.db, workspaceID, id, now)
	if err != nil {
		return Share{}, err
	}
	item.Links, err = repository.loadLinks(ctx, repository.db, workspaceID, id)
	return item, err
}

func (repository *SQLiteRepository) GetByLink(
	ctx context.Context,
	workspaceID string,
	linkID string,
	now time.Time,
) (Share, error) {
	item, err := scanShare(repository.db.QueryRowContext(ctx, shareSelect+`
		JOIN share_links ON share_links.share_id = shares.id
		WHERE share_links.id = ? AND shares.workspace_id = ?
	`, formatTime(now), linkID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("get share by link: %w", err)
	}
	item.Links, err = repository.loadLinks(ctx, repository.db, workspaceID, item.ID)
	return item, err
}

func (repository *SQLiteRepository) Create(
	ctx context.Context,
	record createRecord,
) (Share, Link, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, Link{}, fmt.Errorf("begin share creation: %w", err)
	}
	defer tx.Rollback()

	var reviewName string
	err = tx.QueryRowContext(ctx, `
		SELECT name
		FROM review_sessions
		WHERE id = ? AND workspace_id = ? AND status = 'open'
			AND EXISTS (
				SELECT 1 FROM review_items
				WHERE review_items.review_session_id = review_sessions.id
			)
	`, *record.ReviewSessionID, record.WorkspaceID).Scan(&reviewName)
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, Link{}, ErrInvalidReview
	}
	if err != nil {
		return Share{}, Link{}, fmt.Errorf("validate share review session: %w", err)
	}

	now := formatTime(record.Now)
	var expiresAt any
	if record.ExpiresAt != nil {
		expiresAt = formatTime(record.ExpiresAt.UTC())
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO shares (
			id, workspace_id, review_session_id, name, status,
			allow_comment, allow_download, require_nickname, expires_at,
			max_visits, password_hash, password_secret_ref, created_by,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, record.ID, record.WorkspaceID, record.ReviewSessionID, record.Name,
		record.AllowComment, record.AllowDownload, record.RequireNickname,
		expiresAt, record.MaxVisits, nullString(record.PasswordHash),
		nullableStringPtr(record.PasswordSecretRef), record.CreatedBy, now, now); err != nil {
		return Share{}, Link{}, fmt.Errorf("create share: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO share_links (
			id, workspace_id, share_id, token_digest, token_prefix,
			token_secret_ref, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
	`, record.LinkID, record.WorkspaceID, record.ID, record.TokenDigest,
		record.TokenPrefix, nullString(record.TokenSecretRef), now); err != nil {
		return Share{}, Link{}, fmt.Errorf("create share link: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Share{}, Link{}, fmt.Errorf("commit share creation: %w", err)
	}
	item, err := repository.Get(
		ctx,
		record.WorkspaceID,
		record.ID,
		record.Now,
	)
	if err != nil {
		return Share{}, Link{}, err
	}
	return item, item.Links[0], nil
}

func (repository *SQLiteRepository) CreateVisitorCode(
	ctx context.Context,
	record visitorCodeRecord,
) (VisitorCode, error) {
	var count int
	if err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM shares
		WHERE id = ? AND workspace_id = ? AND status = 'active'
			AND (expires_at IS NULL OR expires_at > ?)
	`, record.ShareID, record.WorkspaceID, formatTime(record.Now)).Scan(&count); err != nil {
		return VisitorCode{}, fmt.Errorf("validate share for visitor code: %w", err)
	}
	if count != 1 {
		return VisitorCode{}, ErrNotFound
	}

	now := formatTime(record.Now)
	var expiresAt any
	if record.ExpiresAt != nil {
		expiresAt = formatTime(record.ExpiresAt.UTC())
	}
	if _, err := repository.db.ExecContext(ctx, `
		INSERT INTO share_visitor_codes (
			id, workspace_id, share_id, code_digest, code_prefix,
			display_name, status, expires_at, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)
	`, record.ID, record.WorkspaceID, record.ShareID, record.CodeDigest,
		record.CodePrefix, record.DisplayName, expiresAt, record.CreatedBy,
		now); err != nil {
		return VisitorCode{}, fmt.Errorf("create share visitor code: %w", err)
	}
	return VisitorCode{
		ID:          record.ID,
		ShareID:     record.ShareID,
		CodePrefix:  record.CodePrefix,
		DisplayName: record.DisplayName,
		Status:      "active",
		ExpiresAt:   record.ExpiresAt,
		CreatedBy:   record.CreatedBy,
		CreatedAt:   record.Now,
	}, nil
}

func (repository *SQLiteRepository) Update(
	ctx context.Context,
	record updateRecord,
) (Share, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, fmt.Errorf("begin share update: %w", err)
	}
	defer tx.Rollback()

	var expiresAt any
	if record.ExpiresAt != nil {
		expiresAt = formatTime(record.ExpiresAt.UTC())
	}
	passwordExpression := "password_hash"
	passwordSecretExpression := "password_secret_ref"
	args := []any{
		record.Name,
		record.AllowComment,
		record.AllowDownload,
		record.RequireNickname,
		expiresAt,
	}
	if record.SetPasswordHash {
		passwordExpression = "?"
		value := any(nil)
		if record.PasswordHash != nil {
			value = *record.PasswordHash
		}
		args = append(args, value)
	}
	if record.SetPasswordSecretRef {
		passwordSecretExpression = "?"
		args = append(args, nullableStringPtr(record.PasswordSecretRef))
	}
	args = append(args, formatTime(record.Now), record.ID, record.WorkspaceID,
		record.Revision, formatTime(record.Now))
	result, err := tx.ExecContext(ctx, `
		UPDATE shares
		SET name = ?, allow_comment = ?, allow_download = ?,
			require_nickname = ?, expires_at = ?,
			password_hash = `+passwordExpression+`,
			password_secret_ref = `+passwordSecretExpression+`,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND status = 'active'
			AND (expires_at IS NULL OR expires_at > ?)
	`, args...)
	if err != nil {
		return Share{}, fmt.Errorf("update share: %w", err)
	}
	if err := requireShareChanged(
		ctx,
		tx,
		result,
		record.WorkspaceID,
		record.ID,
		record.Revision,
		record.Now,
	); err != nil {
		return Share{}, err
	}
	if record.SetPasswordHash {
		if _, err := tx.ExecContext(ctx, `
			UPDATE share_visitor_sessions
			SET revoked_at = ?
			WHERE share_id = ? AND revoked_at IS NULL
		`, formatTime(record.Now), record.ID); err != nil {
			return Share{}, fmt.Errorf("revoke sessions after password update: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Share{}, fmt.Errorf("commit share update: %w", err)
	}
	return repository.Get(ctx, record.WorkspaceID, record.ID, record.Now)
}

func (repository *SQLiteRepository) Revoke(
	ctx context.Context,
	input StateInput,
	now time.Time,
) (Share, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Share{}, fmt.Errorf("begin share revocation: %w", err)
	}
	defer tx.Rollback()
	formattedNow := formatTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE shares
		SET status = 'revoked', revoked_at = ?, updated_at = ?,
			revision = revision + 1
		WHERE id = ? AND workspace_id = ? AND revision = ?
			AND status = 'active'
	`, formattedNow, formattedNow, input.ID, input.WorkspaceID, input.Revision)
	if err != nil {
		return Share{}, fmt.Errorf("revoke share: %w", err)
	}
	if err := requireShareChanged(
		ctx,
		tx,
		result,
		input.WorkspaceID,
		input.ID,
		input.Revision,
		now,
	); err != nil {
		return Share{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_links
		SET status = 'revoked', revoked_at = ?
		WHERE share_id = ? AND status = 'active'
	`, formattedNow, input.ID); err != nil {
		return Share{}, fmt.Errorf("revoke share links: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_sessions
		SET revoked_at = ?
		WHERE share_id = ? AND revoked_at IS NULL
	`, formattedNow, input.ID); err != nil {
		return Share{}, fmt.Errorf("revoke share sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_codes
		SET status = 'revoked', revoked_at = ?
		WHERE share_id = ? AND status = 'active'
	`, formattedNow, input.ID); err != nil {
		return Share{}, fmt.Errorf("revoke share visitor codes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Share{}, fmt.Errorf("commit share revocation: %w", err)
	}
	return repository.Get(ctx, input.WorkspaceID, input.ID, now)
}

func (repository *SQLiteRepository) CreateLink(
	ctx context.Context,
	workspaceID string,
	shareID string,
	linkID string,
	tokenDigest string,
	tokenSecretRef string,
	tokenPrefix string,
	now time.Time,
) (Link, error) {
	var count int
	err := repository.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM shares
		WHERE id = ? AND workspace_id = ? AND status = 'active'
			AND (expires_at IS NULL OR expires_at > ?)
	`, shareID, workspaceID, formatTime(now)).Scan(&count)
	if err != nil {
		return Link{}, fmt.Errorf("validate share for link: %w", err)
	}
	if count != 1 {
		return Link{}, ErrInvalidState
	}
	if _, err := repository.db.ExecContext(ctx, `
		INSERT INTO share_links (
			id, workspace_id, share_id, token_digest, token_prefix,
			token_secret_ref, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'active', ?)
	`, linkID, workspaceID, shareID, tokenDigest, tokenPrefix,
		nullString(tokenSecretRef), formatTime(now)); err != nil {
		return Link{}, fmt.Errorf("create share link: %w", err)
	}
	return Link{
		ID:             linkID,
		ShareID:        shareID,
		TokenPrefix:    tokenPrefix,
		TokenSecretRef: optionalString(tokenSecretRef),
		Status:         "active",
		CreatedAt:      now,
	}, nil
}

func (repository *SQLiteRepository) RevokeLink(
	ctx context.Context,
	workspaceID string,
	linkID string,
	now time.Time,
) (Link, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, fmt.Errorf("begin share link revocation: %w", err)
	}
	defer tx.Rollback()
	formattedNow := formatTime(now)
	result, err := tx.ExecContext(ctx, `
		UPDATE share_links
		SET status = 'revoked', revoked_at = ?
		WHERE id = ? AND workspace_id = ? AND status = 'active'
	`, formattedNow, linkID, workspaceID)
	if err != nil {
		return Link{}, fmt.Errorf("revoke share link: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Link{}, fmt.Errorf("read share link revocation result: %w", err)
	}
	if affected == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM share_links WHERE id = ? AND workspace_id = ?
		`, linkID, workspaceID).Scan(&count); err != nil {
			return Link{}, fmt.Errorf("read share link after conflict: %w", err)
		}
		if count == 0 {
			return Link{}, ErrNotFound
		}
		return Link{}, ErrInvalidState
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_sessions
		SET revoked_at = ?
		WHERE share_link_id = ? AND revoked_at IS NULL
	`, formattedNow, linkID); err != nil {
		return Link{}, fmt.Errorf("revoke link sessions: %w", err)
	}
	link, err := scanLink(tx.QueryRowContext(ctx, `
		SELECT id, share_id, token_prefix, token_secret_ref, status, created_at,
			last_used_at, revoked_at
		FROM share_links
		WHERE id = ? AND workspace_id = ?
	`, linkID, workspaceID))
	if err != nil {
		return Link{}, err
	}
	if err := tx.Commit(); err != nil {
		return Link{}, fmt.Errorf("commit share link revocation: %w", err)
	}
	return link, nil
}

func (repository *SQLiteRepository) OpenEntry(
	ctx context.Context,
	tokenDigest string,
	record sessionRecord,
) (sessionAccess, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionAccess{}, fmt.Errorf("begin share entry: %w", err)
	}
	defer tx.Rollback()

	var workspaceID, shareID, linkID, passwordHash string
	var expiresAt sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT shares.workspace_id, shares.id, share_links.id,
			COALESCE(shares.password_hash, ''), shares.expires_at
		FROM share_links
		JOIN shares ON shares.id = share_links.share_id
		WHERE share_links.token_digest = ?
			AND share_links.status = 'active'
			AND shares.status = 'active'
			AND (shares.expires_at IS NULL OR shares.expires_at > ?)
	`, tokenDigest, formatTime(record.Now)).Scan(
		&workspaceID,
		&shareID,
		&linkID,
		&passwordHash,
		&expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionAccess{}, ErrEntryUnavailable
	}
	if err != nil {
		return sessionAccess{}, fmt.Errorf("resolve share entry: %w", err)
	}

	sessionExpiresAt := record.ExpiresAt
	if passwordHash != "" && record.UnverifiedExpiresAt.Before(sessionExpiresAt) {
		sessionExpiresAt = record.UnverifiedExpiresAt
	}
	if expiresAt.Valid {
		shareExpiry, err := parseTime(expiresAt.String)
		if err != nil {
			return sessionAccess{}, err
		}
		if shareExpiry.Before(sessionExpiresAt) {
			sessionExpiresAt = shareExpiry
		}
	}
	now := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM share_visitor_sessions
		WHERE share_link_id = ? AND (revoked_at IS NOT NULL OR expires_at <= ?)
	`, linkID, now); err != nil {
		return sessionAccess{}, fmt.Errorf("prune expired share sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM share_visitor_sessions
		WHERE id IN (
			SELECT id FROM share_visitor_sessions
			WHERE share_link_id = ? AND revoked_at IS NULL
			ORDER BY last_seen_at ASC, created_at ASC
			LIMIT -1 OFFSET 200
		)
	`, linkID); err != nil {
		return sessionAccess{}, fmt.Errorf("bound active share sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM share_visitors
		WHERE workspace_id = ? AND share_id = ?
			AND NOT EXISTS (
				SELECT 1 FROM share_visitor_sessions
				WHERE share_visitor_sessions.share_visitor_id = share_visitors.id
			)
	`, workspaceID, shareID); err != nil {
		return sessionAccess{}, fmt.Errorf("prune orphaned share visitors: %w", err)
	}
	var verifiedAt any
	if passwordHash == "" {
		verifiedAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO share_visitors (
			id, workspace_id, share_id, display_name, identity_method,
			verified_at, created_at, last_seen_at
		) VALUES (?, ?, ?, NULL, 'anonymous', NULL, ?, ?)
	`, record.VisitorID, workspaceID, shareID, now, now); err != nil {
		return sessionAccess{}, fmt.Errorf("create anonymous share visitor: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO share_visitor_sessions (
			id, workspace_id, share_id, share_link_id, share_visitor_id, session_digest,
			verified_password_at, expires_at, created_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, record.ID, workspaceID, shareID, linkID, record.VisitorID,
		record.SessionDigest,
		verifiedAt, formatTime(sessionExpiresAt), now, now); err != nil {
		return sessionAccess{}, fmt.Errorf("create share visitor session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_links SET last_used_at = ? WHERE id = ?
	`, now, linkID); err != nil {
		return sessionAccess{}, fmt.Errorf("mark share link used: %w", err)
	}
	publicShare, err := repository.loadPublicShare(ctx, tx, workspaceID, shareID)
	if err != nil {
		return sessionAccess{}, err
	}
	if err := tx.Commit(); err != nil {
		return sessionAccess{}, fmt.Errorf("commit share entry: %w", err)
	}
	return sessionAccess{
		PublicShare: withAccessContext(withVisitor(publicShare, PublicVisitor{
			ID:             record.VisitorID,
			IdentityMethod: "anonymous",
			Identified:     false,
			Verified:       false,
		}), linkID, record.ID),
		PasswordHash: passwordHash,
		Verified:     passwordHash == "",
		SessionID:    record.ID,
		ShareLinkID:  linkID,
		SessionExpiresAt: sessionExpiresAt,
	}, nil
}

func (repository *SQLiteRepository) Session(
	ctx context.Context,
	sessionDigest string,
	now time.Time,
) (sessionAccess, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionAccess{}, fmt.Errorf("begin share session lookup: %w", err)
	}
	defer tx.Rollback()

	var sessionID, workspaceID, shareID, linkID, passwordHash string
	var verifiedAt, visitorID, visitorDisplayName, visitorMethod,
		visitorVerifiedAt sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT share_visitor_sessions.id, shares.workspace_id, shares.id,
			share_links.id,
			COALESCE(shares.password_hash, ''),
			share_visitor_sessions.verified_password_at,
			share_visitor_sessions.share_visitor_id,
			share_visitors.display_name,
			share_visitors.identity_method,
			share_visitors.verified_at
		FROM share_visitor_sessions
		JOIN share_links ON share_links.id = share_visitor_sessions.share_link_id
		JOIN shares ON shares.id = share_visitor_sessions.share_id
		LEFT JOIN share_visitors
			ON share_visitors.id = share_visitor_sessions.share_visitor_id
		WHERE share_visitor_sessions.session_digest = ?
			AND share_visitor_sessions.revoked_at IS NULL
			AND share_visitor_sessions.expires_at > ?
			AND share_links.status = 'active'
			AND shares.status = 'active'
			AND (shares.expires_at IS NULL OR shares.expires_at > ?)
	`, sessionDigest, formatTime(now), formatTime(now)).Scan(
		&sessionID,
		&workspaceID,
		&shareID,
		&linkID,
		&passwordHash,
		&verifiedAt,
		&visitorID,
		&visitorDisplayName,
		&visitorMethod,
		&visitorVerifiedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionAccess{}, ErrSessionNotFound
	}
	if err != nil {
		return sessionAccess{}, fmt.Errorf("resolve share session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_sessions SET last_seen_at = ? WHERE id = ?
	`, formatTime(now), sessionID); err != nil {
		return sessionAccess{}, fmt.Errorf("touch share session: %w", err)
	}
	if visitorID.Valid {
		if _, err := tx.ExecContext(ctx, `
			UPDATE share_visitors SET last_seen_at = ? WHERE id = ?
		`, formatTime(now), visitorID.String); err != nil {
			return sessionAccess{}, fmt.Errorf("touch share visitor: %w", err)
		}
	}
	publicShare, err := repository.loadPublicShare(ctx, tx, workspaceID, shareID)
	if err != nil {
		return sessionAccess{}, err
	}
	if err := tx.Commit(); err != nil {
		return sessionAccess{}, fmt.Errorf("commit share session lookup: %w", err)
	}
	return sessionAccess{
		PublicShare: withAccessContext(withVisitor(publicShare, publicVisitorFromNulls(
			visitorID,
			visitorDisplayName,
			visitorMethod,
			visitorVerifiedAt,
		)), linkID, sessionID),
		PasswordHash: passwordHash,
		Verified:     verifiedAt.Valid,
		SessionID:    sessionID,
		ShareLinkID:  linkID,
		SessionExpiresAt: now,
	}, nil
}

func (repository *SQLiteRepository) MarkSessionVerified(
	ctx context.Context,
	sessionDigest string,
	now time.Time,
	expiresAt time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE share_visitor_sessions
		SET verified_password_at = ?, last_seen_at = ?, expires_at = ?
		WHERE session_digest = ? AND revoked_at IS NULL AND expires_at > ?
	`, formatTime(now), formatTime(now), formatTime(expiresAt), sessionDigest, formatTime(now))
	if err != nil {
		return fmt.Errorf("verify share session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read share session verification result: %w", err)
	}
	if affected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (repository *SQLiteRepository) IdentifySession(
	ctx context.Context,
	sessionDigest string,
	record visitorIdentityRecord,
) (sessionAccess, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionAccess{}, fmt.Errorf("begin share identity update: %w", err)
	}
	defer tx.Rollback()

	session, err := repository.resolveSession(ctx, tx, sessionDigest, record.Now)
	if err != nil {
		return sessionAccess{}, err
	}
	if session.PasswordHash != "" && !session.PasswordVerified {
		return sessionAccess{}, ErrPasswordRequired
	}
	if record.IdentityMethod == "anonymous" && session.RequireNickname {
		return sessionAccess{}, ErrIdentityRequired
	}
	visitorID, err := repository.ensureSessionVisitor(
		ctx,
		tx,
		session,
		record.VisitorID,
		record.Now,
	)
	if err != nil {
		return sessionAccess{}, err
	}
	var displayName any
	if record.DisplayName != nil {
		displayName = *record.DisplayName
	}
	var verifiedAt any
	if record.VerifiedAt != nil {
		verifiedAt = formatTime(record.VerifiedAt.UTC())
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitors
		SET display_name = ?, identity_method = ?, verified_at = ?,
			last_seen_at = ?
		WHERE id = ?
	`, displayName, record.IdentityMethod, verifiedAt,
		formatTime(record.Now), visitorID); err != nil {
		return sessionAccess{}, fmt.Errorf("update share visitor identity: %w", err)
	}
	return repository.finishSessionAccess(
		ctx,
		tx,
		session,
		visitorID,
		record.Now,
	)
}

func (repository *SQLiteRepository) IdentifySessionWithCode(
	ctx context.Context,
	record visitorCodeUseRecord,
) (sessionAccess, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return sessionAccess{}, fmt.Errorf("begin share visitor code use: %w", err)
	}
	defer tx.Rollback()

	session, err := repository.resolveSession(
		ctx,
		tx,
		record.SessionDigest,
		record.Now,
	)
	if err != nil {
		return sessionAccess{}, err
	}
	if session.PasswordHash != "" && !session.PasswordVerified {
		return sessionAccess{}, ErrPasswordRequired
	}

	var codeID, displayName string
	err = tx.QueryRowContext(ctx, `
		SELECT id, display_name
		FROM share_visitor_codes
		WHERE share_id = ? AND code_digest = ? AND status = 'active'
			AND (expires_at IS NULL OR expires_at > ?)
	`, session.ShareID, record.CodeDigest, formatTime(record.Now)).Scan(
		&codeID,
		&displayName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionAccess{}, ErrVisitorCodeInvalid
	}
	if err != nil {
		return sessionAccess{}, fmt.Errorf("resolve share visitor code: %w", err)
	}

	visitorID, err := repository.ensureSessionVisitor(
		ctx,
		tx,
		session,
		record.VisitorID,
		record.Now,
	)
	if err != nil {
		return sessionAccess{}, err
	}
	now := formatTime(record.Now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitors
		SET display_name = ?, identity_method = 'verification_code',
			verified_at = ?, last_seen_at = ?
		WHERE id = ?
	`, displayName, now, now, visitorID); err != nil {
		return sessionAccess{}, fmt.Errorf("update share visitor from code: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_codes
		SET last_used_at = ?
		WHERE id = ?
	`, now, codeID); err != nil {
		return sessionAccess{}, fmt.Errorf("mark visitor code used: %w", err)
	}
	return repository.finishSessionAccess(
		ctx,
		tx,
		session,
		visitorID,
		record.Now,
	)
}

func (repository *SQLiteRepository) get(
	ctx context.Context,
	queryer rowQueryer,
	workspaceID string,
	id string,
	now time.Time,
) (Share, error) {
	item, err := scanShare(queryer.QueryRowContext(ctx, shareSelect+`
		WHERE shares.id = ? AND shares.workspace_id = ?
	`, formatTime(now), id, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("get share: %w", err)
	}
	return item, nil
}

func (repository *SQLiteRepository) loadLinks(
	ctx context.Context,
	queryer rowsQueryer,
	workspaceID string,
	shareID string,
) ([]Link, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT id, share_id, token_prefix, token_secret_ref, status, created_at,
			last_used_at, revoked_at
		FROM share_links
		WHERE workspace_id = ? AND share_id = ?
		ORDER BY created_at DESC
	`, workspaceID, shareID)
	if err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	defer rows.Close()
	items := make([]Link, 0)
	for rows.Next() {
		item, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate share links: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) loadPublicShare(
	ctx context.Context,
	queryer fullQueryer,
	workspaceID string,
	shareID string,
) (PublicShare, error) {
	var item PublicShare
	var reviewSessionID string
	var expiresAt sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT shares.id, shares.workspace_id, shares.name,
			review_sessions.id, review_sessions.name, review_sessions.status,
			COALESCE(NULLIF(settings.team_name, ''), workspaces.name),
			shares.allow_comment, shares.allow_download,
			shares.require_nickname, shares.expires_at
		FROM shares
		JOIN review_sessions ON review_sessions.id = shares.review_session_id
		JOIN workspaces ON workspaces.id = shares.workspace_id
		LEFT JOIN workspace_registration_settings settings
			ON settings.workspace_id = shares.workspace_id
		WHERE shares.id = ? AND shares.workspace_id = ?
	`, shareID, workspaceID).Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Name,
		&item.ReviewSessionID,
		&item.ReviewName,
		&item.ReviewStatus,
		&item.TeamName,
		&item.AllowComment,
		&item.AllowDownload,
		&item.RequireNickname,
		&expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicShare{}, ErrEntryUnavailable
	}
	if err != nil {
		return PublicShare{}, fmt.Errorf("read public share: %w", err)
	}
	item.AllowComment = item.AllowComment && item.ReviewStatus == "open"
	if expiresAt.Valid {
		value, err := parseTime(expiresAt.String)
		if err != nil {
			return PublicShare{}, err
		}
		item.ExpiresAt = &value
	}
	reviewSessionID = item.ReviewSessionID

	rows, err := queryer.QueryContext(ctx, `
		SELECT
			review_items.id,
			review_items.asset_version_id,
			assets.name,
			asset_versions.version_number,
			assets.type,
			media_probes.duration_us,
			media_probes.width,
			media_probes.height,
			(
				SELECT renditions.id
				FROM renditions
				WHERE renditions.workspace_id = review_items.workspace_id
					AND renditions.source_storage_object_id = version_files.storage_object_id
					AND renditions.source_fingerprint = asset_versions.source_fingerprint
					AND renditions.status = 'ready'
					AND renditions.kind IN ('proxy', 'hls', 'screen_preview', 'poster', 'thumbnail')
				ORDER BY CASE renditions.kind
					WHEN 'proxy' THEN 1
					WHEN 'hls' THEN 2
					WHEN 'screen_preview' THEN 3
					WHEN 'poster' THEN 4
					ELSE 5
				END
				LIMIT 1
			),
			(
				SELECT renditions.kind
				FROM renditions
				WHERE renditions.workspace_id = review_items.workspace_id
					AND renditions.source_storage_object_id = version_files.storage_object_id
					AND renditions.source_fingerprint = asset_versions.source_fingerprint
					AND renditions.status = 'ready'
					AND renditions.kind IN ('proxy', 'hls', 'screen_preview', 'poster', 'thumbnail')
				ORDER BY CASE renditions.kind
					WHEN 'proxy' THEN 1
					WHEN 'hls' THEN 2
					WHEN 'screen_preview' THEN 3
					WHEN 'poster' THEN 4
					ELSE 5
				END
				LIMIT 1
			),
			(
				SELECT renditions.id
				FROM renditions
				WHERE renditions.workspace_id = review_items.workspace_id
					AND renditions.source_storage_object_id = version_files.storage_object_id
					AND renditions.source_fingerprint = asset_versions.source_fingerprint
					AND renditions.status = 'ready'
					AND renditions.kind IN ('poster', 'thumbnail')
				ORDER BY CASE renditions.kind WHEN 'poster' THEN 1 ELSE 2 END
				LIMIT 1
			),
			version_files.storage_object_id,
			asset_versions.source_filename,
			asset_versions.source_mime,
			storage_objects.size_bytes
		FROM review_items
		JOIN assets ON assets.id = review_items.asset_id
		JOIN asset_versions ON asset_versions.id = review_items.asset_version_id
		JOIN version_files
			ON version_files.asset_version_id = asset_versions.id
			AND version_files.role = 'primary'
		JOIN storage_objects ON storage_objects.id = version_files.storage_object_id
		LEFT JOIN media_probes
			ON media_probes.storage_object_id = version_files.storage_object_id
			AND media_probes.status = 'succeeded'
		WHERE review_items.review_session_id = ?
			AND review_items.workspace_id = ?
			AND assets.deleted_at IS NULL
			AND asset_versions.deleted_at IS NULL
			AND storage_objects.status = 'available'
			AND storage_objects.deleted_at IS NULL
			AND NOT EXISTS (
				SELECT 1
				FROM upload_checks
				WHERE upload_checks.workspace_id = review_items.workspace_id
					AND upload_checks.asset_version_id = review_items.asset_version_id
					AND upload_checks.status <> 'ready'
			)
		ORDER BY review_items.position
	`, reviewSessionID, workspaceID)
	if err != nil {
		return PublicShare{}, fmt.Errorf("list public share items: %w", err)
	}
	defer rows.Close()
	item.Items = make([]PublicItem, 0)
	for rows.Next() {
		var publicItem PublicItem
		var previewID, previewKind, thumbnailID, sourceMIME sql.NullString
		var durationUs sql.NullInt64
		var width, height sql.NullInt64
		if err := rows.Scan(
			&publicItem.ID,
			&publicItem.AssetVersionID,
			&publicItem.AssetName,
			&publicItem.VersionNumber,
			&publicItem.MediaType,
			&durationUs,
			&width,
			&height,
			&previewID,
			&previewKind,
			&thumbnailID,
			&publicItem.SourceStorageObjectID,
			&publicItem.SourceFilename,
			&sourceMIME,
			&publicItem.SourceSizeBytes,
		); err != nil {
			return PublicShare{}, fmt.Errorf("scan public share item: %w", err)
		}
		if durationUs.Valid {
			value := durationUs.Int64
			publicItem.DurationUs = &value
		}
		if width.Valid {
			value := int(width.Int64)
			publicItem.Width = &value
		}
		if height.Valid {
			value := int(height.Int64)
			publicItem.Height = &value
		}
		if previewID.Valid {
			publicItem.PreviewRenditionID = &previewID.String
		}
		if previewKind.Valid {
			publicItem.PreviewRenditionKind = &previewKind.String
		}
		if thumbnailID.Valid {
			publicItem.ThumbnailRenditionID = &thumbnailID.String
		}
		if sourceMIME.Valid {
			publicItem.SourceMIME = &sourceMIME.String
		}
		item.Items = append(item.Items, publicItem)
	}
	if err := rows.Err(); err != nil {
		return PublicShare{}, fmt.Errorf("iterate public share items: %w", err)
	}
	return item, nil
}

const shareSelect = `
	SELECT
		shares.id,
		shares.workspace_id,
		shares.review_session_id,
		review_sessions.name,
		shares.name,
		CASE
			WHEN shares.status = 'active'
				AND shares.expires_at IS NOT NULL
				AND shares.expires_at <= ?
			THEN 'expired'
			ELSE shares.status
		END,
		shares.allow_comment,
		shares.allow_download,
		shares.require_nickname,
		shares.expires_at,
		shares.max_visits,
		CASE WHEN shares.password_hash IS NULL THEN 0 ELSE 1 END,
		shares.password_secret_ref,
		shares.created_by,
		shares.revision,
		shares.created_at,
		shares.updated_at,
		shares.revoked_at
	FROM shares
	LEFT JOIN review_sessions ON review_sessions.id = shares.review_session_id
`

type resolvedShareSession struct {
	SessionID        string
	WorkspaceID      string
	ShareID          string
	ShareLinkID      string
	PasswordHash     string
	PasswordVerified bool
	VisitorID        sql.NullString
	RequireNickname  bool
}

func (repository *SQLiteRepository) resolveSession(
	ctx context.Context,
	tx *sql.Tx,
	sessionDigest string,
	now time.Time,
) (resolvedShareSession, error) {
	var item resolvedShareSession
	var verifiedAt sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT share_visitor_sessions.id, shares.workspace_id, shares.id,
			share_links.id,
			COALESCE(shares.password_hash, ''),
			share_visitor_sessions.verified_password_at,
			share_visitor_sessions.share_visitor_id,
			shares.require_nickname
		FROM share_visitor_sessions
		JOIN share_links ON share_links.id = share_visitor_sessions.share_link_id
		JOIN shares ON shares.id = share_visitor_sessions.share_id
		WHERE share_visitor_sessions.session_digest = ?
			AND share_visitor_sessions.revoked_at IS NULL
			AND share_visitor_sessions.expires_at > ?
			AND share_links.status = 'active'
			AND shares.status = 'active'
			AND (shares.expires_at IS NULL OR shares.expires_at > ?)
	`, sessionDigest, formatTime(now), formatTime(now)).Scan(
		&item.SessionID,
		&item.WorkspaceID,
		&item.ShareID,
		&item.ShareLinkID,
		&item.PasswordHash,
		&verifiedAt,
		&item.VisitorID,
		&item.RequireNickname,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return resolvedShareSession{}, ErrSessionNotFound
	}
	if err != nil {
		return resolvedShareSession{}, fmt.Errorf("resolve share session: %w", err)
	}
	item.PasswordVerified = verifiedAt.Valid
	return item, nil
}

func (repository *SQLiteRepository) ensureSessionVisitor(
	ctx context.Context,
	tx *sql.Tx,
	session resolvedShareSession,
	fallbackVisitorID string,
	now time.Time,
) (string, error) {
	if session.VisitorID.Valid {
		return session.VisitorID.String, nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO share_visitors (
			id, workspace_id, share_id, display_name, identity_method,
			verified_at, created_at, last_seen_at
		) VALUES (?, ?, ?, NULL, 'anonymous', NULL, ?, ?)
	`, fallbackVisitorID, session.WorkspaceID, session.ShareID,
		formatTime(now), formatTime(now)); err != nil {
		return "", fmt.Errorf("create share visitor for session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_sessions SET share_visitor_id = ?
		WHERE id = ?
	`, fallbackVisitorID, session.SessionID); err != nil {
		return "", fmt.Errorf("attach share visitor to session: %w", err)
	}
	return fallbackVisitorID, nil
}

func (repository *SQLiteRepository) finishSessionAccess(
	ctx context.Context,
	tx *sql.Tx,
	session resolvedShareSession,
	visitorID string,
	now time.Time,
) (sessionAccess, error) {
	formattedNow := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitor_sessions SET last_seen_at = ? WHERE id = ?
	`, formattedNow, session.SessionID); err != nil {
		return sessionAccess{}, fmt.Errorf("touch share session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE share_visitors SET last_seen_at = ? WHERE id = ?
	`, formattedNow, visitorID); err != nil {
		return sessionAccess{}, fmt.Errorf("touch share visitor: %w", err)
	}

	var displayName, method, verifiedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT display_name, identity_method, verified_at
		FROM share_visitors
		WHERE id = ?
	`, visitorID).Scan(&displayName, &method, &verifiedAt); err != nil {
		return sessionAccess{}, fmt.Errorf("read share visitor: %w", err)
	}
	publicShare, err := repository.loadPublicShare(
		ctx,
		tx,
		session.WorkspaceID,
		session.ShareID,
	)
	if err != nil {
		return sessionAccess{}, err
	}
	if err := tx.Commit(); err != nil {
		return sessionAccess{}, fmt.Errorf("commit share visitor identity: %w", err)
	}
	return sessionAccess{
		PublicShare: withAccessContext(withVisitor(publicShare, publicVisitorFromNulls(
			sql.NullString{String: visitorID, Valid: visitorID != ""},
			displayName,
			method,
			verifiedAt,
		)), session.ShareLinkID, session.SessionID),
		PasswordHash: session.PasswordHash,
		Verified:     session.PasswordVerified,
		SessionID:    session.SessionID,
		ShareLinkID:  session.ShareLinkID,
	}, nil
}

func withVisitor(item PublicShare, visitor PublicVisitor) PublicShare {
	item.Visitor = visitor
	return item
}

func withAccessContext(item PublicShare, linkID, sessionID string) PublicShare {
	item.ShareLinkID = linkID
	item.VisitorSessionID = sessionID
	return item
}

func publicVisitorFromNulls(
	id sql.NullString,
	displayName sql.NullString,
	method sql.NullString,
	verifiedAt sql.NullString,
) PublicVisitor {
	result := PublicVisitor{
		IdentityMethod: "anonymous",
	}
	if id.Valid {
		result.ID = id.String
	}
	if method.Valid && method.String != "" {
		result.IdentityMethod = method.String
	}
	if displayName.Valid && displayName.String != "" {
		result.DisplayName = &displayName.String
	}
	result.Verified = result.IdentityMethod == "verification_code" &&
		verifiedAt.Valid
	result.Identified = result.DisplayName != nil ||
		result.IdentityMethod == "verification_code"
	return result
}

func requireShareChanged(
	ctx context.Context,
	tx *sql.Tx,
	result sql.Result,
	workspaceID string,
	id string,
	revision int,
	now time.Time,
) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read share update result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var status string
	var currentRevision int
	var expiresAt sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT status, revision, expires_at
		FROM shares
		WHERE id = ? AND workspace_id = ?
	`, id, workspaceID).Scan(&status, &currentRevision, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read share after update conflict: %w", err)
	}
	if currentRevision != revision {
		return ErrRevisionConflict
	}
	if status != "active" {
		return ErrInvalidState
	}
	if expiresAt.Valid {
		value, err := parseTime(expiresAt.String)
		if err != nil {
			return err
		}
		if !value.After(now) {
			return ErrInvalidState
		}
	}
	return ErrInvalidState
}

type rowScanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type rowsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type fullQueryer interface {
	rowQueryer
	rowsQueryer
}

func scanShare(scanner rowScanner) (Share, error) {
	var item Share
	var reviewSessionID, reviewSessionName, passwordSecretRef sql.NullString
	var expiresAt, revokedAt sql.NullString
	var maxVisits sql.NullInt64
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&reviewSessionID,
		&reviewSessionName,
		&item.Name,
		&item.Status,
		&item.AllowComment,
		&item.AllowDownload,
		&item.RequireNickname,
		&expiresAt,
		&maxVisits,
		&item.PasswordProtected,
		&passwordSecretRef,
		&item.CreatedBy,
		&item.Revision,
		&createdAt,
		&updatedAt,
		&revokedAt,
	)
	if err != nil {
		return Share{}, err
	}
	if reviewSessionID.Valid {
		item.ReviewSessionID = &reviewSessionID.String
	}
	if reviewSessionName.Valid {
		item.ReviewSessionName = &reviewSessionName.String
	}
	if maxVisits.Valid {
		value := int(maxVisits.Int64)
		item.MaxVisits = &value
	}
	if passwordSecretRef.Valid {
		item.PasswordSecretRef = &passwordSecretRef.String
	}
	var parseErr error
	if item.CreatedAt, parseErr = parseTime(createdAt); parseErr != nil {
		return Share{}, parseErr
	}
	if item.UpdatedAt, parseErr = parseTime(updatedAt); parseErr != nil {
		return Share{}, parseErr
	}
	if expiresAt.Valid {
		value, err := parseTime(expiresAt.String)
		if err != nil {
			return Share{}, err
		}
		item.ExpiresAt = &value
	}
	if revokedAt.Valid {
		value, err := parseTime(revokedAt.String)
		if err != nil {
			return Share{}, err
		}
		item.RevokedAt = &value
	}
	return item, nil
}

func scanLink(scanner rowScanner) (Link, error) {
	var item Link
	var createdAt string
	var tokenSecretRef, lastUsedAt, revokedAt sql.NullString
	err := scanner.Scan(
		&item.ID,
		&item.ShareID,
		&item.TokenPrefix,
		&tokenSecretRef,
		&item.Status,
		&createdAt,
		&lastUsedAt,
		&revokedAt,
	)
	if err != nil {
		return Link{}, err
	}
	if tokenSecretRef.Valid {
		item.TokenSecretRef = &tokenSecretRef.String
	}
	var parseErr error
	if item.CreatedAt, parseErr = parseTime(createdAt); parseErr != nil {
		return Link{}, parseErr
	}
	if lastUsedAt.Valid {
		value, err := parseTime(lastUsedAt.String)
		if err != nil {
			return Link{}, err
		}
		item.LastUsedAt = &value
	}
	if revokedAt.Valid {
		value, err := parseTime(revokedAt.String)
		if err != nil {
			return Link{}, err
		}
		item.RevokedAt = &value
	}
	return item, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableStringPtr(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse share time: %w", err)
	}
	return parsed, nil
}
