package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// Dashboard and issue detail limits.
const (
	DefaultTimeZone      = "Asia/Jakarta"
	defaultDashboardDays = 30
	maxDashboardDays     = 366
	topComplaintsLimit   = 5
	recentFeedbacksLimit = 10
	issueDetailEvidence  = 5
	dateLayout           = "2006-01-02"
	daysPerWeek          = 7
)

// InsightRepository is the read-side persistence InsightService needs.
type InsightRepository interface {
	IssueOwnership
	ResolveIssueShop(ctx context.Context, issueID, userID uuid.UUID) (uuid.UUID, error)
	ListIssueSummaries(ctx context.Context, shopID uuid.UUID, limit, offset int) ([]domain.IssueSummary, int64, error)
	GetIssueSummary(ctx context.Context, shopID, issueID uuid.UUID) (domain.IssueSummary, error)
	LastArchivedAt(ctx context.Context, shopID uuid.UUID, issueIDs []uuid.UUID) (map[uuid.UUID]time.Time, error)
	LatestEvidence(ctx context.Context, shopID, issueID uuid.UUID, limit int) ([]domain.EvidenceOpinion, error)
	RepresentativeEvidence(ctx context.Context, shopID, issueID uuid.UUID, limit int) ([]domain.EvidenceOpinion, error)
	DashboardKPI(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) (domain.DashboardKPI, error)
	SentimentTrend(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) ([]domain.SentimentTrendPoint, error)
	WeeklyReviews(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange) ([]domain.WeeklyReviewBucket, error)
	TopComplaints(ctx context.Context, shopID uuid.UUID, rg domain.DashboardRange, limit int) ([]domain.TopComplaint, error)
	RecentFeedbacks(ctx context.Context, shopID uuid.UUID, limit int) ([]domain.Feedback, error)
}

// SessionLister lists the consultations of an issue.
type SessionLister interface {
	ListSessionsByIssue(ctx context.Context, shopID, issueID uuid.UUID) ([]domain.ConsultationSession, error)
}

// DashboardInput selects the dashboard range. From and To are calendar dates (inclusive)
// in TimeZone; nil means the last 30 days up to today.
type DashboardInput struct {
	From     *time.Time
	To       *time.Time
	TimeZone string
}

// IssueDetail is one issue with its evidence and consultation history.
type IssueDetail struct {
	ShopID   uuid.UUID
	Summary  domain.IssueSummary
	Evidence []domain.EvidenceOpinion
	Sessions []domain.ConsultationSession
}

// InsightService serves the dashboard and the issue galaxy.
type InsightService struct {
	repo     InsightRepository
	members  MembershipReader
	sessions SessionLister
	now      func() time.Time
}

// NewInsightService returns an InsightService.
func NewInsightService(repo InsightRepository, members MembershipReader, sessions SessionLister) *InsightService {
	return &InsightService{repo: repo, members: members, sessions: sessions, now: time.Now}
}

// Dashboard returns the shop overview for the selected range.
func (s *InsightService) Dashboard(ctx context.Context, userID, shopID uuid.UUID, in DashboardInput) (domain.Dashboard, error) {
	if _, err := requireMember(ctx, s.members, userID, shopID); err != nil {
		return domain.Dashboard{}, err
	}
	rg, loc, err := s.dashboardRange(in)
	if err != nil {
		return domain.Dashboard{}, err
	}

	d := domain.Dashboard{Range: rg}
	if d.KPI, err = s.repo.DashboardKPI(ctx, shopID, rg); err != nil {
		return domain.Dashboard{}, err
	}
	trend, err := s.repo.SentimentTrend(ctx, shopID, rg)
	if err != nil {
		return domain.Dashboard{}, err
	}
	d.SentimentTrend = fillDays(trend, rg, loc)
	weekly, err := s.repo.WeeklyReviews(ctx, shopID, rg)
	if err != nil {
		return domain.Dashboard{}, err
	}
	d.WeeklyReviews = fillWeeks(weekly, rg, loc)
	if d.TopComplaints, err = s.repo.TopComplaints(ctx, shopID, rg, topComplaintsLimit); err != nil {
		return domain.Dashboard{}, err
	}
	if d.RecentFeedbacks, err = s.repo.RecentFeedbacks(ctx, shopID, recentFeedbacksLimit); err != nil {
		return domain.Dashboard{}, err
	}
	return d, nil
}

// ListIssues returns one page of the shop's issues with statistics and planet status.
func (s *InsightService) ListIssues(ctx context.Context, userID, shopID uuid.UUID, params PageParams) (Page[domain.IssueSummary], error) {
	if _, err := requireMember(ctx, s.members, userID, shopID); err != nil {
		return Page[domain.IssueSummary]{}, err
	}
	params = params.Normalize()
	items, total, err := s.repo.ListIssueSummaries(ctx, shopID, params.PageSize, params.Offset())
	if err != nil {
		return Page[domain.IssueSummary]{}, fmt.Errorf("list issues: %w", err)
	}
	if err := s.withPlanetStatus(ctx, shopID, items); err != nil {
		return Page[domain.IssueSummary]{}, err
	}
	return Page[domain.IssueSummary]{Items: items, Total: total, Page: params.Page, PageSize: params.PageSize}, nil
}

// GetIssue returns one issue of a shop the user belongs to, its latest evidence, and its
// consultation history.
func (s *InsightService) GetIssue(ctx context.Context, userID, issueID uuid.UUID) (IssueDetail, error) {
	shopID, err := s.repo.ResolveIssueShop(ctx, issueID, userID)
	if err != nil {
		return IssueDetail{}, err
	}
	summary, err := s.repo.GetIssueSummary(ctx, shopID, issueID)
	if err != nil {
		return IssueDetail{}, err
	}
	items := []domain.IssueSummary{summary}
	if err := s.withPlanetStatus(ctx, shopID, items); err != nil {
		return IssueDetail{}, err
	}
	evidence, err := s.repo.LatestEvidence(ctx, shopID, issueID, issueDetailEvidence)
	if err != nil {
		return IssueDetail{}, err
	}
	sessions, err := s.sessions.ListSessionsByIssue(ctx, shopID, issueID)
	if err != nil {
		return IssueDetail{}, err
	}
	return IssueDetail{ShopID: shopID, Summary: items[0], Evidence: evidence, Sessions: sessions}, nil
}

func (s *InsightService) withPlanetStatus(ctx context.Context, shopID uuid.UUID, items []domain.IssueSummary) error {
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.Issue.ID
	}
	archived, err := s.repo.LastArchivedAt(ctx, shopID, ids)
	if err != nil {
		return err
	}
	for i := range items {
		at, ok := archived[items[i].Issue.ID]
		if !ok {
			items[i].PlanetStatus = domain.PlanetUnconsulted
			continue
		}
		items[i].LastConsultedAt = &at
		items[i].PlanetStatus = PlanetStatus(items[i].LastOpinionAt, at)
	}
	return nil
}

// PlanetStatus classifies an issue that has an archived consultation: new opinions after
// the archive mean new reviews arrived since the owner last acted.
func PlanetStatus(lastOpinionAt *time.Time, lastArchivedAt time.Time) domain.PlanetStatus {
	if lastOpinionAt != nil && lastOpinionAt.After(lastArchivedAt) {
		return domain.PlanetHasNewReviews
	}
	return domain.PlanetConsulted
}

func (s *InsightService) dashboardRange(in DashboardInput) (domain.DashboardRange, *time.Location, error) {
	tz := in.TimeZone
	if tz == "" {
		tz = DefaultTimeZone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return domain.DashboardRange{}, nil, apperror.InvalidInput("unknown time zone " + tz)
	}
	today := s.now().In(loc)
	toDay := dateIn(today, loc)
	if in.To != nil {
		toDay = dateIn(*in.To, loc)
	}
	fromDay := toDay.AddDate(0, 0, -(defaultDashboardDays - 1))
	if in.From != nil {
		fromDay = dateIn(*in.From, loc)
	}
	to := toDay.AddDate(0, 0, 1) // inclusive end date
	if !fromDay.Before(to) {
		return domain.DashboardRange{}, nil, apperror.InvalidInput("from must not be after to")
	}
	if fromDay.AddDate(0, 0, maxDashboardDays).Before(to) {
		return domain.DashboardRange{}, nil, apperror.InvalidInput(fmt.Sprintf("the range must not exceed %d days", maxDashboardDays))
	}
	return domain.DashboardRange{From: fromDay, To: to, TimeZone: loc.String()}, loc, nil
}

// dateIn returns midnight of t's calendar date in loc (t's own Y-M-D is kept).
func dateIn(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// fillDays returns one point per day of the range, with zeros for days without data.
// Database buckets carry the local wall clock, so they are matched by calendar date.
func fillDays(points []domain.SentimentTrendPoint, rg domain.DashboardRange, loc *time.Location) []domain.SentimentTrendPoint {
	byDay := make(map[string]domain.SentimentBreakdown, len(points))
	for _, p := range points {
		byDay[p.Date.Format(dateLayout)] = p.Sentiment
	}
	var out []domain.SentimentTrendPoint
	for d := rg.From; d.Before(rg.To); d = d.AddDate(0, 0, 1) {
		out = append(out, domain.SentimentTrendPoint{Date: dateIn(d, loc), Sentiment: byDay[d.Format(dateLayout)]})
	}
	return out
}

// fillWeeks returns one bucket per Monday-based week overlapping the range.
func fillWeeks(buckets []domain.WeeklyReviewBucket, rg domain.DashboardRange, loc *time.Location) []domain.WeeklyReviewBucket {
	byWeek := make(map[string]domain.WeeklyReviewBucket, len(buckets))
	for _, b := range buckets {
		byWeek[b.WeekStart.Format(dateLayout)] = b
	}
	offset := (int(rg.From.Weekday()) + daysPerWeek - 1) % daysPerWeek // days since Monday
	var out []domain.WeeklyReviewBucket
	for w := rg.From.AddDate(0, 0, -offset); w.Before(rg.To); w = w.AddDate(0, 0, daysPerWeek) {
		b := byWeek[w.Format(dateLayout)]
		b.WeekStart = dateIn(w, loc)
		out = append(out, b)
	}
	return out
}
