package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/domain"
)

// FeedbackRepository stores feedbacks, their processing jobs, and the Path A results
// (issues, opinions, vectors).
type FeedbackRepository struct {
	db *gorm.DB
}

// NewFeedbackRepository returns a FeedbackRepository backed by db.
func NewFeedbackRepository(db *gorm.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// Create inserts a feedback.
func (r *FeedbackRepository) Create(ctx context.Context, f domain.Feedback) (domain.Feedback, error) {
	if err := database.Conn(ctx, r.db).Create(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return domain.Feedback{}, apperror.Wrap(apperror.ErrNotFound, "shop not found", err)
		}
		return domain.Feedback{}, fmt.Errorf("insert feedback: %w", err)
	}
	return f, nil
}

// GetByID returns an active feedback.
func (r *FeedbackRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.Feedback, error) {
	var f domain.Feedback
	err := database.Conn(ctx, r.db).Where("id = ? AND is_deleted = false", id).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Feedback{}, apperror.NotFound("feedback not found")
	}
	if err != nil {
		return domain.Feedback{}, fmt.Errorf("select feedback: %w", err)
	}
	return f, nil
}

// LockFeedback takes a row lock on the feedback until the surrounding transaction ends,
// serializing concurrent writers of its processing results.
func (r *FeedbackRepository) LockFeedback(ctx context.Context, id uuid.UUID) error {
	var ids []uuid.UUID
	err := database.Conn(ctx, r.db).Model(&domain.Feedback{}).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Where("id = ?", id).
		Pluck("id", &ids).Error
	if err != nil {
		return fmt.Errorf("lock feedback: %w", err)
	}
	if len(ids) == 0 {
		return apperror.NotFound("feedback not found")
	}
	return nil
}

// EnqueueJob schedules the processing of a feedback. Call it in the same transaction as
// Create so a stored feedback always has a job.
func (r *FeedbackRepository) EnqueueJob(ctx context.Context, feedbackID uuid.UUID, runAt time.Time) error {
	job := domain.FeedbackJob{FeedbackID: feedbackID, Status: domain.JobPending, NextRunAt: runAt}
	err := database.Conn(ctx, r.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
	if err != nil {
		return fmt.Errorf("insert feedback job: %w", err)
	}
	return nil
}

// ClaimJob takes the next due job: a PENDING job whose next_run_at has passed, or a
// PROCESSING job whose lease expired (its worker died). The job is marked PROCESSING with
// a new lease and its attempt counter incremented. Concurrent workers never claim the same
// job thanks to FOR UPDATE SKIP LOCKED. It returns nil when nothing is due.
func (r *FeedbackRepository) ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (*domain.FeedbackJob, error) {
	var claimed *domain.FeedbackJob
	err := database.Conn(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		var job domain.FeedbackJob
		err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate, Options: clause.LockingOptionsSkipLocked}).
			Where("(status = ? AND next_run_at <= ?) OR (status = ? AND locked_until < ?)",
				domain.JobPending, now, domain.JobProcessing, now).
			Order("next_run_at").
			Limit(1).
			Find(&job).Error
		if err != nil {
			return fmt.Errorf("select due job: %w", err)
		}
		if job.ID == uuid.Nil {
			return nil
		}
		lockedUntil := now.Add(lease)
		job.Status, job.Attempts, job.LockedUntil = domain.JobProcessing, job.Attempts+1, &lockedUntil
		err = tx.Model(&domain.FeedbackJob{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":       job.Status,
			"attempts":     job.Attempts,
			"locked_until": lockedUntil,
			"updated_at":   now,
		}).Error
		if err != nil {
			return fmt.Errorf("lease job: %w", err)
		}
		claimed = &job
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// CompleteJob marks a job DONE.
func (r *FeedbackRepository) CompleteJob(ctx context.Context, id uuid.UUID) error {
	return r.updateJob(ctx, id, map[string]any{"status": domain.JobDone, "locked_until": nil, "last_error": nil})
}

// RetryJob puts a job back to PENDING, due at nextRunAt.
func (r *FeedbackRepository) RetryJob(ctx context.Context, id uuid.UUID, nextRunAt time.Time, lastErr string) error {
	return r.updateJob(ctx, id, map[string]any{
		"status": domain.JobPending, "next_run_at": nextRunAt, "locked_until": nil, "last_error": lastErr,
	})
}

// FailJob marks a job FAILED after its last attempt.
func (r *FeedbackRepository) FailJob(ctx context.Context, id uuid.UUID, lastErr string) error {
	return r.updateJob(ctx, id, map[string]any{"status": domain.JobFailed, "locked_until": nil, "last_error": lastErr})
}

func (r *FeedbackRepository) updateJob(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	updates["updated_at"] = time.Now().UTC()
	if err := database.Conn(ctx, r.db).Model(&domain.FeedbackJob{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("update feedback job: %w", err)
	}
	return nil
}

// HasOpinions reports whether opinions were already stored for a feedback. Results are
// written once, which makes reprocessing a job idempotent.
func (r *FeedbackRepository) HasOpinions(ctx context.Context, feedbackID uuid.UUID) (bool, error) {
	var n int64
	err := database.Conn(ctx, r.db).Model(&domain.FeedbackOpinion{}).
		Where("feedback_id = ? AND is_deleted = false", feedbackID).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("count opinions: %w", err)
	}
	return n > 0, nil
}

// ListCandidateIssues returns the active issues of a shop with their opinion counts.
func (r *FeedbackRepository) ListCandidateIssues(ctx context.Context, shopID uuid.UUID) ([]domain.CandidateIssue, error) {
	var rows []domain.CandidateIssue
	err := shopOpinions(database.Conn(ctx, r.db), shopID).
		Joins("JOIN issues i ON i.id = fo.issue_id AND i.is_deleted = false").
		Select("i.id AS id, i.title AS title, COUNT(fo.id) AS opinion_count").
		Group("i.id, i.title").
		Order("opinion_count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list candidate issues: %w", err)
	}
	return rows, nil
}

// ListCandidateVectors returns the most recent opinion embeddings of a shop.
func (r *FeedbackRepository) ListCandidateVectors(ctx context.Context, shopID uuid.UUID, limit int) ([]domain.CandidateVector, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []domain.CandidateVector
	err := shopOpinions(database.Conn(ctx, r.db), shopID).
		Joins("JOIN opinion_vectors ov ON ov.feedback_opinion_id = fo.id AND ov.is_deleted = false").
		Select("fo.id AS opinion_id, fo.issue_id AS issue_id, ov.embedding AS embedding, ov.embedding_model AS embedding_model").
		Order("ov.created_at DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list candidate vectors: %w", err)
	}
	return rows, nil
}

// CreateIssue inserts an issue.
func (r *FeedbackRepository) CreateIssue(ctx context.Context, issue domain.Issue) (domain.Issue, error) {
	if err := database.Conn(ctx, r.db).Create(&issue).Error; err != nil {
		return domain.Issue{}, fmt.Errorf("insert issue: %w", err)
	}
	return issue, nil
}

// TouchIssue bumps an issue's updated_at when new opinions join it.
func (r *FeedbackRepository) TouchIssue(ctx context.Context, id uuid.UUID) error {
	err := database.Conn(ctx, r.db).Model(&domain.Issue{}).Where("id = ?", id).Update("updated_at", time.Now().UTC()).Error
	if err != nil {
		return fmt.Errorf("touch issue: %w", err)
	}
	return nil
}

// CreateOpinion inserts an opinion and its embedding.
func (r *FeedbackRepository) CreateOpinion(ctx context.Context, op domain.FeedbackOpinion, vec domain.OpinionVector) error {
	db := database.Conn(ctx, r.db)
	if err := db.Create(&op).Error; err != nil {
		return fmt.Errorf("insert opinion: %w", err)
	}
	vec.FeedbackOpinionID = op.ID
	if err := db.Create(&vec).Error; err != nil {
		return fmt.Errorf("insert opinion vector: %w", err)
	}
	return nil
}

// shopOpinions starts a query over the active opinions ("fo") of a shop's active
// feedbacks ("f").
func shopOpinions(db *gorm.DB, shopID uuid.UUID) *gorm.DB {
	return db.Table("feedback_opinions AS fo").
		Joins("JOIN feedbacks f ON f.id = fo.feedback_id").
		Where("f.shop_id = ? AND fo.is_deleted = false AND f.is_deleted = false", shopID)
}
