package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/logger"
)

// Rating bounds of a feedback.
const (
	MinRating = 1.0
	MaxRating = 5.0
)

// FeedbackWriter stores feedbacks and schedules their processing.
type FeedbackWriter interface {
	Create(ctx context.Context, f domain.Feedback) (domain.Feedback, error)
	EnqueueJob(ctx context.Context, feedbackID uuid.UUID, runAt time.Time) error
}

// ShopReader reads active shops.
type ShopReader interface {
	GetActive(ctx context.Context, id uuid.UUID) (domain.Shop, error)
}

// JobNotifier wakes the feedback worker after a job was enqueued.
type JobNotifier interface {
	Notify()
}

// SubmitFeedbackInput is a customer review.
type SubmitFeedbackInput struct {
	Name   *string
	Rating float64
	Text   string
	Image  *string
}

// FeedbackService accepts customer feedback (Path A, synchronous part).
type FeedbackService struct {
	feedbacks FeedbackWriter
	shops     ShopReader
	tx        TxRunner
	notifier  JobNotifier
	now       func() time.Time
}

// NewFeedbackService returns a FeedbackService. notifier may be nil.
func NewFeedbackService(feedbacks FeedbackWriter, shops ShopReader, tx TxRunner, notifier JobNotifier) *FeedbackService {
	return &FeedbackService{feedbacks: feedbacks, shops: shops, tx: tx, notifier: notifier, now: func() time.Time { return time.Now().UTC() }}
}

// Submit stores a feedback for an active shop and schedules its NLP/ML processing. It
// returns as soon as the feedback is stored; the AI runs in the background worker.
func (s *FeedbackService) Submit(ctx context.Context, shopID uuid.UUID, in SubmitFeedbackInput) (domain.Feedback, error) {
	text, err := requiredText("feedback", in.Text)
	if err != nil {
		return domain.Feedback{}, err
	}
	rating := math.Round(in.Rating*10) / 10
	if rating < MinRating || rating > MaxRating {
		return domain.Feedback{}, apperror.InvalidInput("rating must be between 1.0 and 5.0")
	}
	if _, err := s.shops.GetActive(ctx, shopID); err != nil {
		return domain.Feedback{}, fmt.Errorf("submit feedback: %w", err)
	}

	var fb domain.Feedback
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		created, err := s.feedbacks.Create(ctx, domain.Feedback{
			Name:   optionalText(in.Name),
			Rating: rating,
			Text:   text,
			Image:  optionalText(in.Image),
			ShopID: shopID,
		})
		if err != nil {
			return err
		}
		fb = created
		return s.feedbacks.EnqueueJob(ctx, created.ID, s.now())
	})
	if err != nil {
		return domain.Feedback{}, fmt.Errorf("submit feedback: %w", err)
	}
	if s.notifier != nil {
		s.notifier.Notify()
	}
	return fb, nil
}

// FeedbackProcessingRepository is the persistence FeedbackProcessor needs.
type FeedbackProcessingRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Feedback, error)
	LockFeedback(ctx context.Context, id uuid.UUID) error
	HasOpinions(ctx context.Context, feedbackID uuid.UUID) (bool, error)
	ListCandidateIssues(ctx context.Context, shopID uuid.UUID) ([]domain.CandidateIssue, error)
	ListCandidateVectors(ctx context.Context, shopID uuid.UUID, limit int) ([]domain.CandidateVector, error)
	CreateIssue(ctx context.Context, issue domain.Issue) (domain.Issue, error)
	TouchIssue(ctx context.Context, id uuid.UUID) error
	CreateOpinion(ctx context.Context, op domain.FeedbackOpinion, vec domain.OpinionVector) error
}

// IssueOwnership checks that an issue belongs to a shop.
type IssueOwnership interface {
	IssueBelongsToShop(ctx context.Context, issueID, shopID uuid.UUID) (bool, error)
}

// FeedbackProcessor runs the NLP/ML pipeline for one feedback (Path A, worker part).
type FeedbackProcessor struct {
	repo           FeedbackProcessingRepository
	shops          ShopReader
	issues         IssueOwnership
	ai             AIClient
	tx             TxRunner
	candidateLimit int
}

// NewFeedbackProcessor returns a FeedbackProcessor. candidateLimit caps the opinion
// embeddings sent to the AI as clustering candidates.
func NewFeedbackProcessor(repo FeedbackProcessingRepository, shops ShopReader, issues IssueOwnership, ai AIClient, tx TxRunner, candidateLimit int) *FeedbackProcessor {
	return &FeedbackProcessor{repo: repo, shops: shops, issues: issues, ai: ai, tx: tx, candidateLimit: candidateLimit}
}

// Process analyses a feedback with the AI and stores its issues, opinions, and vectors in
// one transaction. It is idempotent: a feedback that already has opinions is skipped, so
// a retried job never duplicates data. Deleted feedbacks or shops are skipped silently.
func (p *FeedbackProcessor) Process(ctx context.Context, feedbackID uuid.UUID) error {
	log := logger.FromContext(ctx)
	fb, err := p.repo.GetByID(ctx, feedbackID)
	if errors.Is(err, apperror.ErrNotFound) {
		log.WarnContext(ctx, "feedback no longer exists, skipping", slog.String("feedback_id", feedbackID.String()))
		return nil
	}
	if err != nil {
		return err
	}
	done, err := p.repo.HasOpinions(ctx, fb.ID)
	if err != nil || done {
		return err
	}
	shop, err := p.shops.GetActive(ctx, fb.ShopID)
	if errors.Is(err, apperror.ErrNotFound) {
		log.WarnContext(ctx, "shop no longer active, skipping feedback", slog.String("feedback_id", feedbackID.String()))
		return nil
	}
	if err != nil {
		return err
	}

	candidates, err := p.repo.ListCandidateIssues(ctx, shop.ID)
	if err != nil {
		return err
	}
	vectors, err := p.repo.ListCandidateVectors(ctx, shop.ID, p.candidateLimit)
	if err != nil {
		return err
	}
	analysis, err := p.ai.ProcessFeedback(ctx, FeedbackAnalysisRequest{
		Feedback:         fb,
		Shop:             shopProfile(shop),
		CandidateIssues:  candidates,
		CandidateVectors: vectors,
	})
	if err != nil {
		return fmt.Errorf("ai process feedback: %w", err)
	}
	opinions := validOpinions(ctx, analysis.Opinions)

	known := make(map[uuid.UUID]bool, len(candidates))
	for _, c := range candidates {
		known[c.ID] = true
	}
	return p.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Serialize with any other worker holding this feedback, then re-check.
		if err := p.repo.LockFeedback(ctx, fb.ID); err != nil {
			return err
		}
		if done, err := p.repo.HasOpinions(ctx, fb.ID); err != nil || done {
			return err
		}
		return p.store(ctx, fb, opinions, known)
	})
}

// store inserts the opinions, creating new issues as needed. Opinions proposing the same
// new title share one issue. An existing issue ID that does not belong to the shop is
// never trusted: the opinion gets a new issue instead.
func (p *FeedbackProcessor) store(ctx context.Context, fb domain.Feedback, opinions []ExtractedOpinion, known map[uuid.UUID]bool) error {
	newIssues := make(map[string]uuid.UUID)
	touched := make(map[uuid.UUID]bool)
	for _, op := range opinions {
		issueID, err := p.resolveIssue(ctx, fb.ShopID, op, known, newIssues, touched)
		if err != nil {
			return err
		}
		var severity *domain.Severity
		if op.Severe {
			sev := domain.SeveritySevere
			severity = &sev
		}
		evidence := optionalText(&op.EvidenceText)
		err = p.repo.CreateOpinion(ctx,
			domain.FeedbackOpinion{
				OpinionText:  op.OpinionText,
				EvidenceText: evidence,
				Sentiment:    op.Sentiment,
				Severity:     severity,
				FeedbackID:   fb.ID,
				IssueID:      issueID,
			},
			domain.OpinionVector{Embedding: pgvector.NewVector(op.Embedding), EmbeddingModel: op.EmbeddingModel},
		)
		if err != nil {
			return err
		}
	}
	for id := range touched {
		if err := p.repo.TouchIssue(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *FeedbackProcessor) resolveIssue(ctx context.Context, shopID uuid.UUID, op ExtractedOpinion,
	known map[uuid.UUID]bool, newIssues map[string]uuid.UUID, touched map[uuid.UUID]bool,
) (*uuid.UUID, error) {
	if op.ExistingIssueID != nil {
		id := *op.ExistingIssueID
		ok := known[id]
		if !ok {
			var err error
			if ok, err = p.issues.IssueBelongsToShop(ctx, id, shopID); err != nil {
				return nil, err
			}
		}
		if ok {
			known[id], touched[id] = true, true
			return &id, nil
		}
		logger.FromContext(ctx).WarnContext(ctx, "AI assigned an issue of another shop, creating a new issue instead",
			slog.String("issue_id", id.String()))
	}
	title := strings.TrimSpace(op.IssueTitle)
	if title == "" {
		return nil, nil
	}
	key := strings.ToLower(title)
	if id, ok := newIssues[key]; ok {
		return &id, nil
	}
	issue, err := p.repo.CreateIssue(ctx, domain.Issue{Title: title})
	if err != nil {
		return nil, err
	}
	newIssues[key] = issue.ID
	return &issue.ID, nil
}

// validOpinions drops opinions the database cannot store (no text, no sentiment, no
// embedding) instead of failing the whole feedback.
func validOpinions(ctx context.Context, in []ExtractedOpinion) []ExtractedOpinion {
	out := make([]ExtractedOpinion, 0, len(in))
	for i, op := range in {
		op.OpinionText = strings.TrimSpace(op.OpinionText)
		op.EmbeddingModel = strings.TrimSpace(op.EmbeddingModel)
		switch {
		case op.OpinionText == "", len(op.Embedding) == 0, op.EmbeddingModel == "":
		case op.Sentiment != domain.SentimentNegative && op.Sentiment != domain.SentimentPositive && op.Sentiment != domain.SentimentNeutral:
		default:
			out = append(out, op)
			continue
		}
		logger.FromContext(ctx).WarnContext(ctx, "dropping invalid opinion from AI response", slog.Int("index", i))
	}
	return out
}

// shopProfile maps a shop (with category and scale loaded) to its AI profile.
func shopProfile(s domain.Shop) domain.ShopProfile {
	p := domain.ShopProfile{ID: s.ID, Name: s.Name}
	if s.Description != nil {
		p.Description = *s.Description
	}
	if s.Category != nil {
		p.Category = s.Category.Category
	}
	if s.Scale != nil {
		p.Scale = s.Scale.Scale
	}
	return p
}
