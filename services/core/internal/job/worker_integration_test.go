package job

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerReportsProgressRetriesAndHonorsCancellation(t *testing.T) {
	ctx := context.Background()
	db, workspaceID := jobTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	retryExecutor := &failOnceExecutor{}
	worker := NewWorker(service, WorkerConfig{
		NodeID:          "test-worker",
		NodeName:        "Test Worker",
		SoftwareVersion: "test",
		PollInterval:    10 * time.Millisecond,
		LeaseDuration:   300 * time.Millisecond,
		Executors: map[string]Executor{
			"test.fail_once": retryExecutor,
			"test.block":     blockingExecutor{},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	workerContext, cancelWorker := context.WithCancel(ctx)
	if err := worker.Start(workerContext); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		cancelWorker()
		stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(stopContext); err != nil {
			t.Errorf("stop worker: %v", err)
		}
	})

	retryJob, _, err := service.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.fail_once",
		SubjectType: "fixture",
		SubjectID:   "retry",
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("create retry worker job: %v", err)
	}
	succeeded := waitForJobStatus(
		t,
		service,
		workspaceID,
		retryJob.ID,
		StatusSucceeded,
	)
	if succeeded.AttemptCount != 2 ||
		succeeded.Progress == nil ||
		succeeded.Progress.Current != 3 ||
		succeeded.Progress.Total != 3 ||
		retryExecutor.calls.Load() != 2 {
		t.Fatalf("unexpected retried worker job: %#v", succeeded)
	}

	cancelJob, _, err := service.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.block",
		SubjectType: "fixture",
		SubjectID:   "cancel",
	})
	if err != nil {
		t.Fatalf("create cancellable job: %v", err)
	}
	waitForJobStatus(t, service, workspaceID, cancelJob.ID, StatusRunning)
	requested, err := service.Cancel(ctx, workspaceID, cancelJob.ID)
	if err != nil || requested.Status != StatusCancelRequested {
		t.Fatalf("request worker cancellation: %#v err=%v", requested, err)
	}
	cancelled := waitForJobStatus(
		t,
		service,
		workspaceID,
		cancelJob.ID,
		StatusCancelled,
	)
	if cancelled.CompletedAt == nil {
		t.Fatalf("cancelled worker job has no completion time: %#v", cancelled)
	}
}

func TestWorkerHonorsMaxConcurrentJobs(t *testing.T) {
	ctx := context.Background()
	db, workspaceID := jobTestDatabase(t, ctx)
	service := NewService(NewSQLiteRepository(db))
	worker := NewWorker(service, WorkerConfig{
		NodeID:            "limited-worker",
		NodeName:          "Limited Worker",
		SoftwareVersion:   "test",
		PollInterval:      10 * time.Millisecond,
		LeaseDuration:     300 * time.Millisecond,
		MaxConcurrentJobs: 1,
		Executors: map[string]Executor{
			"test.block": blockingExecutor{},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	workerContext, cancelWorker := context.WithCancel(ctx)
	if err := worker.Start(workerContext); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		cancelWorker()
		stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := worker.Stop(stopContext); err != nil {
			t.Errorf("stop worker: %v", err)
		}
	})

	first, _, err := service.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.block",
		SubjectType: "fixture",
		SubjectID:   "first",
	})
	if err != nil {
		t.Fatalf("create first job: %v", err)
	}
	second, _, err := service.Create(ctx, CreateInput{
		WorkspaceID: workspaceID,
		Type:        "test.block",
		SubjectType: "fixture",
		SubjectID:   "second",
	})
	if err != nil {
		t.Fatalf("create second job: %v", err)
	}

	waitForJobStatus(t, service, workspaceID, first.ID, StatusRunning)
	time.Sleep(100 * time.Millisecond)
	queued, err := service.Get(ctx, workspaceID, second.ID)
	if err != nil {
		t.Fatalf("read second job: %v", err)
	}
	if queued.Status != StatusQueued {
		t.Fatalf("second job status = %q, want queued", queued.Status)
	}
}

type failOnceExecutor struct {
	calls atomic.Int32
}

func (executor *failOnceExecutor) Execute(
	ctx context.Context,
	_ Job,
	reporter Reporter,
) error {
	call := executor.calls.Add(1)
	if err := reporter.SetProgress(ctx, 1, 3, "items"); err != nil {
		return err
	}
	if call == 1 {
		return errors.New("intentional first failure")
	}
	return reporter.SetProgress(ctx, 3, 3, "items")
}

type blockingExecutor struct{}

func (blockingExecutor) Execute(
	ctx context.Context,
	_ Job,
	_ Reporter,
) error {
	<-ctx.Done()
	return ctx.Err()
}

func waitForJobStatus(
	t *testing.T,
	service *Service,
	workspaceID string,
	jobID string,
	status string,
) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, err := service.Get(context.Background(), workspaceID, jobID)
		if err != nil {
			t.Fatalf("get job while waiting for %s: %v", status, err)
		}
		if item.Status == status {
			return item
		}
		time.Sleep(10 * time.Millisecond)
	}
	item, _ := service.Get(context.Background(), workspaceID, jobID)
	t.Fatalf("job did not reach %s: %#v", status, item)
	return Job{}
}
