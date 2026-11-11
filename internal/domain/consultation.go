package domain

import (
	"time"

	"github.com/google/uuid"
)

// SessionStatus is the state of a consultation session.
type SessionStatus string

// Session states: COLLECTING_CONTEXT -> SOLUTION_READY -> ARCHIVED. A CONTINUE_DISCUSSION
// turn may move SOLUTION_READY back to COLLECTING_CONTEXT when the AI asks a question.
const (
	SessionCollectingContext SessionStatus = "COLLECTING_CONTEXT"
	SessionSolutionReady     SessionStatus = "SOLUTION_READY"
	SessionArchived          SessionStatus = "ARCHIVED"
)

// MessageRole is the author of a consultation message.
type MessageRole string

// Message roles.
const (
	RoleOwner   MessageRole = "OWNER"
	RoleAtlasAI MessageRole = "ATLAS_AI"
)

// MessageType distinguishes text messages from structured solutions.
type MessageType string

// Message types.
const (
	MessageText     MessageType = "TEXT"
	MessageSolution MessageType = "SOLUTION"
)

// TurnEvent is what triggers a consultation turn.
type TurnEvent string

// Turn events. START is internal (session creation); the others come from the owner.
const (
	EventStart              TurnEvent = "START"
	EventOwnerReply         TurnEvent = "OWNER_REPLY"
	EventSkipQuestion       TurnEvent = "SKIP_QUESTION"
	EventGenerateNow        TurnEvent = "GENERATE_NOW"
	EventContinueDiscussion TurnEvent = "CONTINUE_DISCUSSION"
)

// ConsultationSession is one guided AI consultation on an issue.
type ConsultationSession struct {
	ID                     uuid.UUID              `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ShopID                 uuid.UUID              `gorm:"type:uuid;not null"`
	IssueID                uuid.UUID              `gorm:"type:uuid;not null"`
	StartedByUserID        uuid.UUID              `gorm:"type:uuid;not null"`
	Status                 SessionStatus          `gorm:"not null"`
	StateVersion           int64                  `gorm:"not null"`
	QuestionCount          int16                  `gorm:"not null"`
	AskedTopics            TextArray              `gorm:"type:text[];not null"`
	SkippedTopics          TextArray              `gorm:"type:text[];not null"`
	ContextSnapshot        JSONB[ContextSnapshot] `gorm:"type:jsonb;not null"`
	ProcessingRequestID    *uuid.UUID             `gorm:"type:uuid"`
	FinalSolutionMessageID *uuid.UUID             `gorm:"type:uuid"`
	CreatedAt              time.Time              `gorm:"not null"`
	UpdatedAt              time.Time              `gorm:"not null"`
	ArchivedAt             *time.Time
}

// TableName pins the table name.
func (ConsultationSession) TableName() string { return "consultation_sessions" }

// SessionTurnUpdate is the state a session moves to after a turn.
type SessionTurnUpdate struct {
	Status        SessionStatus
	QuestionCount int16
	AskedTopics   TextArray
	SkippedTopics TextArray
}

// ConsultationMessage is one message of a session: an owner text, an AI question, or an
// AI solution.
type ConsultationMessage struct {
	ID               uuid.UUID               `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	SessionID        uuid.UUID               `gorm:"type:uuid;not null"`
	Role             MessageRole             `gorm:"not null"`
	MessageType      MessageType             `gorm:"not null"`
	Content          *string                 `gorm:"type:text"`
	SolutionData     NullJSONB[SolutionData] `gorm:"type:jsonb"`
	Revision         *int
	TopicKey         *string
	ModelID          *string
	PromptVersion    *string
	MemoryCandidates JSONB[[]MemoryCandidate] `gorm:"type:jsonb;not null"`
	RequestID        *uuid.UUID               `gorm:"type:uuid"`
	CreatedAt        time.Time                `gorm:"not null"`
}

// TableName pins the table name.
func (ConsultationMessage) TableName() string { return "consultation_messages" }

// BusinessMemory is a fact about the business confirmed by the owner, reused as context in
// later consultations.
type BusinessMemory struct {
	ID                    uuid.UUID          `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ShopID                uuid.UUID          `gorm:"type:uuid;not null"`
	FactText              string             `gorm:"not null"`
	SourceSessionID       uuid.UUID          `gorm:"type:uuid;not null"`
	SourceOwnerMessageIDs JSONB[[]uuid.UUID] `gorm:"type:jsonb;not null"`
	ConfirmedByUserID     uuid.UUID          `gorm:"type:uuid;not null"`
	ConfirmedAt           time.Time          `gorm:"not null"`
	IsActive              bool               `gorm:"not null;default:true"`
	CreatedAt             time.Time          `gorm:"not null"`
	UpdatedAt             time.Time          `gorm:"not null"`
}

// TableName pins the table name.
func (BusinessMemory) TableName() string { return "business_memories" }

// MemoryChanges is a partial update to a BusinessMemory. Nil fields are left unchanged.
type MemoryChanges struct {
	FactText *string
	IsActive *bool
}

// ContextSnapshot is the static SQL evidence captured when a session starts and stored in
// consultation_sessions.context_snapshot. AI solutions may only cite IDs found here (or
// OWNER messages of the session).
type ContextSnapshot struct {
	Issue              SnapshotIssue     `json:"issue"`
	Period             SnapshotPeriod    `json:"period"`
	Stats              IssueStats        `json:"stats"`
	Evidence           []EvidenceOpinion `json:"evidence"`
	Shop               ShopProfile       `json:"shop"`
	Memories           []MemoryFact      `json:"memories"`
	PriorIssueSolution *PriorSolution    `json:"prior_issue_solution"`
	CapturedAt         time.Time         `json:"captured_at"`
}

// SnapshotIssue identifies the consulted issue.
type SnapshotIssue struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
}

// SnapshotPeriod is the time span covered by the evidence.
type SnapshotPeriod struct {
	From *time.Time `json:"from"`
	To   *time.Time `json:"to"`
}

// ShopProfile describes a shop for AI context.
type ShopProfile struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Scale       string    `json:"scale"`
}

// MemoryFact is an active business memory.
type MemoryFact struct {
	ID       uuid.UUID `json:"id"`
	FactText string    `json:"fact_text"`
}

// PriorSolution is the final solution of the latest archived session on the same issue.
type PriorSolution struct {
	SessionID  uuid.UUID    `json:"session_id"`
	MessageID  uuid.UUID    `json:"message_id"`
	ArchivedAt time.Time    `json:"archived_at"`
	Solution   SolutionData `json:"solution"`
}

// MemoryCandidate is a business fact proposed by the AI for the owner to confirm.
type MemoryCandidate struct {
	FactText              string      `json:"fact_text"`
	SourceOwnerMessageIDs []uuid.UUID `json:"source_owner_message_ids"`
}

// References cite the sources of a solution statement.
type References struct {
	OpinionIDs      []uuid.UUID `json:"opinion_ids"`
	OwnerMessageIDs []uuid.UUID `json:"owner_message_ids"`
	MemoryIDs       []uuid.UUID `json:"memory_ids"`
}

// SolutionData is the five-component structured solution.
type SolutionData struct {
	Findings          []SolutionFinding        `json:"findings"`
	CustomerEvidence  []SolutionEvidence       `json:"customer_evidence"`
	Hypotheses        []SolutionHypothesis     `json:"hypotheses"`
	Recommendations   []SolutionRecommendation `json:"recommendations"`
	SuccessIndicators []SolutionIndicator      `json:"success_indicators"`
}

// SolutionFinding is an observation grounded in evidence.
type SolutionFinding struct {
	Statement  string     `json:"statement"`
	References References `json:"references"`
}

// SolutionEvidence quotes one evidence opinion.
type SolutionEvidence struct {
	OpinionID uuid.UUID `json:"opinion_id"`
	Quote     string    `json:"quote"`
	Note      string    `json:"note"`
}

// SolutionHypothesis is a likely root cause.
type SolutionHypothesis struct {
	Statement  string     `json:"statement"`
	Confidence string     `json:"confidence"`
	References References `json:"references"`
}

// SolutionRecommendation is an action for the owner.
type SolutionRecommendation struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	References  References `json:"references"`
}

// SolutionIndicator is a measurable success target.
type SolutionIndicator struct {
	Metric    string `json:"metric"`
	Target    string `json:"target"`
	Timeframe string `json:"timeframe"`
}
