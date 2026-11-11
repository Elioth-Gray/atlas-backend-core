package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// Sentiment of an opinion (sentiment_enum).
type Sentiment string

// Sentiments.
const (
	SentimentNegative Sentiment = "Negative"
	SentimentPositive Sentiment = "Positive"
	SentimentNeutral  Sentiment = "Neutral"
)

// Severity of an opinion (severity_enum). Only severe opinions are flagged; the column is
// NULL otherwise.
type Severity string

// SeveritySevere marks an opinion that needs urgent attention.
const SeveritySevere Severity = "Severe"

// Feedback is a customer review submitted through a shop's QR page.
type Feedback struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name      *string
	Rating    float64 `gorm:"type:numeric(2,1);not null"`
	Text      string  `gorm:"column:feedback;not null"`
	Image     *string
	ShopID    uuid.UUID `gorm:"type:uuid;not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	IsDeleted bool      `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (Feedback) TableName() string { return "feedbacks" }

// Issue is a cluster of similar opinions ("planet"). It has no shop_id: ownership is
// derived through feedback_opinions -> feedbacks.shop_id.
type Issue struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Title     string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	IsDeleted bool      `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (Issue) TableName() string { return "issues" }

// FeedbackOpinion is one aspect-level opinion extracted from a feedback.
type FeedbackOpinion struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	OpinionText  string    `gorm:"not null"`
	EvidenceText *string
	Sentiment    Sentiment  `gorm:"type:sentiment_enum;not null"`
	Severity     *Severity  `gorm:"type:severity_enum"`
	FeedbackID   uuid.UUID  `gorm:"type:uuid;not null"`
	IssueID      *uuid.UUID `gorm:"type:uuid"`
	CreatedAt    time.Time  `gorm:"not null"`
	UpdatedAt    time.Time  `gorm:"not null"`
	IsDeleted    bool       `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (FeedbackOpinion) TableName() string { return "feedback_opinions" }

// OpinionVector is the embedding of an opinion (pgvector).
type OpinionVector struct {
	ID                uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Embedding         pgvector.Vector `gorm:"type:vector;not null"`
	EmbeddingModel    string          `gorm:"not null"`
	FeedbackOpinionID uuid.UUID       `gorm:"type:uuid;not null"`
	CreatedAt         time.Time       `gorm:"not null"`
	UpdatedAt         time.Time       `gorm:"not null"`
	IsDeleted         bool            `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (OpinionVector) TableName() string { return "opinion_vectors" }

// JobStatus is the state of a FeedbackJob.
type JobStatus string

// Job states.
const (
	JobPending    JobStatus = "PENDING"
	JobProcessing JobStatus = "PROCESSING"
	JobDone       JobStatus = "DONE"
	JobFailed     JobStatus = "FAILED"
)

// FeedbackJob schedules the NLP/ML processing of one feedback (Path A).
type FeedbackJob struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	FeedbackID  uuid.UUID `gorm:"type:uuid;not null"`
	Status      JobStatus `gorm:"not null"`
	Attempts    int       `gorm:"not null"`
	NextRunAt   time.Time `gorm:"not null"`
	LockedUntil *time.Time
	LastError   *string
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

// TableName pins the table name.
func (FeedbackJob) TableName() string { return "feedback_jobs" }

// SentimentBreakdown counts opinions per sentiment.
type SentimentBreakdown struct {
	Negative int64 `json:"negative"`
	Positive int64 `json:"positive"`
	Neutral  int64 `json:"neutral"`
}

// SeverityBreakdown counts severe and not severe opinions.
type SeverityBreakdown struct {
	Severe    int64 `json:"severe"`
	NotSevere int64 `json:"not_severe"`
}

// IssueStats aggregates the opinions of an issue within a shop. Unique feedbacks and
// opinions are counted separately: one feedback can yield several opinions.
type IssueStats struct {
	UniqueFeedbackCount int64              `json:"unique_feedback_count"`
	OpinionCount        int64              `json:"opinion_count"`
	AverageRating       float64            `json:"average_rating"`
	FirstFeedbackAt     *time.Time         `json:"first_feedback_at"`
	LastFeedbackAt      *time.Time         `json:"last_feedback_at"`
	Sentiment           SentimentBreakdown `json:"sentiment"`
	Severity            SeverityBreakdown  `json:"severity"`
}

// EvidenceOpinion is an opinion together with its source feedback.
type EvidenceOpinion struct {
	OpinionID    uuid.UUID `json:"opinion_id"`
	OpinionText  string    `json:"opinion_text"`
	EvidenceText *string   `json:"evidence_text"`
	Sentiment    Sentiment `json:"sentiment"`
	Severity     *Severity `json:"severity"`
	FeedbackID   uuid.UUID `json:"feedback_id"`
	FullFeedback string    `json:"full_feedback"`
	Rating       float64   `json:"rating"`
	CreatedAt    time.Time `json:"created_at"`
}

// CandidateIssue is an existing issue of a shop, offered to the ML service for clustering.
type CandidateIssue struct {
	ID           uuid.UUID
	Title        string
	OpinionCount int64
}

// CandidateVector is the embedding of an existing opinion of a shop.
type CandidateVector struct {
	OpinionID      uuid.UUID
	IssueID        *uuid.UUID
	Embedding      pgvector.Vector
	EmbeddingModel string
}
