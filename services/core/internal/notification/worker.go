package notification

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type WorkerConfig struct {
	PollInterval  time.Duration
	LeaseDuration time.Duration
	Logger        *slog.Logger
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
		config.PollInterval = time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Worker{service: service, config: config, done: make(chan struct{})}
}

func (worker *Worker) Start(parent context.Context) {
	worker.once.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		worker.stop = cancel
		go worker.run(ctx)
	})
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
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		message, claimed, err := worker.service.ClaimDelivery(
			ctx,
			worker.config.LeaseDuration,
		)
		if err != nil {
			worker.config.Logger.Error("notification claim failed", "error", err)
			timer.Reset(worker.config.PollInterval)
			continue
		}
		if !claimed {
			timer.Reset(worker.config.PollInterval)
			continue
		}
		worker.dispatch(ctx, message)
		timer.Reset(0)
	}
}

func (worker *Worker) dispatch(ctx context.Context, message OutboxMessage) {
	if err := worker.service.DispatchMessage(ctx, message); err != nil {
		deliveryErr := deliveryErrorFrom(err)
		if failErr := worker.service.FailDelivery(
			context.WithoutCancel(ctx),
			message.ID,
			deliveryErr,
		); failErr != nil {
			worker.config.Logger.Error(
				"notification failure recording failed",
				"outbox_id", message.ID,
				"error", failErr,
			)
		}
		return
	}
	if err := worker.service.SucceedDelivery(
		context.WithoutCancel(ctx),
		message.ID,
	); err != nil {
		worker.config.Logger.Error(
			"notification completion failed",
			"outbox_id", message.ID,
			"error", err,
		)
	}
}
