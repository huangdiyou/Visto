package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Executor interface {
	Execute(ctx context.Context, item Job, reporter Reporter) error
}

type Reporter interface {
	SetProgress(ctx context.Context, current, total int64, unit string) error
}

type WorkerConfig struct {
	NodeID            string
	NodeName          string
	NodeKind          string
	SoftwareVersion   string
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	MaxConcurrentJobs int
	Executors         map[string]Executor
	Logger            *slog.Logger
}

type Worker struct {
	service *Service
	config  WorkerConfig
	stop    context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func NewWorker(service *Service, config WorkerConfig) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.MaxConcurrentJobs <= 0 {
		config.MaxConcurrentJobs = 1
	}
	if config.NodeKind == "" {
		config.NodeKind = "embedded"
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Worker{service: service, config: config, done: make(chan struct{})}
}

func (worker *Worker) Start(parent context.Context) error {
	var startErr error
	worker.once.Do(func() {
		if worker.config.NodeID == "" || worker.config.NodeName == "" {
			startErr = fmt.Errorf("%w: worker node identity is required", ErrInvalidInput)
			close(worker.done)
			return
		}
		ctx, cancel := context.WithCancel(parent)
		worker.stop = cancel
		if err := worker.service.EnsureNode(ctx, Node{
			ID:              worker.config.NodeID,
			Name:            worker.config.NodeName,
			Kind:            worker.config.NodeKind,
			Status:          "online",
			SoftwareVersion: worker.config.SoftwareVersion,
		}); err != nil {
			startErr = err
			close(worker.done)
			return
		}
		go worker.run(ctx)
	})
	return startErr
}

func (worker *Worker) Stop(ctx context.Context) error {
	if worker.stop != nil {
		worker.stop()
	}
	select {
	case <-worker.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (worker *Worker) run(ctx context.Context) {
	defer close(worker.done)
	slots := make(chan struct{}, worker.config.MaxConcurrentJobs)
	var active sync.WaitGroup
	defer active.Wait()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		lease, claimed, err := worker.service.Claim(
			ctx,
			worker.config.NodeID,
			worker.config.LeaseDuration,
		)
		if err != nil {
			<-slots
			worker.config.Logger.Error("job claim failed", "error", err)
			timer.Reset(worker.config.PollInterval)
			continue
		}
		if !claimed {
			<-slots
			timer.Reset(worker.config.PollInterval)
			continue
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-slots }()
			worker.execute(ctx, lease)
		}()
		timer.Reset(0)
	}
}

func (worker *Worker) execute(workerContext context.Context, lease Lease) {
	if err := worker.service.Start(workerContext, lease); err != nil {
		worker.config.Logger.Warn(
			"job start failed",
			"job_id", lease.Job.ID,
			"error", err,
		)
		return
	}

	executor, ok := worker.config.Executors[lease.Job.Type]
	if !ok {
		_ = worker.service.Fail(context.WithoutCancel(workerContext), lease, Error{
			Code:    "job.type_unsupported",
			Message: "no executor is registered for this job type",
		})
		return
	}

	executionContext, cancel := context.WithCancel(workerContext)
	defer cancel()
	result := make(chan error, 1)
	reporter := leaseReporter{service: worker.service, lease: lease}
	go func() {
		result <- executor.Execute(executionContext, lease.Job, reporter)
	}()

	heartbeatInterval := lease.Duration / 3
	if heartbeatInterval < 100*time.Millisecond {
		heartbeatInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-workerContext.Done():
			cancel()
			return
		case err := <-result:
			if err == nil {
				if completeErr := worker.service.Succeed(
					context.WithoutCancel(workerContext),
					lease,
				); completeErr != nil && !errors.Is(completeErr, ErrLeaseLost) {
					worker.config.Logger.Error(
						"job completion failed",
						"job_id", lease.Job.ID,
						"error", completeErr,
					)
				}
				return
			}
			jobError := executionError(err)
			if failErr := worker.service.Fail(
				context.WithoutCancel(workerContext),
				lease,
				jobError,
			); failErr != nil && !errors.Is(failErr, ErrLeaseLost) {
				worker.config.Logger.Error(
					"job failure recording failed",
					"job_id", lease.Job.ID,
					"error", failErr,
				)
			}
			return
		case <-ticker.C:
			cancelRequested, err := worker.service.Heartbeat(workerContext, lease)
			if err != nil {
				cancel()
				if !errors.Is(err, ErrLeaseLost) &&
					!errors.Is(err, context.Canceled) {
					worker.config.Logger.Warn(
						"job heartbeat failed",
						"job_id", lease.Job.ID,
						"error", err,
					)
				}
				return
			}
			if cancelRequested {
				cancel()
			}
		}
	}
}

type leaseReporter struct {
	service *Service
	lease   Lease
}

func (reporter leaseReporter) SetProgress(
	ctx context.Context,
	current int64,
	total int64,
	unit string,
) error {
	return reporter.service.SetProgress(ctx, reporter.lease, Progress{
		Current: current,
		Total:   total,
		Unit:    unit,
	})
}

type CodedError interface {
	error
	Code() string
}

func executionError(err error) Error {
	code := "job.execution_failed"
	var coded CodedError
	if errors.As(err, &coded) {
		code = coded.Code()
	}
	if errors.Is(err, context.Canceled) {
		code = "job.cancelled"
	}
	return Error{Code: code, Message: sanitizeError(err.Error())}
}
