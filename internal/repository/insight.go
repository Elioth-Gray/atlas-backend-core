package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/domain"
)

// InsightRepository runs the read-side queries behind the dashboard, the issue galaxy, and
// the consultation evidence. Every query is scoped to one shop through
// feedback_opinions -> feedbacks.shop_id, because issues have no shop_id column.
type InsightRepository struct {
	db *gorm.DB
}

// NewInsightRepository returns an InsightRepository backed by db.
func NewInsightRepository(db *gorm.DB) *InsightRepository {
	return &InsightRepository{db: db}
}

// IssueBelongsToShop reports whether the issue has at least one active opinion from an
// active feedback of the shop (the ownership rule for issues).
func (r *InsightRepository) IssueBelongsToShop(ctx context.Context, issueID, shopID uuid.UUID) (bool, error) {
	var ids []uuid.UUID
	err := shopOpinions(database.Conn(ctx, r.db), shopID).
		Where("fo.issue_id = ?", issueID).
		Limit(1).
		Pluck("fo.id", &ids).Error
	if err != nil {
		return false, fmt.Errorf("check issue ownership: %w", err)
	}
	return len(ids) > 0, nil
}

// ResolveIssueShop returns the shop that owns the issue among the shops the user is an
// active member of. It answers NotFound when the issue does not exist or belongs to
// another tenant, so foreign issue IDs cannot be probed.
func (r *InsightRepository) ResolveIssueShop(ctx context.Context, issueID, userID uuid.UUID) (uuid.UUID, error) {
	var shopIDs []uuid.UUID
	err := database.Conn(ctx, r.db).Table("feedback_opinions AS fo").
		Joins("JOIN feedbacks f ON f.id = fo.feedback_id AND f.is_deleted = false").
		Joins("JOIN shops s ON s.id = f.shop_id AND s.is_deleted = false").
		Joins("JOIN shops_users su ON su.shop_id = f.shop_id AND su.is_deleted = false AND su.user_id = ?", userID).
		Joins("JOIN issues i ON i.id = fo.issue_id AND i.is_deleted = false").
		Where("fo.issue_id = ? AND fo.is_deleted = false", issueID).
		Distinct("f.shop_id").
		Limit(1).
		Pluck("f.shop_id", &shopIDs).Error
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve issue shop: %w", err)
	}
	if len(shopIDs) == 0 {
		return uuid.Nil, apperror.NotFound("issue not found")
	}
	return shopIDs[0], nil
}

// GetIssue returns an active issue.
func (r *InsightRepository) GetIssue(ctx context.Context, id uuid.UUID) (domain.Issue, error) {
	var issue domain.Issue
	err := database.Conn(ctx, r.db).Where("id = ? AND is_deleted = false", id).First(&issue).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Issue{}, apperror.NotFound("issue not found")
	}
	if err != nil {
		return domain.Issue{}, fmt.Errorf("select issue: %w", err)
	}
	return issue, nil
}

// issueAggregateSelect computes IssueStats per issue. Unique feedbacks (COUNT DISTINCT f.id)
// and opinions (COUNT fo.id) are counted separately.
const issueAggregateSelect = `i.id AS issue_id, i.title AS title, i.created_at AS issue_created_at, i.updated_at AS issue_updated_at,
	COUNT(DISTINCT f.id) AS unique_feedback_count,
	COUNT(fo.id) AS opinion_count,
	MIN(f.created_at) AS first_feedback_at,
	MAX(f.created_at) AS last_feedback_at,
	MAX(fo.created_at) AS last_opinion_at,
	COUNT(*) FILTER (WHERE fo.sentiment = 'Negative') AS negative,
	COUNT(*) FILTER (WHERE fo.sentiment = 'Positive') AS positive,
	COUNT(*) FILTER (WHERE fo.sentiment = 'Neutral') AS neutral,
	COUNT(*) FILTER (WHERE fo.severity = 'Severe') AS severe`

type issueAggregateRow struct {
	IssueID             uuid.UUID
	Title               string
	IssueCreatedAt      time.Time
	IssueUpdatedAt      time.Time
	UniqueFeedbackCount int64
	OpinionCount        int64
	FirstFeedbackAt     *time.Time
	LastFeedbackAt      *time.Time
	LastOpinionAt       *time.Time
	Negative            int64
	Positive            int64
	Neutral             int64
	Severe              int64
}

func (row issueAggregateRow) summary() domain.IssueSummary {
	return domain.IssueSummary{
		Issue:         domain.Issue{ID: row.IssueID, Title: row.Title, CreatedAt: row.IssueCreatedAt, UpdatedAt: row.IssueUpdatedAt},
		LastOpinionAt: row.LastOpinionAt,
		Stats: domain.IssueStats{
			UniqueFeedbackCount: row.UniqueFeedbackCount,
			OpinionCount:        row.OpinionCount,
			FirstFeedbackAt:     row.FirstFeedbackAt,
			LastFeedbackAt:      row.LastFeedbackAt,
			Sentiment:           domain.SentimentBreakdown{Negative: row.Negative, Positive: row.Positive, Neutral: row.Neutral},
			Severity:            domain.SeverityBreakdown{Severe: row.Severe, NotSevere: row.OpinionCount - row.Severe},
		},
	}
}

func (r *InsightRepository) shopIssues(ctx context.Context, shopID uuid.UUID) *gorm.DB {
	return shopOpinions(database.Conn(ctx, r.db), shopID).
		Joins("JOIN issues i ON i.id = fo.issue_id AND i.is_deleted = false").
		Where("fo.sentiment = ?", domain.SentimentNegative)
}

func (r *InsightRepository) shopIssuesFiltered(ctx context.Context, shopID uuid.UUID, filter domain.IssueFilter) *gorm.DB {
	q := r.shopIssues(ctx, shopID)
	if filter.From != nil {
		q = q.Where("f.created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("f.created_at < ?", *filter.To)
	}
	return q
}

// ListIssueSummaries returns one page of the shop's issues with their statistics, the
// largest first, and the total number of issues.
func (r *InsightRepository) ListIssueSummaries(ctx context.Context, shopID uuid.UUID, filter domain.IssueFilter, limit, offset int) ([]domain.IssueSummary, int64, error) {
	var total int64
	if err := r.shopIssuesFiltered(ctx, shopID, filter).Distinct("fo.issue_id").Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count issues: %w", err)
	}
	if total == 0 {
		return []domain.IssueSummary{}, 0, nil
	}
	var rows []issueAggregateRow
	err := r.shopIssuesFiltered(ctx, shopID, filter).
		Select(issueAggregateSelect).
		Group("i.id, i.title, i.created_at, i.updated_at").
		Order("opinion_count DESC").
		Order("last_feedback_at DESC").
		Order("issue_id").
		Limit(limit).
		Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("aggregate issues: %w", err)
	}
	out, err := r.withRatings(ctx, shopID, rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// GetIssueSummary returns one issue with its statistics within the shop.
func (r *InsightRepository) GetIssueSummary(ctx context.Context, shopID, issueID uuid.UUID) (domain.IssueSummary, error) {
	var rows []issueAggregateRow
	err := r.shopIssues(ctx, shopID).
		Where("fo.issue_id = ?", issueID).
		Select(issueAggregateSelect).
		Group("i.id, i.title, i.created_at, i.updated_at").
		Scan(&rows).Error
	if err != nil {
		return domain.IssueSummary{}, fmt.Errorf("aggregate issue: %w", err)
	}
	if len(rows) == 0 {
		return domain.IssueSummary{}, apperror.NotFound("issue not found")
	}
	out, err := r.withRatings(ctx, shopID, rows)
	if err != nil {
		return domain.IssueSummary{}, err
	}
	return out[0], nil
}

// withRatings adds the average rating per issue, computed over distinct feedbacks so a
// feedback with several opinions in the same issue is not counted twice.
func (r *InsightRepository) withRatings(ctx context.Context, shopID uuid.UUID, rows []issueAggregateRow) ([]domain.IssueSummary, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.IssueID
	}
	distinct := shopOpinions(database.Conn(ctx, r.db), shopID).
		Where("fo.issue_id IN ? AND fo.sentiment = ?", ids, domain.SentimentNegative).
		Select("DISTINCT fo.issue_id, f.id, f.rating")
	var ratings []struct {
		IssueID       uuid.UUID
		AverageRating float64
	}
	err := database.Conn(ctx, r.db).Table("(?) AS x", distinct).
		Select("x.issue_id AS issue_id, COALESCE(AVG(x.rating), 0)::float8 AS average_rating").
		Group("x.issue_id").
		Scan(&ratings).Error
	if err != nil {
		return nil, fmt.Errorf("average issue ratings: %w", err)
	}
	byIssue := make(map[uuid.UUID]float64, len(ratings))
	for _, rt := range ratings {
		byIssue[rt.IssueID] = rt.AverageRating
	}
	out := make([]domain.IssueSummary, len(rows))
	for i, row := range rows {
		out[i] = row.summary()
		out[i].Stats.AverageRating = byIssue[row.IssueID]
	}
	return out, nil
}

// LastArchivedAt returns, per issue, when its latest consultation in the shop was archived.
func (r *InsightRepository) LastArchivedAt(ctx context.Context, shopID uuid.UUID, issueIDs []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	if len(issueIDs) == 0 {
		return map[uuid.UUID]time.Time{}, nil
	}
	var rows []struct {
		IssueID        uuid.UUID
		LastArchivedAt time.Time
	}
	err := database.Conn(ctx, r.db).Model(&domain.ConsultationSession{}).
		Select("issue_id, MAX(archived_at) AS last_archived_at").
		Where("shop_id = ? AND status = ? AND issue_id IN ?", shopID, domain.SessionArchived, issueIDs).
		Group("issue_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("select last archived sessions: %w", err)
	}
	out := make(map[uuid.UUID]time.Time, len(rows))
	for _, row := range rows {
		out[row.IssueID] = row.LastArchivedAt
	}
	return out, nil
}

// evidenceSelect maps an opinion and its feedback onto domain.EvidenceOpinion.
const evidenceSelect = `fo.id AS opinion_id, fo.opinion_text AS opinion_text, fo.evidence_text AS evidence_text,
	fo.sentiment AS sentiment, fo.severity AS severity, f.id AS feedback_id, f.feedback AS full_feedback,
	f.rating AS rating, f.created_at AS created_at`

// LatestEvidence returns the newest negative opinions of the issue within the shop.
func (r *InsightRepository) LatestEvidence(ctx context.Context, shopID, issueID uuid.UUID, limit int) ([]domain.EvidenceOpinion, error) {
	var out []domain.EvidenceOpinion
	err := shopOpinions(database.Conn(ctx, r.db), shopID).
		Where("fo.issue_id = ? AND fo.sentiment = ?", issueID, domain.SentimentNegative).
		Select(evidenceSelect).
		Order("f.created_at DESC").
		Order("fo.id").
		Limit(limit).
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("select latest evidence: %w", err)
	}
	return out, nil
}

// RepresentativeEvidence returns a sample of the issue's negative opinions for the AI: severe
// first, then the most recent.
func (r *InsightRepository) RepresentativeEvidence(ctx context.Context, shopID, issueID uuid.UUID, limit int) ([]domain.EvidenceOpinion, error) {
	var out []domain.EvidenceOpinion
	err := shopOpinions(database.Conn(ctx, r.db), shopID).
		Where("fo.issue_id = ? AND fo.sentiment = ?", issueID, domain.SentimentNegative).
		Select(evidenceSelect).
		Order("(fo.severity IS NOT NULL) DESC").
		Order("(fo.sentiment = 'Negative') DESC").
		Order("f.created_at DESC").
		Order("fo.id").
		Limit(limit).
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("select representative evidence: %w", err)
	}
	return out, nil
}

func applyFeedbackRangeFilter(q *gorm.DB, shopID uuid.UUID, rg domain.DashboardRange) *gorm.DB {
	q = q.Where("shop_id = ? AND is_deleted = false AND created_at >= ? AND created_at < ?", shopID, rg.From, rg.To)
	switch rg.Sentiment {
	case string(domain.SentimentPositive):
		q = q.Where(`(rating >= 4 OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND fo.sentiment = 'Positive'))`)
	case string(domain.SentimentNeutral):
		q = q.Where(`((rating >= 3 AND rating < 4) OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND fo.sentiment = 'Neutral'))`)
	case string(domain.SentimentNegative):
		q = q.Where(`(rating < 3 OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND fo.sentiment = 'Negative'))`)
	}
	switch rg.Priority {
	case "high":
		q = q.Where(`(rating <= 2 OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND fo.severity = 'Severe'))`)
	case "medium":
		q = q.Where(`((rating > 2 AND rating <= 3) OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND (fo.severity IS NULL OR fo.severity = 'NotSevere') AND fo.sentiment IN ('Negative', 'Neutral')))`)
	case "low":
		q = q.Where(`(rating >= 4 OR EXISTS (SELECT 1 FROM feedback_opinions fo WHERE fo.feedback_id = feedbacks.id AND fo.is_deleted = false AND fo.sentiment = 'Positive'))`)
	}
	return q
}

func applyOpinionRangeFilter(q *gorm.DB, rg domain.DashboardRange) *gorm.DB {
	q = q.Where("f.created_at >= ? AND f.created_at < ?", rg.From, rg.To)
	if rg.Sentiment != "" {
		q = q.Where("fo.sentiment = ?", rg.Sentiment)
	}
	switch rg.Priority {
	case "high":
		q = q.Where("(fo.severity = 'Severe' OR f.rating <= 2)")
	case "medium":
		q = q.Where("((fo.severity IS NULL OR fo.severity = 'NotSevere') AND fo.sentiment IN ('Negative', 'Neutral') AND f.rating > 2)")
	case "low":
		q = q.Where("(fo.sentiment = 'Positive' OR f.rating >= 4)")
	}
	return q
}

// DashboardKPI returns the headline numbers of the shop within the range.
func (r *InsightRepository) DashboardKPI(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) (domain.DashboardKPI, error) {
	var fb struct {
		TotalFeedbacks int64
		AverageRating  float64
	}
	err := applyFeedbackRangeFilter(database.Conn(ctx, r.db).Model(&domain.Feedback{}), shopID, rg).
		Select("COUNT(*) AS total_feedbacks, COALESCE(AVG(rating), 0)::float8 AS average_rating").
		Scan(&fb).Error
	if err != nil {
		return domain.DashboardKPI{}, fmt.Errorf("dashboard feedback kpi: %w", err)
	}
	var op struct {
		TotalOpinions int64
		Negative      int64
		Positive      int64
		Neutral       int64
		Severe        int64
		ActiveIssues  int64
	}
	err = applyOpinionRangeFilter(shopOpinions(database.Conn(ctx, r.db), shopID), rg).
		Select(`COUNT(fo.id) AS total_opinions,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Negative') AS negative,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Positive') AS positive,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Neutral') AS neutral,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Negative' AND fo.severity = 'Severe') AS severe,
			COUNT(DISTINCT fo.issue_id) FILTER (WHERE fo.sentiment = 'Negative') AS active_issues`).
		Scan(&op).Error
	if err != nil {
		return domain.DashboardKPI{}, fmt.Errorf("dashboard opinion kpi: %w", err)
	}
	return domain.DashboardKPI{
		TotalFeedbacks: fb.TotalFeedbacks,
		AverageRating:  fb.AverageRating,
		TotalOpinions:  op.TotalOpinions,
		Sentiment:      domain.SentimentBreakdown{Negative: op.Negative, Positive: op.Positive, Neutral: op.Neutral},
		SevereOpinions: op.Severe,
		ActiveIssues:   op.ActiveIssues,
	}, nil
}

// SentimentTrend returns opinion counts per sentiment per local day (only days with data).
func (r *InsightRepository) SentimentTrend(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) ([]domain.SentimentTrendPoint, error) {
	var rows []struct {
		Bucket   time.Time
		Negative int64
		Positive int64
		Neutral  int64
	}
	err := applyOpinionRangeFilter(shopOpinions(database.Conn(ctx, r.db), shopID), rg).
		Select(`date_trunc('day', f.created_at AT TIME ZONE ?) AS bucket,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Negative') AS negative,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Positive') AS positive,
			COUNT(*) FILTER (WHERE fo.sentiment = 'Neutral') AS neutral`, rg.TimeZone).
		Group("bucket").
		Order("bucket").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dashboard sentiment trend: %w", err)
	}
	out := make([]domain.SentimentTrendPoint, len(rows))
	for i, row := range rows {
		out[i] = domain.SentimentTrendPoint{
			Date:      row.Bucket,
			Sentiment: domain.SentimentBreakdown{Negative: row.Negative, Positive: row.Positive, Neutral: row.Neutral},
		}
	}
	return out, nil
}

// WeeklyReviews returns feedback counts per local week (only weeks with data).
func (r *InsightRepository) WeeklyReviews(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) ([]domain.WeeklyReviewBucket, error) {
	var rows []struct {
		Bucket        time.Time
		FeedbackCount int64
		AverageRating float64
	}
	err := applyFeedbackRangeFilter(database.Conn(ctx, r.db).Model(&domain.Feedback{}), shopID, rg).
		Select(`date_trunc('week', created_at AT TIME ZONE ?) AS bucket,
			COUNT(*) AS feedback_count, COALESCE(AVG(rating), 0)::float8 AS average_rating`, rg.TimeZone).
		Group("bucket").
		Order("bucket").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dashboard weekly reviews: %w", err)
	}
	out := make([]domain.WeeklyReviewBucket, len(rows))
	for i, row := range rows {
		out[i] = domain.WeeklyReviewBucket{WeekStart: row.Bucket, FeedbackCount: row.FeedbackCount, AverageRating: row.AverageRating}
	}
	return out, nil
}

// TopComplaints returns the issues with the most negative opinions within the range.
func (r *InsightRepository) TopComplaints(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange, limit int) ([]domain.TopComplaint, error) {
	var out []domain.TopComplaint
	err := applyOpinionRangeFilter(r.shopIssues(ctx, shopID), rg).
		Where("fo.sentiment = ?", domain.SentimentNegative).
		Select(`i.id AS issue_id, i.title AS title, COUNT(fo.id) AS negative_opinions,
			COUNT(DISTINCT f.id) AS unique_feedbacks,
			COUNT(*) FILTER (WHERE fo.severity = 'Severe') AS severe_opinions`).
		Group("i.id, i.title").
		Order("negative_opinions DESC").
		Order("severe_opinions DESC").
		Order("issue_id").
		Limit(limit).
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("dashboard top complaints: %w", err)
	}
	return out, nil
}

// RecentFeedbacks returns the shop's newest feedbacks matching the range and filters.
func (r *InsightRepository) RecentFeedbacks(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange, limit int) ([]domain.Feedback, error) {
	var out []domain.Feedback
	err := applyFeedbackRangeFilter(database.Conn(ctx, r.db), shopID, rg).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("dashboard recent feedbacks: %w", err)
	}
	return out, nil
}
