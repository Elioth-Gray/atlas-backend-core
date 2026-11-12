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
	"golang.org/x/sync/errgroup"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/logger"
)

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

	aiCreatedAt := s.now()
	if owner != nil && !aiCreatedAt.After(owner.CreatedAt) {
		aiCreatedAt = owner.CreatedAt.Add(time.Microsecond)
	}
	ai := domain.ConsultationMessage{
		ID:               uuid.Must(uuid.NewV7()),
		SessionID:        sess.ID,
		Role:             domain.RoleAtlasAI,
		ModelID:          optionalText(&res.ModelID),
		PromptVersion:    optionalText(&res.PromptVersion),
		MemoryCandidates: domain.NewJSONB(res.MemoryCandidates),
		RequestID:        &requestID,
		CreatedAt:        aiCreatedAt,
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

// buildSnapshot gathers the SQL evidence for a new session concurrently.
func (s *ConsultationService) buildSnapshot(ctx context.Context, shopID, issueID uuid.UUID) (domain.ContextSnapshot, error) {
	var (
		summary  domain.IssueSummary
		evidence []domain.EvidenceOpinion
		shop     domain.Shop
		memories []domain.MemoryFact
		prior    *domain.PriorSolution
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		summary, err = s.evidence.GetIssueSummary(gctx, shopID, issueID)
		return err
	})
	g.Go(func() (err error) {
		evidence, err = s.evidence.RepresentativeEvidence(gctx, shopID, issueID, s.cfg.EvidenceLimit)
		return err
	})
	g.Go(func() (err error) {
		shop, err = s.shops.GetActive(gctx, shopID)
		return err
	})
	g.Go(func() (err error) {
		memories, err = s.memories.ListActiveFacts(gctx, shopID)
		return err
	})
	g.Go(func() (err error) {
		prior, err = s.sessions.LatestArchivedSolution(gctx, shopID, issueID)
		return err
	})
	if err := g.Wait(); err != nil {
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
