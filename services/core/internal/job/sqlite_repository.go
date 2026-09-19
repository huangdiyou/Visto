package job

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

func (repository *SQLiteRepository) Create(
	ctx context.Context,
	record createRecord,
) (Job, bool, error) {
	if record.IdempotencyKey != nil {
		existing, err := repository.activeIdempotent(
			ctx,
			record.WorkspaceID,
			record.Type,
			*record.IdempotencyKey,
		)
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Job{}, false, err
		}
	}

	now := formatTime(record.Now)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, workspace_id, type, status, priority, idempotency_key,
			subject_type, subject_id, payload_json, available_at,
			max_attempts, attempt_count, created_at, updated_at
		) VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
	`,
		record.ID,
		record.WorkspaceID,
		record.Type,
		record.Priority,
		record.IdempotencyKey,
		record.SubjectType,
		record.SubjectID,
		record.Payload,
		formatTime(record.AvailableAt),
		record.MaxAttempts,
		now,
		now,
	)
	if err != nil {
		if record.IdempotencyKey != nil {
			existing, lookupErr := repository.activeIdempotent(
				ctx,
				record.WorkspaceID,
				record.Type,
				*record.IdempotencyKey,
			)
			if lookupErr == nil {
				return existing, false, nil
			}
		}
		return Job{}, false, fmt.Errorf("create job: %w", err)
	}
	created, err := repository.Get(ctx, record.WorkspaceID, record.ID)
	return created, true, err
}

func (repository *SQLiteRepository) Get(
	ctx context.Context,
	workspaceID string,
	jobID string,
) (Job, error) {
	item, err := scanJob(repository.db.QueryRowContext(ctx, jobSelect+`
		WHERE id = ? AND workspace_id = ?
	`, jobID, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return item, err
}

func (repository *SQLiteRepository) List(
	ctx context.Context,
	input ListInput,
) ([]Job, error) {
	query := jobSelect + ` WHERE workspace_id = ?`
	arguments := []any{input.WorkspaceID}
	if input.Status != "" {
		query += ` AND status = ?`
		arguments = append(arguments, input.Status)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC LIMIT ?`
	arguments = append(arguments, input.Limit)

	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	items := make([]Job, 0)
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}
	return items, nil
}

func (repository *SQLiteRepository) EnsureNode(
	ctx context.Context,
	node Node,
) error {
	now := formatTime(node.UpdatedAt)
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO media_nodes (
			id, workspace_id, name, kind, status, capabilities_json,
			software_version, last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			name = excluded.name,
			kind = excluded.kind,
			status = excluded.status,
			capabilities_json = excluded.capabilities_json,
			software_version = excluded.software_version,
			last_seen_at = excluded.last_seen_at,
			updated_at = excluded.updated_at
	`,
		node.ID,
		node.WorkspaceID,
		node.Name,
		node.Kind,
		node.Status,
		string(node.Capabilities),
		node.SoftwareVersion,
		nullableTime(node.LastSeenAt),
		formatTime(node.CreatedAt),
		now,
	)
	if err != nil {
		return fmt.Errorf("ensure media node: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) Claim(
	ctx context.Context,
	nodeID string,
	leaseDigest string,
	now time.Time,
	leaseDuration time.Duration,
) (Job, Attempt, bool, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, Attempt{}, false, fmt.Errorf("begin job claim: %w", err)
	}
	defer tx.Rollback()

	if err := reclaimExpired(ctx, tx, now); err != nil {
		return Job{}, Attempt{}, false, err
	}

	item, err := scanJob(tx.QueryRowContext(ctx, jobSelect+`
		WHERE status = 'queued' AND available_at <= ?
		ORDER BY priority DESC, available_at, created_at
		LIMIT 1
	`, formatTime(now)))
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return Job{}, Attempt{}, false, fmt.Errorf("commit empty job claim: %w", err)
		}
		return Job{}, Attempt{}, false, nil
	}
	if err != nil {
		return Job{}, Attempt{}, false, err
	}

	attemptID, err := newID()
	if err != nil {
		return Job{}, Attempt{}, false, err
	}
	attemptNumber := item.AttemptCount + 1
	timestamp := formatTime(now)
	leaseExpiresAt := now.Add(leaseDuration)
	result, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'leased',
			attempt_count = ?,
			started_at = COALESCE(started_at, ?),
			updated_at = ?
		WHERE id = ? AND status = 'queued'
	`, attemptNumber, timestamp, timestamp, item.ID)
	if err != nil {
		return Job{}, Attempt{}, false, fmt.Errorf("lease job: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Job{}, Attempt{}, false, fmt.Errorf("read leased job result: %w", err)
	}
	if changed != 1 {
		return Job{}, Attempt{}, false, ErrInvalidTransition
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO job_attempts (
			id, job_id, media_node_id, attempt_number, lease_token_digest,
			lease_expires_at, started_at, heartbeat_at, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'running')
	`,
		attemptID,
		item.ID,
		nodeID,
		attemptNumber,
		leaseDigest,
		formatTime(leaseExpiresAt),
		timestamp,
		timestamp,
	); err != nil {
		return Job{}, Attempt{}, false, fmt.Errorf("create job attempt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, Attempt{}, false, fmt.Errorf("commit job claim: %w", err)
	}

	item.Status = StatusLeased
	item.AttemptCount = attemptNumber
	item.UpdatedAt = now
	if item.StartedAt == nil {
		item.StartedAt = pointer(now)
	}
	return item, Attempt{
		ID:             attemptID,
		JobID:          item.ID,
		MediaNodeID:    pointer(nodeID),
		AttemptNumber:  attemptNumber,
		LeaseExpiresAt: leaseExpiresAt,
		StartedAt:      now,
		HeartbeatAt:    now,
		Status:         StatusRunning,
	}, true, nil
}

func (repository *SQLiteRepository) Start(
	ctx context.Context,
	jobID string,
	attemptID string,
	leaseDigest string,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'running', updated_at = ?
		WHERE id = ? AND status = 'leased'
			AND EXISTS (
				SELECT 1 FROM job_attempts
				WHERE id = ? AND job_id = jobs.id
					AND lease_token_digest = ?
					AND status = 'running'
					AND lease_expires_at > ?
			)
	`,
		formatTime(now),
		jobID,
		attemptID,
		leaseDigest,
		formatTime(now),
	)
	return requireTransition(result, err, "start job")
}

func (repository *SQLiteRepository) Heartbeat(
	ctx context.Context,
	jobID string,
	attemptID string,
	leaseDigest string,
	now time.Time,
	leaseDuration time.Duration,
) (bool, error) {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin job heartbeat: %w", err)
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT jobs.status
		FROM jobs
		JOIN job_attempts ON job_attempts.job_id = jobs.id
		WHERE jobs.id = ?
			AND job_attempts.id = ?
			AND job_attempts.lease_token_digest = ?
			AND job_attempts.status = 'running'
			AND job_attempts.lease_expires_at > ?
			AND jobs.status IN ('running', 'cancel_requested')
	`, jobID, attemptID, leaseDigest, formatTime(now)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("read job heartbeat state: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE job_attempts
		SET heartbeat_at = ?, lease_expires_at = ?
		WHERE id = ?
	`, formatTime(now), formatTime(now.Add(leaseDuration)), attemptID); err != nil {
		return false, fmt.Errorf("extend job lease: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE media_nodes
		SET status = 'online', last_seen_at = ?, updated_at = ?
		WHERE id = (SELECT media_node_id FROM job_attempts WHERE id = ?)
	`, formatTime(now), formatTime(now), attemptID); err != nil {
		return false, fmt.Errorf("update media node heartbeat: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit job heartbeat: %w", err)
	}
	return status == StatusCancelRequested, nil
}

func (repository *SQLiteRepository) SetProgress(
	ctx context.Context,
	jobID string,
	attemptID string,
	leaseDigest string,
	progress Progress,
	now time.Time,
) error {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE jobs
		SET progress_current = ?, progress_total = ?, progress_unit = ?,
			updated_at = ?
		WHERE id = ? AND status = 'running'
			AND EXISTS (
				SELECT 1 FROM job_attempts
				WHERE id = ? AND job_id = jobs.id
					AND lease_token_digest = ?
					AND status = 'running'
					AND lease_expires_at > ?
			)
	`,
		progress.Current,
		progress.Total,
		progress.Unit,
		formatTime(now),
		jobID,
		attemptID,
		leaseDigest,
		formatTime(now),
	)
	return requireLease(result, err, "update job progress")
}

func (repository *SQLiteRepository) Succeed(
	ctx context.Context,
	jobID string,
	attemptID string,
	leaseDigest string,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin job success: %w", err)
	}
	defer tx.Rollback()
	if err := requireAttempt(
		ctx, tx, jobID, attemptID, leaseDigest, now, StatusRunning,
	); err != nil {
		return err
	}
	timestamp := formatTime(now)
	if _, err := tx.ExecContext(ctx, `
		UPDATE job_attempts
		SET status = 'succeeded', finished_at = ?
		WHERE id = ?
	`, timestamp, attemptID); err != nil {
		return fmt.Errorf("complete job attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'succeeded',
			progress_current = CASE
				WHEN progress_total IS NOT NULL THEN progress_total
				ELSE progress_current
			END,
			completed_at = ?, updated_at = ?
		WHERE id = ? AND status = 'running'
	`, timestamp, timestamp, jobID); err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return commit(tx, "job success")
}

func (repository *SQLiteRepository) Fail(
	ctx context.Context,
	jobID string,
	attemptID string,
	leaseDigest string,
	jobError Error,
	retryAt time.Time,
	now time.Time,
) error {
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin job failure: %w", err)
	}
	defer tx.Rollback()

	var status string
	var attemptCount int
	var maxAttempts int
	err = tx.QueryRowContext(ctx, `
		SELECT jobs.status, jobs.attempt_count, jobs.max_attempts
		FROM jobs
		JOIN job_attempts ON job_attempts.job_id = jobs.id
		WHERE jobs.id = ?
			AND job_attempts.id = ?
			AND job_attempts.lease_token_digest = ?
			AND job_attempts.status = 'running'
			AND job_attempts.lease_expires_at > ?
			AND jobs.status IN ('running', 'cancel_requested')
	`, jobID, attemptID, leaseDigest, formatTime(now)).
		Scan(&status, &attemptCount, &maxAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("read failed job state: %w", err)
	}

	timestamp := formatTime(now)
	attemptStatus := "failed"
	nextStatus := StatusFailed
	completedAt := any(timestamp)
	availableAt := formatTime(retryAt)
	if status == StatusCancelRequested {
		attemptStatus = "cancelled"
		nextStatus = StatusCancelled
	} else if attemptCount < maxAttempts {
		nextStatus = StatusQueued
		completedAt = nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE job_attempts
		SET status = ?, error_code = ?, error_message = ?, finished_at = ?
		WHERE id = ?
	`, attemptStatus, jobError.Code, jobError.Message, timestamp, attemptID); err != nil {
		return fmt.Errorf("fail job attempt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, available_at = ?,
			last_error_code = ?, last_error_message = ?,
			completed_at = ?, updated_at = ?
		WHERE id = ?
	`,
		nextStatus,
		availableAt,
		jobError.Code,
		jobError.Message,
		completedAt,
		timestamp,
		jobID,
	); err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return commit(tx, "job failure")
}

func (repository *SQLiteRepository) Cancel(
	ctx context.Context,
	workspaceID string,
	jobID string,
	now time.Time,
) (Job, error) {
	timestamp := formatTime(now)
	result, err := repository.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = CASE
				WHEN status = 'queued' THEN 'cancelled'
				WHEN status = 'cancelled' THEN 'cancelled'
				ELSE 'cancel_requested'
			END,
			completed_at = CASE
				WHEN status = 'queued' THEN ?
				WHEN status = 'cancelled' THEN completed_at
				ELSE NULL
			END,
			updated_at = ?
		WHERE id = ? AND workspace_id = ?
			AND status IN (
				'queued', 'leased', 'running',
				'cancel_requested', 'cancelled'
			)
	`, timestamp, timestamp, jobID, workspaceID)
	if err != nil {
		return Job{}, fmt.Errorf("cancel job: %w", err)
	}
	if err := requireJobChange(ctx, repository.db, result, workspaceID, jobID); err != nil {
		return Job{}, err
	}
	return repository.Get(ctx, workspaceID, jobID)
}

func (repository *SQLiteRepository) Retry(
	ctx context.Context,
	workspaceID string,
	jobID string,
	now time.Time,
) (Job, error) {
	result, err := repository.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'queued',
			max_attempts = CASE
				WHEN max_attempts <= attempt_count THEN attempt_count + 1
				ELSE max_attempts
			END,
			available_at = ?,
			last_error_code = NULL,
			last_error_message = NULL,
			completed_at = NULL,
			updated_at = ?
		WHERE id = ? AND workspace_id = ? AND status = 'failed'
	`, formatTime(now), formatTime(now), jobID, workspaceID)
	if err != nil {
		return Job{}, fmt.Errorf("retry job: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Job{}, fmt.Errorf("read retry result: %w", err)
	}
	if changed == 0 {
		if _, getErr := repository.Get(ctx, workspaceID, jobID); getErr != nil {
			return Job{}, getErr
		}
		return Job{}, ErrNotRetryable
	}
	return repository.Get(ctx, workspaceID, jobID)
}

const jobSelect = `
	SELECT
		id, workspace_id, type, status, priority, idempotency_key,
		subject_type, subject_id, payload_json, progress_current,
		progress_total, progress_unit, available_at, max_attempts,
		attempt_count, last_error_code, last_error_message,
		created_at, updated_at, started_at, completed_at
	FROM jobs
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(scanner rowScanner) (Job, error) {
	var item Job
	var payload string
	var progressCurrent sql.NullInt64
	var progressTotal sql.NullInt64
	var progressUnit sql.NullString
	var errorCode sql.NullString
	var errorMessage sql.NullString
	var availableAt string
	var createdAt string
	var updatedAt string
	var startedAt sql.NullString
	var completedAt sql.NullString
	err := scanner.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.Type,
		&item.Status,
		&item.Priority,
		&item.IdempotencyKey,
		&item.SubjectType,
		&item.SubjectID,
		&payload,
		&progressCurrent,
		&progressTotal,
		&progressUnit,
		&availableAt,
		&item.MaxAttempts,
		&item.AttemptCount,
		&errorCode,
		&errorMessage,
		&createdAt,
		&updatedAt,
		&startedAt,
		&completedAt,
	)
	if err != nil {
		return Job{}, err
	}
	item.Payload = []byte(payload)
	if progressCurrent.Valid && progressTotal.Valid && progressUnit.Valid {
		item.Progress = &Progress{
			Current: progressCurrent.Int64,
			Total:   progressTotal.Int64,
			Unit:    progressUnit.String,
		}
	}
	if errorCode.Valid {
		item.LastError = &Error{Code: errorCode.String, Message: errorMessage.String}
	}
	item.AvailableAt, err = parseTime(availableAt)
	if err != nil {
		return Job{}, err
	}
	item.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Job{}, err
	}
	item.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Job{}, err
	}
	item.StartedAt, err = parseNullableTime(startedAt)
	if err != nil {
		return Job{}, err
	}
	item.CompletedAt, err = parseNullableTime(completedAt)
	if err != nil {
		return Job{}, err
	}
	return item, nil
}

func (repository *SQLiteRepository) activeIdempotent(
	ctx context.Context,
	workspaceID string,
	jobType string,
	key string,
) (Job, error) {
	item, err := scanJob(repository.db.QueryRowContext(ctx, jobSelect+`
		WHERE workspace_id = ? AND type = ? AND idempotency_key = ?
			AND status IN ('queued', 'leased', 'running', 'cancel_requested')
		LIMIT 1
	`, workspaceID, jobType, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return item, err
}

func reclaimExpired(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			job_attempts.id,
			jobs.id,
			jobs.status,
			jobs.attempt_count,
			jobs.max_attempts
		FROM job_attempts
		JOIN jobs ON jobs.id = job_attempts.job_id
		WHERE job_attempts.status = 'running'
			AND job_attempts.lease_expires_at <= ?
			AND jobs.status IN ('leased', 'running', 'cancel_requested')
	`, formatTime(now))
	if err != nil {
		return fmt.Errorf("find expired job leases: %w", err)
	}
	type expired struct {
		attemptID   string
		jobID       string
		status      string
		attempts    int
		maxAttempts int
	}
	var items []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(
			&item.attemptID,
			&item.jobID,
			&item.status,
			&item.attempts,
			&item.maxAttempts,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired job lease: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close expired job leases: %w", err)
	}
	for _, item := range items {
		timestamp := formatTime(now)
		if _, err := tx.ExecContext(ctx, `
			UPDATE job_attempts
			SET status = 'abandoned', finished_at = ?,
				error_code = 'job.lease_expired',
				error_message = 'worker lease expired'
			WHERE id = ? AND status = 'running'
		`, timestamp, item.attemptID); err != nil {
			return fmt.Errorf("abandon expired job attempt: %w", err)
		}
		nextStatus := StatusQueued
		completedAt := any(nil)
		if item.status == StatusCancelRequested {
			nextStatus = StatusCancelled
			completedAt = timestamp
		} else if item.attempts >= item.maxAttempts {
			nextStatus = StatusFailed
			completedAt = timestamp
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE jobs
			SET status = ?, available_at = ?,
				last_error_code = 'job.lease_expired',
				last_error_message = 'worker lease expired',
				completed_at = ?, updated_at = ?
			WHERE id = ?
		`, nextStatus, timestamp, completedAt, timestamp, item.jobID); err != nil {
			return fmt.Errorf("recover expired job: %w", err)
		}
	}
	return nil
}

func requireAttempt(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	attemptID string,
	leaseDigest string,
	now time.Time,
	jobStatus string,
) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM jobs
		JOIN job_attempts ON job_attempts.job_id = jobs.id
		WHERE jobs.id = ?
			AND jobs.status = ?
			AND job_attempts.id = ?
			AND job_attempts.lease_token_digest = ?
			AND job_attempts.status = 'running'
			AND job_attempts.lease_expires_at > ?
	`, jobID, jobStatus, attemptID, leaseDigest, formatTime(now)).Scan(&count); err != nil {
		return fmt.Errorf("validate job attempt: %w", err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func requireTransition(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s result: %w", operation, err)
	}
	if changed != 1 {
		return ErrInvalidTransition
	}
	return nil
}

func requireLease(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s result: %w", operation, err)
	}
	if changed != 1 {
		return ErrLeaseLost
	}
	return nil
}

func requireJobChange(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	result sql.Result,
	workspaceID string,
	jobID string,
) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read job change result: %w", err)
	}
	if changed == 1 {
		return nil
	}
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs WHERE id = ? AND workspace_id = ?
	`, jobID, workspaceID).Scan(&count); err != nil {
		return fmt.Errorf("check changed job: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return ErrInvalidTransition
}

func commit(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse job time: %w", err)
	}
	return parsed, nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func pointer[T any](value T) *T {
	return &value
}
