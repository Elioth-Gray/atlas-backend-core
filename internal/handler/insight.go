package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

const dateLayout = "2006-01-02"

// InsightService is the business logic InsightHandler depends on.
type InsightService interface {
	Dashboard(ctx context.Context, userID, shopID uuid.UUID, in service.DashboardInput) (domain.Dashboard, error)
	ListIssues(ctx context.Context, userID, shopID uuid.UUID, params service.PageParams) (service.Page[domain.IssueSummary], error)
	GetIssue(ctx context.Context, userID, issueID uuid.UUID) (service.IssueDetail, error)
}

// InsightHandler serves the dashboard and the issue galaxy.
type InsightHandler struct {
	svc InsightService
}

// NewInsightHandler returns an InsightHandler.
func NewInsightHandler(svc InsightService) *InsightHandler { return &InsightHandler{svc: svc} }

// dashboardQuery is the query string of GET /owner/shops/:shopId/dashboard. Dates are
// inclusive calendar days in tz (default Asia/Jakarta); the default range is the last 30
// days.
type dashboardQuery struct {
	From     string `form:"from" binding:"omitempty,datetime=2006-01-02"`
	To       string `form:"to" binding:"omitempty,datetime=2006-01-02"`
	TimeZone string `form:"tz" binding:"omitempty,max=64"`
}

// Dashboard handles GET /api/v1/owner/shops/:shopId/dashboard.
func (h *InsightHandler) Dashboard(c *gin.Context) {
	userID, shopID, ok := userAndUUID(c, "shopId")
	if !ok {
		return
	}
	var q dashboardQuery
	if err := bindQuery(c, &q); err != nil {
		handleError(c, err)
		return
	}
	d, err := h.svc.Dashboard(c.Request.Context(), userID, shopID, service.DashboardInput{
		From: parseDate(q.From), To: parseDate(q.To), TimeZone: q.TimeZone,
	})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[dashboardResponse]{Data: newDashboardResponse(d)})
}

// ListIssues handles GET /api/v1/owner/shops/:shopId/issues.
func (h *InsightHandler) ListIssues(c *gin.Context) {
	userID, shopID, ok := userAndUUID(c, "shopId")
	if !ok {
		return
	}
	var q pageQuery
	if err := bindQuery(c, &q); err != nil {
		handleError(c, err)
		return
	}
	page, err := h.svc.ListIssues(c.Request.Context(), userID, shopID, q.params())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]issueSummaryResponse, 0, len(page.Items))
	for _, it := range page.Items {
		out = append(out, newIssueSummaryResponse(it))
	}
	c.JSON(http.StatusOK, httpx.ListResponse[issueSummaryResponse]{Data: out, Meta: httpx.NewPageMeta(page.Page, page.PageSize, page.Total)})
}

// GetIssue handles GET /api/v1/owner/issues/:issueId.
func (h *InsightHandler) GetIssue(c *gin.Context) {
	userID, issueID, ok := userAndUUID(c, "issueId")
	if !ok {
		return
	}
	d, err := h.svc.GetIssue(c.Request.Context(), userID, issueID)
	if err != nil {
		handleError(c, err)
		return
	}
	out := issueDetailResponse{
		ShopID:        d.ShopID.String(),
		Issue:         newIssueSummaryResponse(d.Summary),
		Evidence:      make([]evidenceResponse, 0, len(d.Evidence)),
		Consultations: make([]sessionSummaryResponse, 0, len(d.Sessions)),
	}
	for _, e := range d.Evidence {
		out.Evidence = append(out.Evidence, newEvidenceResponse(e))
	}
	for _, s := range d.Sessions {
		out.Consultations = append(out.Consultations, newSessionSummaryResponse(s))
	}
	c.JSON(http.StatusOK, httpx.DataResponse[issueDetailResponse]{Data: out})
}

func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return nil // already rejected by the datetime binding rule
	}
	return &t
}

// --- DTOs ------------------------------------------------------------------------------

type sentimentResponse struct {
	Negative int64 `json:"negative"`
	Positive int64 `json:"positive"`
	Neutral  int64 `json:"neutral"`
}

func newSentimentResponse(s domain.SentimentBreakdown) sentimentResponse {
	return sentimentResponse{Negative: s.Negative, Positive: s.Positive, Neutral: s.Neutral}
}

type severityResponse struct {
	Severe    int64 `json:"severe"`
	NotSevere int64 `json:"not_severe"`
}

type issueStatsResponse struct {
	UniqueFeedbackCount int64             `json:"unique_feedback_count"`
	OpinionCount        int64             `json:"opinion_count"`
	AverageRating       float64           `json:"average_rating"`
	FirstFeedbackAt     *time.Time        `json:"first_feedback_at"`
	LastFeedbackAt      *time.Time        `json:"last_feedback_at"`
	Sentiment           sentimentResponse `json:"sentiment"`
	Severity            severityResponse  `json:"severity"`
}

// issueSummaryResponse is an issue ("planet") with its statistics.
type issueSummaryResponse struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	Stats           issueStatsResponse `json:"stats"`
	PlanetStatus    string             `json:"planet_status"`
	LastConsultedAt *time.Time         `json:"last_consulted_at"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

func newIssueSummaryResponse(s domain.IssueSummary) issueSummaryResponse {
	st := s.Stats
	return issueSummaryResponse{
		ID:    s.Issue.ID.String(),
		Title: s.Issue.Title,
		Stats: issueStatsResponse{
			UniqueFeedbackCount: st.UniqueFeedbackCount,
			OpinionCount:        st.OpinionCount,
			AverageRating:       st.AverageRating,
			FirstFeedbackAt:     st.FirstFeedbackAt,
			LastFeedbackAt:      st.LastFeedbackAt,
			Sentiment:           newSentimentResponse(st.Sentiment),
			Severity:            severityResponse{Severe: st.Severity.Severe, NotSevere: st.Severity.NotSevere},
		},
		PlanetStatus:    string(s.PlanetStatus),
		LastConsultedAt: s.LastConsultedAt,
		CreatedAt:       s.Issue.CreatedAt,
		UpdatedAt:       s.Issue.UpdatedAt,
	}
}

// evidenceResponse is one opinion with its source feedback.
type evidenceResponse struct {
	OpinionID    string    `json:"opinion_id"`
	OpinionText  string    `json:"opinion_text"`
	EvidenceText *string   `json:"evidence_text"`
	Sentiment    string    `json:"sentiment"`
	Severity     *string   `json:"severity"`
	FeedbackID   string    `json:"feedback_id"`
	Feedback     string    `json:"feedback"`
	Rating       float64   `json:"rating"`
	CreatedAt    time.Time `json:"created_at"`
}

func newEvidenceResponse(e domain.EvidenceOpinion) evidenceResponse {
	out := evidenceResponse{
		OpinionID:    e.OpinionID.String(),
		OpinionText:  e.OpinionText,
		EvidenceText: e.EvidenceText,
		Sentiment:    string(e.Sentiment),
		FeedbackID:   e.FeedbackID.String(),
		Feedback:     e.FullFeedback,
		Rating:       e.Rating,
		CreatedAt:    e.CreatedAt,
	}
	if e.Severity != nil {
		sev := string(*e.Severity)
		out.Severity = &sev
	}
	return out
}

// sessionSummaryResponse is a consultation in an issue's history.
type sessionSummaryResponse struct {
	ID                     string     `json:"id"`
	Status                 string     `json:"status"`
	StateVersion           int64      `json:"state_version"`
	QuestionCount          int16      `json:"question_count"`
	StartedByUserID        string     `json:"started_by_user_id"`
	FinalSolutionMessageID *string    `json:"final_solution_message_id"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	ArchivedAt             *time.Time `json:"archived_at"`
}

func newSessionSummaryResponse(s domain.ConsultationSession) sessionSummaryResponse {
	return sessionSummaryResponse{
		ID:                     s.ID.String(),
		Status:                 string(s.Status),
		StateVersion:           s.StateVersion,
		QuestionCount:          s.QuestionCount,
		StartedByUserID:        s.StartedByUserID.String(),
		FinalSolutionMessageID: optionalUUID(s.FinalSolutionMessageID),
		CreatedAt:              s.CreatedAt,
		UpdatedAt:              s.UpdatedAt,
		ArchivedAt:             s.ArchivedAt,
	}
}

// issueDetailResponse is GET /owner/issues/:issueId.
type issueDetailResponse struct {
	ShopID        string                   `json:"shop_id"`
	Issue         issueSummaryResponse     `json:"issue"`
	Evidence      []evidenceResponse       `json:"evidence"`
	Consultations []sessionSummaryResponse `json:"consultations"`
}

type rangeResponse struct {
	From     string `json:"from"`
	To       string `json:"to"`
	TimeZone string `json:"time_zone"`
}

type kpiResponse struct {
	TotalFeedbacks int64             `json:"total_feedbacks"`
	AverageRating  float64           `json:"average_rating"`
	TotalOpinions  int64             `json:"total_opinions"`
	Sentiment      sentimentResponse `json:"sentiment"`
	SevereOpinions int64             `json:"severe_opinions"`
	ActiveIssues   int64             `json:"active_issues"`
}

type trendPointResponse struct {
	Date string `json:"date"`
	sentimentResponse
}

type weeklyResponse struct {
	WeekStart     string  `json:"week_start"`
	FeedbackCount int64   `json:"feedback_count"`
	AverageRating float64 `json:"average_rating"`
}

type topComplaintResponse struct {
	IssueID          string `json:"issue_id"`
	Title            string `json:"title"`
	NegativeOpinions int64  `json:"negative_opinions"`
	UniqueFeedbacks  int64  `json:"unique_feedbacks"`
	SevereOpinions   int64  `json:"severe_opinions"`
}

type feedbackResponse struct {
	ID        string    `json:"id"`
	Name      *string   `json:"name"`
	Rating    float64   `json:"rating"`
	Feedback  string    `json:"feedback"`
	Image     *string   `json:"image"`
	CreatedAt time.Time `json:"created_at"`
}

// dashboardResponse is GET /owner/shops/:shopId/dashboard.
type dashboardResponse struct {
	Range           rangeResponse          `json:"range"`
	KPI             kpiResponse            `json:"kpi"`
	SentimentTrend  []trendPointResponse   `json:"sentiment_trend"`
	WeeklyReviews   []weeklyResponse       `json:"weekly_reviews"`
	TopComplaints   []topComplaintResponse `json:"top_complaints"`
	RecentFeedbacks []feedbackResponse     `json:"recent_feedbacks"`
}

func newDashboardResponse(d domain.Dashboard) dashboardResponse {
	out := dashboardResponse{
		Range: rangeResponse{
			From:     d.Range.From.Format(dateLayout),
			To:       d.Range.To.AddDate(0, 0, -1).Format(dateLayout),
			TimeZone: d.Range.TimeZone,
		},
		KPI: kpiResponse{
			TotalFeedbacks: d.KPI.TotalFeedbacks,
			AverageRating:  d.KPI.AverageRating,
			TotalOpinions:  d.KPI.TotalOpinions,
			Sentiment:      newSentimentResponse(d.KPI.Sentiment),
			SevereOpinions: d.KPI.SevereOpinions,
			ActiveIssues:   d.KPI.ActiveIssues,
		},
		SentimentTrend:  make([]trendPointResponse, 0, len(d.SentimentTrend)),
		WeeklyReviews:   make([]weeklyResponse, 0, len(d.WeeklyReviews)),
		TopComplaints:   make([]topComplaintResponse, 0, len(d.TopComplaints)),
		RecentFeedbacks: make([]feedbackResponse, 0, len(d.RecentFeedbacks)),
	}
	for _, p := range d.SentimentTrend {
		out.SentimentTrend = append(out.SentimentTrend, trendPointResponse{Date: p.Date.Format(dateLayout), sentimentResponse: newSentimentResponse(p.Sentiment)})
	}
	for _, w := range d.WeeklyReviews {
		out.WeeklyReviews = append(out.WeeklyReviews, weeklyResponse{WeekStart: w.WeekStart.Format(dateLayout), FeedbackCount: w.FeedbackCount, AverageRating: w.AverageRating})
	}
	for _, t := range d.TopComplaints {
		out.TopComplaints = append(out.TopComplaints, topComplaintResponse{
			IssueID: t.IssueID.String(), Title: t.Title, NegativeOpinions: t.NegativeOpinions,
			UniqueFeedbacks: t.UniqueFeedbacks, SevereOpinions: t.SevereOpinions,
		})
	}
	for _, f := range d.RecentFeedbacks {
		out.RecentFeedbacks = append(out.RecentFeedbacks, feedbackResponse{
			ID: f.ID.String(), Name: f.Name, Rating: f.Rating, Feedback: f.Text, Image: f.Image, CreatedAt: f.CreatedAt,
		})
	}
	return out
}

func optionalUUID(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
