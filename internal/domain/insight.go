package domain

import (
	"time"

	"github.com/google/uuid"
)

// PlanetStatus tells the owner whether an issue ("planet") still needs attention.
type PlanetStatus string

// Planet states.
const (
	// PlanetUnconsulted: no consultation on the issue was archived yet.
	PlanetUnconsulted PlanetStatus = "UNCONSULTED"
	// PlanetConsulted: archived consultation and no feedback since.
	PlanetConsulted PlanetStatus = "CONSULTED"
	// PlanetHasNewReviews: new feedback arrived after the last archived consultation.
	PlanetHasNewReviews PlanetStatus = "HAS_NEW_REVIEWS"
)

// IssueSummary is an issue with its statistics within one shop.
type IssueSummary struct {
	Issue Issue
	Stats IssueStats
	// LastOpinionAt is when the newest opinion joined the issue (feedbacks are clustered
	// asynchronously, so this can be later than the feedback itself).
	LastOpinionAt   *time.Time
	LastConsultedAt *time.Time
	PlanetStatus    PlanetStatus
}

// DashboardKPI holds the headline numbers of the dashboard.
type DashboardKPI struct {
	TotalFeedbacks int64
	AverageRating  float64
	TotalOpinions  int64
	Sentiment      SentimentBreakdown
	SevereOpinions int64
	ActiveIssues   int64
}

// SentimentTrendPoint counts opinions per sentiment on one day.
type SentimentTrendPoint struct {
	Date      time.Time
	Sentiment SentimentBreakdown
}

// WeeklyReviewBucket counts feedbacks in one week (weeks start on Monday).
type WeeklyReviewBucket struct {
	WeekStart     time.Time
	FeedbackCount int64
	AverageRating float64
}

// TopComplaint is an issue ranked by its negative opinions.
type TopComplaint struct {
	IssueID          uuid.UUID
	Title            string
	NegativeOpinions int64
	UniqueFeedbacks  int64
	SevereOpinions   int64
}

// DashboardRange is a half-open time range [From, To) in a time zone.
type DashboardRange struct {
	From     time.Time
	To       time.Time
	TimeZone string
}

// Dashboard is the shop overview.
type Dashboard struct {
	Range           DashboardRange
	KPI             DashboardKPI
	SentimentTrend  []SentimentTrendPoint
	WeeklyReviews   []WeeklyReviewBucket
	TopComplaints   []TopComplaint
	RecentFeedbacks []Feedback
}
