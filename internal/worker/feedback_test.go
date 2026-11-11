package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/config"
	"atlas-backend-core/internal/domain"
)

func TestBackoff(t *testing.T) {
	base, maxDelay := 10*time.Second, 5*time.Minute
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{6, 5 * time.Minute}, // 320s capped
		{50, 5 * time.Minute},
	}
	for _, tt := range tests {
		for range 20 {
			got := Backoff(tt.attempt, base, maxDelay)
			lo, hi := time.Duration(float64(tt.want)*0.8), time.Duration(float64(tt.want)*1.2)
			if got < lo || got > hi {
				t.Fatalf("Backoff(%d) = %s, want within [%s, %s]", tt.attempt, got, lo, hi)
			}
		}
	}
}

type fakeQueue struct {
	job       *domain.FeedbackJob
	completed bool
	retryAt   time.Time
	failed    bool
}

func (q *fakeQueue) ClaimJob(context.Context, time.Time, time.Duration) (*domain.FeedbackJob, error) {
	j := q.job
	q.job = nil
	return j, nil
}

func (q *fakeQueue) CompleteJob(context.Context, uuid.UUID) error { q.completed = true; return nil }

func (q *fakeQueue) RetryJob(_ context.Context, _ uuid.UUID, at time.Time, _ string) error {
	q.retryAt = at
	return nil
}

func (q *fakeQueue) FailJob(context.Context, uuid.UUID, string) error { q.failed = true; return nil }

type procFunc func(ctx context.Context, id uuid.UUID) error

func (f procFunc) Process(ctx context.Context, id uuid.UUID) error { return f(ctx, id) }

func TestFeedbackWorker_RunOne(t *testing.T) {
	cfg := config.WorkerConfig{MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute, Lease: time.Minute}
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		attempts    int
		procErr     error
		wantDone    bool
		wantRetried bool
		wantFailed  bool
	}{
		{name: "success completes the job", attempts: 1, wantDone: true},
		{name: "failure is retried with backoff", attempts: 1, procErr: errors.New("ai down"), wantRetried: true},
		{name: "last attempt fails permanently", attempts: 3, procErr: errors.New("ai down"), wantFailed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQueue{job: &domain.FeedbackJob{ID: uuid.New(), FeedbackID: uuid.New(), Attempts: tt.attempts}}
			w := NewFeedbackWorker(q, procFunc(func(context.Context, uuid.UUID) error { return tt.procErr }), cfg, slog.New(slog.DiscardHandler))
			w.now = func() time.Time { return now }

			if !w.runOne(context.Background()) {
				t.Fatal("runOne found no job")
			}
			if q.completed != tt.wantDone || q.failed != tt.wantFailed || q.retryAt.IsZero() == tt.wantRetried {
				t.Fatalf("done=%t failed=%t retryAt=%s", q.completed, q.failed, q.retryAt)
			}
			if tt.wantRetried && !q.retryAt.After(now) {
				t.Errorf("retry must be in the future, got %s", q.retryAt)
			}
			if w.runOne(context.Background()) {
				t.Error("queue should be empty")
			}
		})
	}
}
