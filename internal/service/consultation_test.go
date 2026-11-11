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

// fakeSessions implements the parts of ConsultationRepository the pre-lock checks use;
// any other method panics through the nil embedded interface.
type fakeSessions struct {
	ConsultationRepository
	session    domain.ConsultationSession
	doneFor    *domain.ConsultationSession
	lockCalls  int
	lockResult bool
}

func (f *fakeSessions) GetSession(context.Context, uuid.UUID) (domain.ConsultationSession, error) {
	if f.session.ID == uuid.Nil {
		return domain.ConsultationSession{}, apperror.NotFound("consultation session not found")
	}
	return f.session, nil
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
