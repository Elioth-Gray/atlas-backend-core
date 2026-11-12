package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/logger"
)

// Consultation limits.
const (
	// MaxOwnerMessageLength bounds the text of an owner turn (characters).
	MaxOwnerMessageLength = 4000
	// MaxConfirmedMemories bounds the memories confirmed in one save.
	MaxConfirmedMemories = 20
	// MaxMemoryFactLength bounds a business memory (characters).
	MaxMemoryFactLength = 1000
	// lockGrace is added to the turn timeout before a processing lock counts as stale.
	lockGrace = 30 * time.Second
)

// ConsultationRepository is the session/message persistence ConsultationService needs.
type ConsultationRepository interface {
	SessionLister
	CreateSession(ctx context.Context, s domain.ConsultationSession) (domain.ConsultationSession, error)
	DeleteEmptySession(ctx context.Context, id uuid.UUID) error
	GetSession(ctx context.Context, id uuid.UUID) (domain.ConsultationSession, error)
	FindSessionByProcessingRequest(ctx context.Context, requestID uuid.UUID) (*domain.ConsultationSession, error)
	FindSessionByAIRequest(ctx context.Context, requestID uuid.UUID) (*domain.ConsultationSession, error)
	LockSession(ctx context.Context, id uuid.UUID, stateVersion int64, requestID uuid.UUID, staleBefore time.Time) (bool, error)
	ReleaseLock(ctx context.Context, id, requestID uuid.UUID) error
	FinishTurn(ctx context.Context, id, requestID uuid.UUID, u domain.SessionTurnUpdate) (domain.ConsultationSession, error)
	Archive(ctx context.Context, id uuid.UUID, stateVersion int64, finalMessageID uuid.UUID, at time.Time) (domain.ConsultationSession, error)
	InsertMessages(ctx context.Context, msgs ...domain.ConsultationMessage) error
	ListMessages(ctx context.Context, sessionID uuid.UUID) ([]domain.ConsultationMessage, error)
	MaxRevision(ctx context.Context, sessionID uuid.UUID) (int, error)
	LatestArchivedSolution(ctx context.Context, shopID, issueID uuid.UUID) (*domain.PriorSolution, error)
}

// EvidenceReader reads the SQL evidence of an issue.
type EvidenceReader interface {
	ResolveIssueShop(ctx context.Context, issueID, userID uuid.UUID) (uuid.UUID, error)
	GetIssueSummary(ctx context.Context, shopID, issueID uuid.UUID) (domain.IssueSummary, error)
	RepresentativeEvidence(ctx context.Context, shopID, issueID uuid.UUID, limit int) ([]domain.EvidenceOpinion, error)
}

// MemoryRepository is the business memory persistence.
type MemoryRepository interface {
	ListActiveFacts(ctx context.Context, shopID uuid.UUID) ([]domain.MemoryFact, error)
	List(ctx context.Context, shopID uuid.UUID, active *bool, limit, offset int) ([]domain.BusinessMemory, int64, error)
	Get(ctx context.Context, id uuid.UUID) (domain.BusinessMemory, error)
	Create(ctx context.Context, memories []domain.BusinessMemory) ([]domain.BusinessMemory, error)
	Update(ctx context.Context, id uuid.UUID, c domain.MemoryChanges) (domain.BusinessMemory, error)
}

// ConsultationConfig tunes ConsultationService.
type ConsultationConfig struct {
	// AIEnabled is false when no AI service is configured; turns then answer 503.
	AIEnabled bool
	// TurnTimeout bounds one AI turn, including persistence.
	TurnTimeout time.Duration
	// EvidenceLimit is the number of representative opinions put in the snapshot.
	EvidenceLimit int
}

// SessionView is a session with its messages in conversation order. Replayed is true when
// the response comes from an earlier request with the same request_id.
type SessionView struct {
	Session  domain.ConsultationSession
	Messages []domain.ConsultationMessage
	Replayed bool
}

// TurnInput is one owner turn.
type TurnInput struct {
	RequestID    uuid.UUID
	StateVersion int64
	Event        domain.TurnEvent
	Content      string
}

// MemoryInput is a memory candidate the owner confirmed (possibly edited).
type MemoryInput struct {
	FactText              string
	SourceOwnerMessageIDs []uuid.UUID
}

// SaveInput archives a session.
type SaveInput struct {
	FinalSolutionMessageID uuid.UUID
	StateVersion           *int64
	ConfirmedMemories      []MemoryInput
}

// SaveResult is the archived session and the memories created.
type SaveResult struct {
	View     SessionView
	Memories []domain.BusinessMemory
}

// ConsultationService runs guided AI consultations (Path B).
type ConsultationService struct {
	sessions ConsultationRepository
	evidence EvidenceReader
	members  MembershipReader
	shops    ShopReader
	memories MemoryRepository
	ai       AIClient
	tx       TxRunner
	cfg      ConsultationConfig
	now      func() time.Time
}

// NewConsultationService returns a ConsultationService.
func NewConsultationService(sessions ConsultationRepository, evidence EvidenceReader, members MembershipReader,
	shops ShopReader, memories MemoryRepository, ai AIClient, tx TxRunner, cfg ConsultationConfig,
) *ConsultationService {
	return &ConsultationService{
		sessions: sessions, evidence: evidence, members: members, shops: shops, memories: memories,
		ai: ai, tx: tx, cfg: cfg, now: func() time.Time { return time.Now().UTC() },
	}
}

// Start opens a session on an issue: it captures the SQL evidence snapshot and runs the
// START turn. Repeating the call with the same requestID returns the same session.
func (s *ConsultationService) Start(ctx context.Context, userID, issueID, requestID uuid.UUID) (SessionView, error) {
	shopID, err := s.evidence.ResolveIssueShop(ctx, issueID, userID)
	if err != nil {
		return SessionView{}, err
	}
	existing, err := s.sessions.FindSessionByAIRequest(ctx, requestID)
	if err != nil {
		return SessionView{}, err
	}
	if existing != nil {
		if existing.IssueID != issueID || existing.StartedByUserID != userID {
			return SessionView{}, apperror.Conflict("request_id was already used for another request")
		}
		return s.view(ctx, *existing, true)
	}
	busy, err := s.sessions.FindSessionByProcessingRequest(ctx, requestID)
	if err != nil {
		return SessionView{}, err
	}
	if busy != nil {
		if s.isLocked(*busy) {
			return SessionView{}, apperror.Conflict("this request is still being processed")
		}
		// Clean up an abandoned session from a crashed START turn so the retry can proceed.
		s.release(ctx, busy.ID, requestID)
		if derr := s.sessions.DeleteEmptySession(ctx, busy.ID); derr != nil {
			logger.FromContext(ctx).WarnContext(ctx, "delete stale empty consultation session", slog.String("error", derr.Error()))
		}
	}
	if !s.cfg.AIEnabled {
		return SessionView{}, apperror.Unavailable("the AI consultation service is not available")
	}

	snapshot, err := s.buildSnapshot(ctx, shopID, issueID)
	if err != nil {
		return SessionView{}, fmt.Errorf("build context snapshot: %w", err)
	}
	now := s.now()
	sess, err := s.sessions.CreateSession(ctx, domain.ConsultationSession{
		ID:                  uuid.New(),
		ShopID:              shopID,
		IssueID:             issueID,
		StartedByUserID:     userID,
		Status:              domain.SessionCollectingContext,
		StateVersion:        0,
		AskedTopics:         domain.TextArray{},
		SkippedTopics:       domain.TextArray{},
		ContextSnapshot:     domain.NewJSONB(snapshot),
		ProcessingRequestID: &requestID,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		return SessionView{}, err
	}

	turnCtx, cancel := s.turnContext(ctx)
	defer cancel()
	view, err := s.runTurn(turnCtx, sess, domain.EventStart, requestID, nil, nil, sess.SkippedTopics)
	if err != nil {
		// Without its first AI message the session is useless; drop it so a retry starts
		// cleanly.
		if derr := s.sessions.DeleteEmptySession(turnCtx, sess.ID); derr != nil {
			logger.FromContext(ctx).WarnContext(ctx, "delete failed consultation session", slog.String("error", derr.Error()))
		}
		return SessionView{}, err
	}
	return view, nil
}

// Get returns a session with all its messages.
func (s *ConsultationService) Get(ctx context.Context, userID, sessionID uuid.UUID) (SessionView, error) {
	sess, err := s.authorizedSession(ctx, userID, sessionID)
	if err != nil {
		return SessionView{}, err
	}
	return s.view(ctx, sess, false)
}

// Turn runs one owner turn. It is idempotent per RequestID, and concurrent turns on the
// same session are rejected with Conflict through processing_request_id and
// state_version (optimistic locking).
func (s *ConsultationService) Turn(ctx context.Context, userID, sessionID uuid.UUID, in TurnInput) (SessionView, error) {
	sess, err := s.authorizedSession(ctx, userID, sessionID)
	if err != nil {
		return SessionView{}, err
	}
	content := strings.TrimSpace(in.Content)
	needsContent := in.Event == domain.EventOwnerReply || in.Event == domain.EventContinueDiscussion
	switch {
	case !slices.Contains(ownerEvents, in.Event):
		return SessionView{}, apperror.InvalidInput("unknown event " + string(in.Event))
	case needsContent && content == "":
		return SessionView{}, apperror.InvalidInput("content is required for " + string(in.Event))
	case len([]rune(content)) > MaxOwnerMessageLength:
		return SessionView{}, apperror.InvalidInput(fmt.Sprintf("content must be at most %d characters long", MaxOwnerMessageLength))
	}

	// 1. Idempotency: a request that already produced an AI message is answered from the DB.
	done, err := s.sessions.FindSessionByAIRequest(ctx, in.RequestID)
	if err != nil {
		return SessionView{}, err
	}
	if done != nil {
		if done.ID != sess.ID {
			return SessionView{}, apperror.Conflict("request_id was already used for another request")
		}
		return s.view(ctx, sess, true)
	}

	// 2. Optimistic locking.
	switch {
	case sess.Status == domain.SessionArchived:
		return SessionView{}, apperror.Conflict("the session is archived")
	case s.isLocked(sess):
		return SessionView{}, apperror.Conflict("the session is still processing a previous request")
	case in.StateVersion != sess.StateVersion:
		return SessionView{}, apperror.Conflict(fmt.Sprintf(
			"state_version %d is stale (current %d): reload the session", in.StateVersion, sess.StateVersion))
	case !eventAllowed(sess.Status, in.Event):
		return SessionView{}, apperror.Conflict(fmt.Sprintf("%s is not allowed while the session is %s", in.Event, sess.Status))
	case !s.cfg.AIEnabled:
		return SessionView{}, apperror.Unavailable("the AI consultation service is not available")
	}
	locked, err := s.sessions.LockSession(ctx, sess.ID, sess.StateVersion, in.RequestID, s.staleBefore())
	if err != nil {
		return SessionView{}, err
	}
	if !locked {
		return SessionView{}, apperror.Conflict("the session is processing another request or has changed: reload it")
	}

	// The turn now owns the session. It finishes even if the client disconnects, so a
	// retry with the same request_id receives the stored result instead of a second LLM call.
	turnCtx, cancel := s.turnContext(ctx)
	defer cancel()
	history, err := s.sessions.ListMessages(turnCtx, sess.ID)
	if err != nil {
		s.release(turnCtx, sess.ID, in.RequestID)
		return SessionView{}, err
	}
	// 3. The owner message gets its ID now so the AI can cite it; it is stored together
	// with the AI response, so a failed turn leaves no orphan message behind.
	var owner *domain.ConsultationMessage
	if needsContent {
		owner = &domain.ConsultationMessage{
			ID:               uuid.Must(uuid.NewV7()),
			SessionID:        sess.ID,
			Role:             domain.RoleOwner,
			MessageType:      domain.MessageText,
			Content:          &content,
			MemoryCandidates: domain.NewJSONB([]domain.MemoryCandidate{}),
			RequestID:        &in.RequestID,
			CreatedAt:        s.now(),
		}
	}
	skipped := slices.Clone(sess.SkippedTopics)
	if in.Event == domain.EventSkipQuestion {
		if topic := lastQuestionTopic(history); topic != "" && !slices.Contains(skipped, topic) {
			skipped = append(skipped, topic)
		}
	}
	return s.runTurn(turnCtx, sess, in.Event, in.RequestID, history, owner, skipped)
}
