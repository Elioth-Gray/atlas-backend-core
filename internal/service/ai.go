package service

import (
	"context"

	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
)

// AIClient is the contract with the stateless Python AI service (atlas.ai.v1.AIService).
// Implementations translate to and from protobuf; every response is validated by the
// services before it is stored. Transient failures are returned as apperror.ErrUnavailable.
type AIClient interface {
	ProcessFeedback(ctx context.Context, req FeedbackAnalysisRequest) (FeedbackAnalysis, error)
	ProcessConsultationTurn(ctx context.Context, req ConsultationTurnRequest) (ConsultationTurnResult, error)
}

// FeedbackAnalysisRequest asks the AI to extract opinions from one feedback.
type FeedbackAnalysisRequest struct {
	Feedback         domain.Feedback
	Shop             domain.ShopProfile
	CandidateIssues  []domain.CandidateIssue
	CandidateVectors []domain.CandidateVector
}

// FeedbackAnalysis is the AI's answer for one feedback.
type FeedbackAnalysis struct {
	Opinions []ExtractedOpinion
}

// ExtractedOpinion is one opinion proposed by the AI. Sentiment is empty when the AI did
// not set one. ExistingIssueID is nil for a new issue (or an unparsable ID).
type ExtractedOpinion struct {
	OpinionText     string
	EvidenceText    string
	Sentiment       domain.Sentiment
	Severe          bool
	Embedding       []float32
	EmbeddingModel  string
	ExistingIssueID *uuid.UUID
	IssueTitle      string
}

// ConsultationTurnRequest carries the full state of a session for one turn.
type ConsultationTurnRequest struct {
	SessionID        uuid.UUID
	RequestID        uuid.UUID
	Event            domain.TurnEvent
	Snapshot         domain.ContextSnapshot
	Messages         []domain.ConsultationMessage
	AskedTopics      []string
	SkippedTopics    []string
	PreviousSolution *domain.SolutionData
	QuestionCount    int
}

// ConsultationTurnResult is either a question or a solution. Reference IDs the AI returned
// that are not valid UUIDs are mapped to uuid.Nil, so validation rejects them.
type ConsultationTurnResult struct {
	Question         *NextQuestion
	Solution         *domain.SolutionData
	MemoryCandidates []domain.MemoryCandidate
	ModelID          string
	PromptVersion    string
}

// NextQuestion asks the owner for more context.
type NextQuestion struct {
	TopicKey string
	Text     string
}
