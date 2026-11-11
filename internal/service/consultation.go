package service

import (
	"context"
	"errors"
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
		return SessionView{}, apperror.Conflict("this request is still being processed")
	}
	if !s.cfg.AIEnabled {
		return SessionView{}, apperror.Unavailable("the AI consultation service is not available")
	}

	snapshot, err := s.buildSnapshot(ctx, shopID, issueID)
	if err != nil {
		return SessionView{}, fmt.Errorf("build context snapshot: %w", err)
	}
	sess, err := s.sessions.CreateSession(ctx, domain.ConsultationSession{
		ID:                  uuid.New(),
		ShopID:              shopID,
		IssueID:             issueID,
		StartedByUserID:     userID,
		Status:              domain.SessionCollectingContext,
		StateVersion:        1,
		AskedTopics:         domain.TextArray{},
		SkippedTopics:       domain.TextArray{},
		ContextSnapshot:     domain.NewJSONB(snapshot),
		ProcessingRequestID: &requestID,
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

// Save archives a SOLUTION_READY session with the chosen final solution and stores the
// business memories the owner confirmed. It never calls the AI.
func (s *ConsultationService) Save(ctx context.Context, userID, sessionID uuid.UUID, in SaveInput) (SaveResult, error) {
	sess, err := s.authorizedSession(ctx, userID, sessionID)
	if err != nil {
		return SaveResult{}, err
	}
	switch {
	case sess.Status == domain.SessionArchived:
		return SaveResult{}, apperror.Conflict("the session is already archived")
	case sess.Status != domain.SessionSolutionReady:
		return SaveResult{}, apperror.Conflict("the session has no solution to save yet")
	case s.isLocked(sess):
		return SaveResult{}, apperror.Conflict("the session is still processing a previous request")
	case in.StateVersion != nil && *in.StateVersion != sess.StateVersion:
		return SaveResult{}, apperror.Conflict(fmt.Sprintf(
			"state_version %d is stale (current %d): reload the session", *in.StateVersion, sess.StateVersion))
	case len(in.ConfirmedMemories) > MaxConfirmedMemories:
		return SaveResult{}, apperror.InvalidInput(fmt.Sprintf("at most %d memories can be confirmed at once", MaxConfirmedMemories))
	}

	msgs, err := s.sessions.ListMessages(ctx, sess.ID)
	if err != nil {
		return SaveResult{}, err
	}
	if !slices.ContainsFunc(msgs, func(m domain.ConsultationMessage) bool {
		return m.ID == in.FinalSolutionMessageID && m.MessageType == domain.MessageSolution
	}) {
		return SaveResult{}, apperror.InvalidInput("final_solution_message_id must reference a solution message of this session")
	}
	owners := ownerMessageIDs(msgs)
	now := s.now()
	memories := make([]domain.BusinessMemory, 0, len(in.ConfirmedMemories))
	for i, m := range in.ConfirmedMemories {
		mem, err := newMemory(i, m, owners)
		if err != nil {
			return SaveResult{}, err
		}
		mem.ShopID, mem.SourceSessionID, mem.ConfirmedByUserID, mem.ConfirmedAt = sess.ShopID, sess.ID, userID, now
		memories = append(memories, mem)
	}

	var res SaveResult
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		archived, err := s.sessions.Archive(ctx, sess.ID, sess.StateVersion, in.FinalSolutionMessageID, now)
		if err != nil {
			return err
		}
		created, err := s.memories.Create(ctx, memories)
		if err != nil {
			return err
		}
		res = SaveResult{View: SessionView{Session: archived, Messages: msgs}, Memories: created}
		return nil
	})
	if err != nil {
		return SaveResult{}, fmt.Errorf("save consultation %s: %w", sess.ID, err)
	}
	return res, nil
}

// runTurn calls the AI for a session locked by requestID, validates the answer, and stores
// it together with the owner message (if any) while moving the session to its next state.
// On failure the lock is released and the state stays unchanged.
func (s *ConsultationService) runTurn(ctx context.Context, sess domain.ConsultationSession, event domain.TurnEvent,
	requestID uuid.UUID, history []domain.ConsultationMessage, owner *domain.ConsultationMessage, skipped domain.TextArray,
) (SessionView, error) {
	msgs := slices.Clone(history)
	if owner != nil {
		msgs = append(msgs, *owner)
	}
	snapshot := sess.ContextSnapshot.V
	res, err := s.ai.ProcessConsultationTurn(ctx, ConsultationTurnRequest{
		SessionID:        sess.ID,
		RequestID:        requestID,
		Event:            event,
		Snapshot:         snapshot,
		Messages:         msgs,
		AskedTopics:      sess.AskedTopics,
		SkippedTopics:    skipped,
		PreviousSolution: latestSolution(history),
		QuestionCount:    int(sess.QuestionCount),
	})
	if err != nil {
		s.release(ctx, sess.ID, requestID)
		return SessionView{}, fmt.Errorf("ai consultation turn: %w", err)
	}
	res, err = validateTurnResult(res, newTurnRefs(snapshot, msgs))
	if err != nil {
		s.release(ctx, sess.ID, requestID)
		logger.FromContext(ctx).WarnContext(ctx, "rejected AI consultation response",
			slog.String("session_id", sess.ID.String()), slog.String("error", err.Error()))
		return SessionView{}, apperror.Wrap(apperror.ErrUnavailable, "the AI returned an invalid response, please try again", err)
	}

	ai := domain.ConsultationMessage{
		ID:               uuid.Must(uuid.NewV7()),
		SessionID:        sess.ID,
		Role:             domain.RoleAtlasAI,
		ModelID:          optionalText(&res.ModelID),
		PromptVersion:    optionalText(&res.PromptVersion),
		MemoryCandidates: domain.NewJSONB(res.MemoryCandidates),
		RequestID:        &requestID,
		CreatedAt:        s.now(),
	}
	update := domain.SessionTurnUpdate{
		QuestionCount: sess.QuestionCount,
		AskedTopics:   slices.Clone(sess.AskedTopics),
		SkippedTopics: skipped,
	}
	var updated domain.ConsultationSession
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if q := res.Question; q != nil {
			ai.MessageType, ai.Content, ai.TopicKey = domain.MessageText, &q.Text, &q.TopicKey
			update.Status = domain.SessionCollectingContext
			update.QuestionCount++
			if !slices.Contains(update.AskedTopics, q.TopicKey) {
				update.AskedTopics = append(update.AskedTopics, q.TopicKey)
			}
		} else {
			rev, err := s.sessions.MaxRevision(ctx, sess.ID)
			if err != nil {
				return err
			}
			rev++
			ai.MessageType, ai.Revision = domain.MessageSolution, &rev
			ai.SolutionData = domain.NullJSONB[domain.SolutionData]{V: *res.Solution, Valid: true}
			update.Status = domain.SessionSolutionReady
		}
		toInsert := []domain.ConsultationMessage{ai}
		if owner != nil {
			toInsert = []domain.ConsultationMessage{*owner, ai}
		}
		if err := s.sessions.InsertMessages(ctx, toInsert...); err != nil {
			return err
		}
		var err error
		updated, err = s.sessions.FinishTurn(ctx, sess.ID, requestID, update)
		return err
	})
	if err != nil {
		s.release(ctx, sess.ID, requestID)
		return SessionView{}, fmt.Errorf("store consultation turn: %w", err)
	}
	return SessionView{Session: updated, Messages: append(msgs, ai)}, nil
}

// buildSnapshot gathers the SQL evidence for a new session.
func (s *ConsultationService) buildSnapshot(ctx context.Context, shopID, issueID uuid.UUID) (domain.ContextSnapshot, error) {
	summary, err := s.evidence.GetIssueSummary(ctx, shopID, issueID)
	if err != nil {
		return domain.ContextSnapshot{}, err
	}
	evidence, err := s.evidence.RepresentativeEvidence(ctx, shopID, issueID, s.cfg.EvidenceLimit)
	if err != nil {
		return domain.ContextSnapshot{}, err
	}
	shop, err := s.shops.GetActive(ctx, shopID)
	if err != nil {
		return domain.ContextSnapshot{}, err
	}
	memories, err := s.memories.ListActiveFacts(ctx, shopID)
	if err != nil {
		return domain.ContextSnapshot{}, err
	}
	prior, err := s.sessions.LatestArchivedSolution(ctx, shopID, issueID)
	if err != nil {
		return domain.ContextSnapshot{}, err
	}
	if evidence == nil {
		evidence = []domain.EvidenceOpinion{}
	}
	if memories == nil {
		memories = []domain.MemoryFact{}
	}
	return domain.ContextSnapshot{
		Issue:              domain.SnapshotIssue{ID: summary.Issue.ID, Title: summary.Issue.Title},
		Period:             domain.SnapshotPeriod{From: summary.Stats.FirstFeedbackAt, To: summary.Stats.LastFeedbackAt},
		Stats:              summary.Stats,
		Evidence:           evidence,
		Shop:               shopProfile(shop),
		Memories:           memories,
		PriorIssueSolution: prior,
		CapturedAt:         s.now(),
	}, nil
}

// authorizedSession loads a session the user may access. Sessions of other tenants are
// reported as not found.
func (s *ConsultationService) authorizedSession(ctx context.Context, userID, sessionID uuid.UUID) (domain.ConsultationSession, error) {
	sess, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return domain.ConsultationSession{}, err
	}
	if _, err := requireMember(ctx, s.members, userID, sess.ShopID); err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return domain.ConsultationSession{}, apperror.NotFound("consultation session not found")
		}
		return domain.ConsultationSession{}, err
	}
	return sess, nil
}

func (s *ConsultationService) view(ctx context.Context, sess domain.ConsultationSession, replayed bool) (SessionView, error) {
	msgs, err := s.sessions.ListMessages(ctx, sess.ID)
	if err != nil {
		return SessionView{}, err
	}
	return SessionView{Session: sess, Messages: msgs, Replayed: replayed}, nil
}

func (s *ConsultationService) turnContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), s.cfg.TurnTimeout)
}

// staleBefore is the update time before which a processing lock is considered abandoned.
func (s *ConsultationService) staleBefore() time.Time {
	return s.now().Add(-(s.cfg.TurnTimeout + lockGrace))
}

func (s *ConsultationService) isLocked(sess domain.ConsultationSession) bool {
	return sess.ProcessingRequestID != nil && sess.UpdatedAt.After(s.staleBefore())
}

func (s *ConsultationService) release(ctx context.Context, sessionID, requestID uuid.UUID) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.sessions.ReleaseLock(releaseCtx, sessionID, requestID); err != nil {
		logger.FromContext(ctx).ErrorContext(ctx, "release consultation lock",
			slog.String("session_id", sessionID.String()), slog.String("error", err.Error()))
	}
}

// ownerEvents are the events an owner may send.
var ownerEvents = []domain.TurnEvent{
	domain.EventOwnerReply, domain.EventSkipQuestion, domain.EventGenerateNow, domain.EventContinueDiscussion,
}

// eventAllowed is the session state machine: questions are answered, skipped, or cut short
// while collecting context; a ready solution can be discussed further or regenerated.
func eventAllowed(status domain.SessionStatus, event domain.TurnEvent) bool {
	switch status {
	case domain.SessionCollectingContext:
		return event == domain.EventOwnerReply || event == domain.EventSkipQuestion || event == domain.EventGenerateNow
	case domain.SessionSolutionReady:
		return event == domain.EventContinueDiscussion || event == domain.EventGenerateNow
	default:
		return false
	}
}

// lastQuestionTopic returns the topic of the newest AI question.
func lastQuestionTopic(msgs []domain.ConsultationMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == domain.RoleAtlasAI && m.MessageType == domain.MessageText && m.TopicKey != nil {
			return *m.TopicKey
		}
	}
	return ""
}

// latestSolution returns the newest solution of the session.
func latestSolution(msgs []domain.ConsultationMessage) *domain.SolutionData {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].MessageType == domain.MessageSolution && msgs[i].SolutionData.Valid {
			sol := msgs[i].SolutionData.V
			return &sol
		}
	}
	return nil
}

func ownerMessageIDs(msgs []domain.ConsultationMessage) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool)
	for _, m := range msgs {
		if m.Role == domain.RoleOwner {
			out[m.ID] = true
		}
	}
	return out
}

// newMemory validates one confirmed memory: its sources must be OWNER messages of the
// session.
func newMemory(i int, in MemoryInput, owners map[uuid.UUID]bool) (domain.BusinessMemory, error) {
	field := fmt.Sprintf("confirmed_memories[%d]", i)
	fact := strings.TrimSpace(in.FactText)
	switch {
	case fact == "":
		return domain.BusinessMemory{}, apperror.InvalidInput(field + ".fact_text must not be blank")
	case len([]rune(fact)) > MaxMemoryFactLength:
		return domain.BusinessMemory{}, apperror.InvalidInput(fmt.Sprintf("%s.fact_text must be at most %d characters long", field, MaxMemoryFactLength))
	case len(in.SourceOwnerMessageIDs) == 0:
		return domain.BusinessMemory{}, apperror.InvalidInput(field + ".source_owner_message_ids must not be empty")
	}
	sources := make([]uuid.UUID, 0, len(in.SourceOwnerMessageIDs))
	for _, id := range in.SourceOwnerMessageIDs {
		if !owners[id] {
			return domain.BusinessMemory{}, apperror.InvalidInput(field + ".source_owner_message_ids must reference owner messages of this session")
		}
		if !slices.Contains(sources, id) {
			sources = append(sources, id)
		}
	}
	return domain.BusinessMemory{FactText: fact, SourceOwnerMessageIDs: domain.NewJSONB(sources), IsActive: true}, nil
}
