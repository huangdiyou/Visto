package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"review-studio.local/core/internal/identity"
	"review-studio.local/core/internal/platform/database"
)

func TestJobLifecycleIdempotencyProgressAndCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, workspaceID := jobTestDatabase(t, ctx)
	now := time.Date(2026, time.June, 10, 1, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }
	if err := service.EnsureNode(ctx, testNode("node-1")); err != nil {
		t.Fatalf("ensure node: %v", err)
	}

	key := "probe:root-1"
	input := CreateInput{
		WorkspaceID:    workspaceID,
		Type:           "media.probe_root",
		IdempotencyKey: &key,
		SubjectType:    "authorizedRoot",
		SubjectID:      "root-1",
		Payload:        json.RawMessage(`{"rootId":"root-1"}`),
	}
	created, wasCreated, err := service.Create(ctx, input)
	if err != nil || !wasCreated {
		t.Fatalf("create job: created=%v err=%v", wasCreated, err)
	}
	duplicate, duplicateCreated, err := service.Create(ctx, input)
	if err != nil || duplicateCreated || duplicate.ID != created.ID {
		t.Fatalf(
			"idempotent create failed: %#v created=%v err=%v",
			duplicate,
			duplicateCreated,
			err,
		)
	}

	lease, claimed, err := service.Claim(ctx, "node-1", 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim job: claimed=%v err=%v", claimed, err)
	}
	if err := service.Start(ctx, lease); err != nil {
		t.Fatalf("start job: %v", err)
	}
	if err := service.SetProgress(ctx, lease, Progress{
		Current: 2,
		Total:   5,
		Unit:    "items",
	}); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	running, err := service.Get(ctx, workspaceID, created.ID)
	if err != nil {
		t.Fatalf("get running job: %v", err)
	}
	if running.Status != StatusRunning ||
		running.Progress == nil ||
		running.Progress.Current != 2 {
		t.Fatalf("unexpected running job: %#v", running)
	}

	cancelled, err := service.Cancel(ctx, workspaceID, created.ID)
	if err != nil || cancelled.Status != StatusCancelRequested {
		t.Fatalf("request cancel: %#v err=%v", cancelled, err)
	}
	requested, err := service.Heartbeat(ctx, lease)
	if err != nil || !requested {
		t.Fatalf("heartbeat did not observe cancellation: %v %v", requested, err)
	}
	if err := service.Fail(ctx, lease, Error{
		Code:    "job.cancelled",
		Message: "execution cancelled",
	}); err != nil {
		t.Fatalf("finish cancellation: %v", err)
	}
	final, err := service.Get(ctx, workspaceID, created.ID)
	if err != nil || final.Status != StatusCancelled || final.CompletedAt == nil {
		t.Fatalf("unexpected cancelled job: %#v err=%v", final, err)
	}
	if err := service.Succeed(ctx, lease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old lease completed cancelled job: %v", err)
	}
	repeatedCancel, err := service.Cancel(ctx, workspaceID, created.ID)
	if err != nil || repeatedCancel.Status != StatusCancelled {
		t.Fatalf("repeated cancellation was not idempotent: %#v %v", repeatedCancel, err)
	}
}

func TestExpiredLeaseRecoversAfterRestartAndRejectsOldWorker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, workspaceID := jobTestDatabase(t, ctx)
	now := time.Date(2026, time.June, 10, 2, 0, 0, 0, time.UTC)
	first := NewService(NewSQLiteRepository(db))
	first.clock = func() time.Time { return now }
	if err := first.EnsureNode(ctx, testNode("node-1")); err != nil {
		t.Fatalf("ensure first node: %v", err)
	}
	if err := first.EnsureNode(ctx, testNode("node-2")); err != nil {
		t.Fatalf("ensure second node: %v", err)
	}
	item, _, err := first.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.restart",
		SubjectType: "fixture",
		SubjectID:   "subject-1",
	})
	if err != nil {
		t.Fatalf("create restart job: %v", err)
	}
	oldLease, claimed, err := first.Claim(ctx, "node-1", 5*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim old lease: %v claimed=%v", err, claimed)
	}
	if err := first.Start(ctx, oldLease); err != nil {
		t.Fatalf("start old lease: %v", err)
	}

	now = now.Add(6 * time.Second)
	restarted := NewService(NewSQLiteRepository(db))
	restarted.clock = func() time.Time { return now }
	newLease, claimed, err := restarted.Claim(ctx, "node-2", 5*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim recovered job: %v claimed=%v", err, claimed)
	}
	if newLease.Job.ID != item.ID || newLease.Attempt.AttemptNumber != 2 {
		t.Fatalf("unexpected recovered lease: %#v", newLease)
	}
	if err := first.Succeed(ctx, oldLease); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired worker changed job: %v", err)
	}
	if err := restarted.Start(ctx, newLease); err != nil {
		t.Fatalf("start recovered lease: %v", err)
	}
	if err := restarted.Succeed(ctx, newLease); err != nil {
		t.Fatalf("complete recovered lease: %v", err)
	}

	var abandoned int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM job_attempts
		WHERE job_id = ? AND status = 'abandoned'
	`, item.ID).Scan(&abandoned); err != nil {
		t.Fatalf("count abandoned attempts: %v", err)
	}
	if abandoned != 1 {
		t.Fatalf("expected one abandoned attempt, got %d", abandoned)
	}
}

func TestFailedJobAutomaticallyRetriesThenCanBeManuallyRetried(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, workspaceID := jobTestDatabase(t, ctx)
	now := time.Date(2026, time.June, 10, 3, 0, 0, 0, time.UTC)
	service := NewService(NewSQLiteRepository(db))
	service.clock = func() time.Time { return now }
	if err := service.EnsureNode(ctx, testNode("node-1")); err != nil {
		t.Fatalf("ensure node: %v", err)
	}
	item, _, err := service.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.retry",
		SubjectType: "fixture",
		SubjectID:   "subject-1",
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("create retry job: %v", err)
	}

	firstLease := claimAndStart(t, ctx, service, now, "node-1")
	if err := service.Fail(ctx, firstLease, Error{
		Code: "fixture.failed", Message: "first attempt",
	}); err != nil {
		t.Fatalf("fail first attempt: %v", err)
	}
	queued, err := service.Get(ctx, workspaceID, item.ID)
	if err != nil || queued.Status != StatusQueued {
		t.Fatalf("job was not automatically requeued: %#v err=%v", queued, err)
	}

	now = now.Add(2 * time.Second)
	secondLease := claimAndStart(t, ctx, service, now, "node-1")
	if err := service.Fail(ctx, secondLease, Error{
		Code: "fixture.failed", Message: "second attempt",
	}); err != nil {
		t.Fatalf("fail second attempt: %v", err)
	}
	failed, err := service.Get(ctx, workspaceID, item.ID)
	if err != nil || failed.Status != StatusFailed {
		t.Fatalf("job did not reach failed: %#v err=%v", failed, err)
	}

	retried, err := service.Retry(ctx, workspaceID, item.ID)
	if err != nil || retried.Status != StatusQueued ||
		retried.MaxAttempts != 3 {
		t.Fatalf("manual retry failed: %#v err=%v", retried, err)
	}
	if _, err := service.Retry(ctx, workspaceID, item.ID); !errors.Is(
		err,
		ErrNotRetryable,
	) {
		t.Fatalf("queued job was retryable: %v", err)
	}
}

func claimAndStart(
	t *testing.T,
	ctx context.Context,
	service *Service,
	now time.Time,
	nodeID string,
) Lease {
	t.Helper()
	service.clock = func() time.Time { return now }
	lease, claimed, err := service.Claim(ctx, nodeID, 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim job: %v claimed=%v", err, claimed)
	}
	if err := service.Start(ctx, lease); err != nil {
		t.Fatalf("start job: %v", err)
	}
	return lease
}

func testNode(id string) Node {
	return Node{
		ID:              id,
		Name:            id,
		Kind:            "embedded",
		Status:          "online",
		Capabilities:    json.RawMessage(`{}`),
		SoftwareVersion: "test",
	}
}

func jobTestDatabase(
	t *testing.T,
	ctx context.Context,
) (*sql.DB, string) {
	t.Helper()
	db, err := database.Open(ctx, database.Config{
		Path: filepath.Join(t.TempDir(), "review-studio.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close database: %v", err)
		}
	})
	result, err := identity.NewService(identity.NewSQLiteRepository(db)).Setup(
		ctx,
		identity.SetupInput{
			WorkspaceName: "Studio",
			OwnerName:     "Owner",
			Password:      "local-password-123",
			Locale:        "zh-CN",
			Timezone:      "Asia/Shanghai",
		},
	)
	if err != nil {
		t.Fatalf("setup identity: %v", err)
	}
	return db, result.Session.Workspace.ID
}
