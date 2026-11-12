package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// fakeSessions implements the parts of ConsultationRepository the pre-lock and turn checks use;
// any other method panics through the nil embedded interface.
type fakeSessions struct {
	ConsultationRepository
	session        domain.ConsultationSession
	created        domain.ConsultationSession
	doneFor        *domain.ConsultationSession
	busyFor        *domain.ConsultationSession
	insertedMsgs   []domain.ConsultationMessage
	deletedEmptyID uuid.UUID
	releasedID     uuid.UUID
	lockCalls      int
	lockResult     bool
}

func (f *fakeSessions) GetSession(context.Context, uuid.UUID) (domain.ConsultationSession, error) {
	if f.session.ID == uuid.Nil {
		return domain.ConsultationSession{}, apperror.NotFound("consultation session not found")
	}
	return f.session, nil
}

func (f *fakeSessions) CreateSession(_ context.Context, s domain.ConsultationSession) (domain.ConsultationSession, error) {
	f.created = s
	return s, nil
}

func (f *fakeSessions) DeleteEmptySession(_ context.Context, id uuid.UUID) error {
	f.deletedEmptyID = id
	return nil
}

func (f *fakeSessions) ReleaseLock(_ context.Context, id, _ uuid.UUID) error {
	f.releasedID = id
	return nil
}

func (f *fakeSessions) FindSessionByProcessingRequest(context.Context, uuid.UUID) (*domain.ConsultationSession, error) {
	return f.busyFor, nil
}

func (f *fakeSessions) FindSessionByAIRequest(context.Context, uuid.UUID) (*domain.ConsultationSession, error) {
	return f.doneFor, nil
}

func (f *fakeSessions) ListMessages(context.Context, uuid.UUID) ([]domain.ConsultationMessage, error) {
	return nil, nil
}

func (f *fakeSessions) LockSession(context.Context, uuid.UUID, int64, uuid.UUID, time.Time) (bool, error) {
	f.lockCalls++
	return f.lockResult, nil
}

func (f *fakeSessions) InsertMessages(_ context.Context, msgs ...domain.ConsultationMessage) error {
	f.insertedMsgs = append(f.insertedMsgs, msgs...)
	return nil
}

func (f *fakeSessions) FinishTurn(_ context.Context, id, _ uuid.UUID, u domain.SessionTurnUpdate) (domain.ConsultationSession, error) {
	s := f.created
	if s.ID == uuid.Nil {
		s = f.session
	}
	s.ID = id
	s.Status = u.Status
	s.QuestionCount = u.QuestionCount
	s.AskedTopics = u.AskedTopics
	s.SkippedTopics = u.SkippedTopics
	s.StateVersion++
	s.ProcessingRequestID = nil
	return s, nil
}

func (f *fakeSessions) LatestArchivedSolution(context.Context, uuid.UUID, uuid.UUID) (*domain.PriorSolution, error) {
	return nil, nil
}

type fakeMembers struct{ shopID uuid.UUID }

func (f fakeMembers) GetMembership(_ context.Context, _, shopID uuid.UUID) (domain.ShopUser, error) {
	if shopID != f.shopID {
		return domain.ShopUser{}, apperror.NotFound("shop not found")
	}
	return domain.ShopUser{Role: domain.ShopRoleOwner}, nil
}

// failingAI fails the test when called.
type failingAI struct{ t *testing.T }

func (a failingAI) ProcessFeedback(context.Context, FeedbackAnalysisRequest) (FeedbackAnalysis, error) {
	a.t.Fatal("AI must not be called")
	return FeedbackAnalysis{}, nil
}

func (a failingAI) ProcessConsultationTurn(context.Context, ConsultationTurnRequest) (ConsultationTurnResult, error) {
	a.t.Fatal("AI must not be called")
	return ConsultationTurnResult{}, nil
}

func TestConsultationService_TurnGuards(t *testing.T) {
	shopID, otherShop := uuid.New(), uuid.New()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	base := domain.ConsultationSession{
		ID: uuid.New(), ShopID: shopID, Status: domain.SessionCollectingContext, StateVersion: 3, UpdatedAt: now,
	}
	reqID := uuid.New()
	reply := TurnInput{RequestID: reqID, StateVersion: 3, Event: domain.EventOwnerReply, Content: "hello"}

	tests := []struct {
		name      string
		session   func() domain.ConsultationSession
		member    uuid.UUID
		doneFor   func(s domain.ConsultationSession) *domain.ConsultationSession
		in        TurnInput
		aiOff     bool
		lockOK    bool
		wantKind  error
		wantReply bool
		wantLock  int
	}{
		{name: "other tenant sees not found", member: otherShop, in: reply, wantKind: apperror.ErrNotFound},
		{name: "unknown event", member: shopID, in: TurnInput{RequestID: reqID, StateVersion: 3, Event: "HACK"}, wantKind: apperror.ErrInvalidInput},
		{name: "reply without content", member: shopID, in: TurnInput{RequestID: reqID, StateVersion: 3, Event: domain.EventOwnerReply}, wantKind: apperror.ErrInvalidInput},
		{
			name: "replayed request answers from the database", member: shopID, in: reply, wantReply: true,
			doneFor: func(s domain.ConsultationSession) *domain.ConsultationSession { return &s },
		},
		{
			name: "request id of another session", member: shopID, in: reply, wantKind: apperror.ErrConflict,
			doneFor: func(domain.ConsultationSession) *domain.ConsultationSession {
				return &domain.ConsultationSession{ID: uuid.New()}
			},
		},
		{
			name: "archived", member: shopID, in: reply, wantKind: apperror.ErrConflict,
			session: func() domain.ConsultationSession { s := base; s.Status = domain.SessionArchived; return s },
		},
		{
			name: "processing another request", member: shopID, in: reply, wantKind: apperror.ErrConflict,
			session: func() domain.ConsultationSession { s := base; id := uuid.New(); s.ProcessingRequestID = &id; return s },
		},
		{
			name: "stale lock is taken over", member: shopID, in: reply, aiOff: true, wantKind: apperror.ErrUnavailable,
			session: func() domain.ConsultationSession {
				s := base
				id := uuid.New()
				s.ProcessingRequestID, s.UpdatedAt = &id, now.Add(-time.Hour)
				return s
			},
		},
		{name: "stale state version", member: shopID, in: TurnInput{RequestID: reqID, StateVersion: 2, Event: domain.EventGenerateNow}, wantKind: apperror.ErrConflict},
		{name: "event not allowed in state", member: shopID, in: TurnInput{RequestID: reqID, StateVersion: 3, Event: domain.EventContinueDiscussion, Content: "x"}, wantKind: apperror.ErrConflict},
		{name: "lock lost to a concurrent request", member: shopID, in: reply, lockOK: false, wantKind: apperror.ErrConflict, wantLock: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := base
			if tt.session != nil {
				sess = tt.session()
			}
			repo := &fakeSessions{session: sess, lockResult: tt.lockOK}
			if tt.doneFor != nil {
				repo.doneFor = tt.doneFor(sess)
			}
			svc := NewConsultationService(repo, nil, fakeMembers{shopID: tt.member}, nil, nil, failingAI{t}, nil,
				ConsultationConfig{AIEnabled: !tt.aiOff, TurnTimeout: time.Minute})
			svc.now = func() time.Time { return now }

			view, err := svc.Turn(context.Background(), uuid.New(), sess.ID, tt.in)
			if tt.wantReply {
				if err != nil || !view.Replayed {
					t.Fatalf("want replayed view, got %+v, %v", view.Replayed, err)
				}
				return
			}
			if !errors.Is(err, tt.wantKind) {
				t.Fatalf("err = %v, want kind %v", err, tt.wantKind)
			}
			if repo.lockCalls != tt.wantLock {
				t.Errorf("LockSession called %d times, want %d", repo.lockCalls, tt.wantLock)
			}
		})
	}
}

type fakeEvidence struct{ shopID, issueID uuid.UUID }

func (f fakeEvidence) ResolveIssueShop(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, error) {
	return f.shopID, nil
}

func (f fakeEvidence) GetIssueSummary(context.Context, uuid.UUID, uuid.UUID) (domain.IssueSummary, error) {
	return domain.IssueSummary{Issue: domain.Issue{ID: f.issueID, Title: "Slow Wi-Fi"}}, nil
}

func (f fakeEvidence) RepresentativeEvidence(context.Context, uuid.UUID, uuid.UUID, int) ([]domain.EvidenceOpinion, error) {
	return nil, nil
}

type fakeShopReader struct{ shopID uuid.UUID }

func (f fakeShopReader) GetActive(context.Context, uuid.UUID) (domain.Shop, error) {
	return domain.Shop{ID: f.shopID, Name: "Bakery"}, nil
}

type fakeMemories struct{ MemoryRepository }

func (fakeMemories) ListActiveFacts(context.Context, uuid.UUID) ([]domain.MemoryFact, error) {
	return nil, nil
}

type stubAI struct{ q NextQuestion }

func (a stubAI) ProcessFeedback(context.Context, FeedbackAnalysisRequest) (FeedbackAnalysis, error) {
	return FeedbackAnalysis{}, nil
}

func (a stubAI) ProcessConsultationTurn(context.Context, ConsultationTurnRequest) (ConsultationTurnResult, error) {
	q := a.q
	return ConsultationTurnResult{Question: &q, ModelID: "test-model", PromptVersion: "v1"}, nil
}

type noopTx struct{}

func (noopTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func TestConsultationService_StartAndMessageOrder(t *testing.T) {
	shopID, issueID, userID, reqID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	t.Run("active processing request returns conflict", func(t *testing.T) {
		repo := &fakeSessions{busyFor: &domain.ConsultationSession{
			ID: uuid.New(), ProcessingRequestID: &reqID, UpdatedAt: now,
		}}
		svc := NewConsultationService(repo, fakeEvidence{shopID: shopID, issueID: issueID}, nil, nil, nil, failingAI{t}, noopTx{},
			ConsultationConfig{AIEnabled: true, TurnTimeout: time.Minute})
		svc.now = func() time.Time { return now }

		_, err := svc.Start(context.Background(), userID, issueID, reqID)
		if !errors.Is(err, apperror.ErrConflict) {
			t.Fatalf("err = %v, want conflict", err)
		}
	})

	t.Run("stale processing request is cleaned up and new session starts at state_version 1", func(t *testing.T) {
		staleID := uuid.New()
		repo := &fakeSessions{busyFor: &domain.ConsultationSession{
			ID: staleID, ProcessingRequestID: &reqID, UpdatedAt: now.Add(-time.Hour),
		}}
		svc := NewConsultationService(repo, fakeEvidence{shopID: shopID, issueID: issueID}, fakeMembers{shopID: shopID},
			fakeShopReader{shopID: shopID}, fakeMemories{}, stubAI{q: NextQuestion{TopicKey: "router", Text: "Which router?"}}, noopTx{},
			ConsultationConfig{AIEnabled: true, TurnTimeout: time.Minute})
		svc.now = func() time.Time { return now }

		view, err := svc.Start(context.Background(), userID, issueID, reqID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.deletedEmptyID != staleID || repo.releasedID != staleID {
			t.Errorf("stale session %s was not cleaned up (deleted=%s, released=%s)", staleID, repo.deletedEmptyID, repo.releasedID)
		}
		if repo.created.StateVersion != 0 || view.Session.StateVersion != 1 {
			t.Errorf("created state_version=%d, finished state_version=%d; want 0 -> 1", repo.created.StateVersion, view.Session.StateVersion)
		}
	})

	t.Run("turn orders AI message strictly after owner message", func(t *testing.T) {
		sess := domain.ConsultationSession{
			ID: uuid.New(), ShopID: shopID, Status: domain.SessionCollectingContext, StateVersion: 1, UpdatedAt: now,
		}
		repo := &fakeSessions{session: sess, lockResult: true}
		svc := NewConsultationService(repo, nil, fakeMembers{shopID: shopID}, nil, nil,
			stubAI{q: NextQuestion{TopicKey: "isp", Text: "Which ISP?"}}, noopTx{},
			ConsultationConfig{AIEnabled: true, TurnTimeout: time.Minute})
		svc.now = func() time.Time { return now }

		view, err := svc.Turn(context.Background(), userID, sess.ID, TurnInput{
			RequestID: reqID, StateVersion: 1, Event: domain.EventOwnerReply, Content: "We use fiber",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(view.Messages) != 2 || !view.Messages[1].CreatedAt.After(view.Messages[0].CreatedAt) {
			t.Fatalf("messages created_at = %v, want AI strictly after OWNER", view.Messages)
		}
	})
}
