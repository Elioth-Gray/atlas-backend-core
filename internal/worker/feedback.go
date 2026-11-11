// Package worker runs background jobs. FeedbackWorker drains the feedback_jobs queue (Path
// A): each job sends one feedback to the AI service and stores the results. Failed jobs
// are retried with exponential backoff; the feedback itself is never lost because it was
// committed before the job ran.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/logger"
	"atlas-backend-core/internal/requestid"
)

// maxErrorLength bounds feedback_jobs.last_error.
const maxErrorLength = 1000

// JobQueue is the durable job store. *repository.FeedbackRepository satisfies it.
type JobQueue interface {
	ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (*domain.FeedbackJob, error)
	CompleteJob(ctx context.Context, id uuid.UUID) error
	RetryJob(ctx context.Context, id uuid.UUID, nextRunAt time.Time, lastErr string) error
	FailJob(ctx context.Context, id uuid.UUID, lastErr string) error
}

// Processor handles one feedback. *service.FeedbackProcessor satisfies it.
type Processor interface {
	Process(ctx context.Context, feedbackID uuid.UUID) error
}

// FeedbackWorker polls the job queue with a fixed number of goroutines.
type FeedbackWorker struct {
	queue JobQueue
	proc  Processor
	cfg   config.WorkerConfig
	log   *slog.Logger
	wake  chan struct{}
	now   func() time.Time
}

// NewFeedbackWorker returns a FeedbackWorker.
func NewFeedbackWorker(queue JobQueue, proc Processor, cfg config.WorkerConfig, log *slog.Logger) *FeedbackWorker {
	return &FeedbackWorker{
		queue: queue,
		proc:  proc,
		cfg:   cfg,
		log:   log.With(slog.String("component", "feedback_worker")),
		wake:  make(chan struct{}, 1),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// Notify wakes an idle worker goroutine so a new job starts without waiting for the next
// poll. It never blocks.
func (w *FeedbackWorker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run processes jobs until ctx is cancelled, then waits for in-flight jobs to finish.
func (w *FeedbackWorker) Run(ctx context.Context) error {
	w.log.Info("feedback worker started", slog.Int("concurrency", w.cfg.Concurrency))
	var wg sync.WaitGroup
	for range w.cfg.Concurrency {
		wg.Go(func() { w.loop(ctx) })
	}
	wg.Wait()
	w.log.Info("feedback worker stopped")
	return nil
}

func (w *FeedbackWorker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Drain every due job before sleeping again.
		for ctx.Err() == nil && w.runOne(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-ticker.C:
		}
	}
}

// runOne claims and handles one job. It reports whether a job was found.
func (w *FeedbackWorker) runOne(ctx context.Context) bool {
	job, err := w.queue.ClaimJob(ctx, w.now(), w.cfg.Lease)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("claim feedback job", slog.String("error", err.Error()))
		}
		return false
	}
	if job == nil {
		return false
	}
	w.handle(ctx, *job)
	return true
}

func (w *FeedbackWorker) handle(ctx context.Context, job domain.FeedbackJob) {
	log := w.log.With(
		slog.String("job_id", job.ID.String()),
		slog.String("feedback_id", job.FeedbackID.String()),
		slog.Int("attempt", job.Attempts),
	)
	// The job ID doubles as the request ID, so AI service logs can be correlated.
	jobCtx := requestid.WithContext(logger.WithContext(ctx, log), "job-"+job.ID.String())
	// Finish well before the lease runs out so no other worker picks the job up meanwhile.
	jobCtx, cancel := context.WithTimeout(jobCtx, w.cfg.Lease*9/10)
	defer cancel()

	started := time.Now()
	procErr := w.proc.Process(jobCtx, job.FeedbackID)

	// Record the outcome even when shutting down; otherwise the job waits for its lease.
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelSave()
	switch {
	case procErr == nil:
		if err := w.queue.CompleteJob(saveCtx, job.ID); err != nil {
			log.Error("complete feedback job", slog.String("error", err.Error()))
			return
		}
		log.Info("feedback processed", slog.Duration("duration", time.Since(started)))
	case job.Attempts >= w.cfg.MaxAttempts:
		if err := w.queue.FailJob(saveCtx, job.ID, truncate(procErr.Error())); err != nil {
			log.Error("fail feedback job", slog.String("error", err.Error()))
		}
		log.Error("feedback job failed permanently", slog.String("error", procErr.Error()))
	default:
		next := w.now().Add(Backoff(job.Attempts, w.cfg.BaseBackoff, w.cfg.MaxBackoff))
		if err := w.queue.RetryJob(saveCtx, job.ID, next, truncate(procErr.Error())); err != nil {
			log.Error("reschedule feedback job", slog.String("error", err.Error()))
		}
		level := slog.LevelWarn
		if errors.Is(procErr, context.Canceled) {
			level = slog.LevelInfo
		}
		log.Log(ctx, level, "feedback job will be retried", slog.Time("next_run_at", next), slog.String("error", procErr.Error()))
	}
}

// Backoff returns the delay before retry number attempt (1-based): base * 2^(attempt-1),
// capped at maxDelay, with ±20% jitter so failed jobs do not retry in lockstep.
func Backoff(attempt int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	jitter := 0.8 + 0.4*rand.Float64() //nolint:gosec // jitter does not need a secure source
	return time.Duration(float64(d) * jitter)
}

func truncate(s string) string {
	if len(s) <= maxErrorLength {
		return s
	}
	return s[:maxErrorLength]
}
