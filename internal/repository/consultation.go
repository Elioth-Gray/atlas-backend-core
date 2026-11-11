package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/domain"
)

// ConsultationRepository stores consultation sessions and their messages.
type ConsultationRepository struct {
	db *gorm.DB
}

// NewConsultationRepository returns a ConsultationRepository backed by db.
func NewConsultationRepository(db *gorm.DB) *ConsultationRepository {
	return &ConsultationRepository{db: db}
}

// CreateSession inserts a session.
func (r *ConsultationRepository) CreateSession(ctx context.Context, s domain.ConsultationSession) (domain.ConsultationSession, error) {
	if err := database.Conn(ctx, r.db).Create(&s).Error; err != nil {
		return domain.ConsultationSession{}, fmt.Errorf("insert consultation session: %w", err)
	}
	return s, nil
}

// DeleteEmptySession removes a session that has no messages (a START turn that failed).
func (r *ConsultationRepository) DeleteEmptySession(ctx context.Context, id uuid.UUID) error {
	db := database.Conn(ctx, r.db)
	var n int64
	if err := db.Model(&domain.ConsultationMessage{}).Where("session_id = ?", id).Count(&n).Error; err != nil {
		return fmt.Errorf("count session messages: %w", err)
	}
	if n > 0 {
		return nil
	}
	if err := db.Where("id = ?", id).Delete(&domain.ConsultationSession{}).Error; err != nil {
		return fmt.Errorf("delete consultation session: %w", err)
	}
	return nil
}

// GetSession returns a session by ID.
func (r *ConsultationRepository) GetSession(ctx context.Context, id uuid.UUID) (domain.ConsultationSession, error) {
	var s domain.ConsultationSession
	err := database.Conn(ctx, r.db).Where("id = ?", id).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ConsultationSession{}, apperror.NotFound("consultation session not found")
	}
	if err != nil {
		return domain.ConsultationSession{}, fmt.Errorf("select consultation session: %w", err)
	}
	return s, nil
}

// ListSessionsByIssue returns the sessions of an issue within a shop, newest first.
func (r *ConsultationRepository) ListSessionsByIssue(ctx context.Context, shopID, issueID uuid.UUID) ([]domain.ConsultationSession, error) {
	var out []domain.ConsultationSession
	err := database.Conn(ctx, r.db).
		Omit("context_snapshot").
		Where("shop_id = ? AND issue_id = ?", shopID, issueID).
		Order("created_at DESC, id DESC").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list consultation sessions: %w", err)
	}
	return out, nil
}

// FindSessionByProcessingRequest returns the session currently processing requestID.
func (r *ConsultationRepository) FindSessionByProcessingRequest(ctx context.Context, requestID uuid.UUID) (*domain.ConsultationSession, error) {
	var out []domain.ConsultationSession
	err := database.Conn(ctx, r.db).Where("processing_request_id = ?", requestID).Limit(1).Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("select session by processing request: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// FindSessionByAIRequest returns the session holding an AI message for requestID.
func (r *ConsultationRepository) FindSessionByAIRequest(ctx context.Context, requestID uuid.UUID) (*domain.ConsultationSession, error) {
	var out []domain.ConsultationSession
	err := database.Conn(ctx, r.db).
		Where("id IN (?)", database.Conn(ctx, r.db).Model(&domain.ConsultationMessage{}).
			Select("session_id").
			Where("request_id = ? AND role = ?", requestID, domain.RoleAtlasAI)).
		Limit(1).
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("select session by request: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out[0], nil
}

// LockSession atomically marks the session as processing requestID, provided its
// state_version still equals stateVersion and no other request holds it. A lock older than
// staleBefore (its holder crashed or timed out) may be taken over. It returns false when
// the lock was not acquired.
func (r *ConsultationRepository) LockSession(ctx context.Context, id uuid.UUID, stateVersion int64, requestID uuid.UUID, staleBefore time.Time) (bool, error) {
	res := database.Conn(ctx, r.db).Model(&domain.ConsultationSession{}).
		Where("id = ? AND state_version = ? AND status <> ?", id, stateVersion, domain.SessionArchived).
		Where("processing_request_id IS NULL OR updated_at < ?", staleBefore).
		Updates(map[string]any{"processing_request_id": requestID, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return false, fmt.Errorf("lock consultation session: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ReleaseLock clears the processing marker if requestID still holds it, leaving the state
// untouched (used when the AI call fails).
func (r *ConsultationRepository) ReleaseLock(ctx context.Context, id, requestID uuid.UUID) error {
	err := database.Conn(ctx, r.db).Model(&domain.ConsultationSession{}).
		Where("id = ? AND processing_request_id = ?", id, requestID).
		Updates(map[string]any{"processing_request_id": nil, "updated_at": time.Now().UTC()}).Error
	if err != nil {
		return fmt.Errorf("release consultation session: %w", err)
	}
	return nil
}

// FinishTurn applies the outcome of a turn held by requestID: new status and counters,
// state_version + 1, and the lock released. It fails with Conflict when the lock was lost.
func (r *ConsultationRepository) FinishTurn(ctx context.Context, id, requestID uuid.UUID, u domain.SessionTurnUpdate) (domain.ConsultationSession, error) {
	var s domain.ConsultationSession
	res := database.Conn(ctx, r.db).Model(&s).
		Clauses(returningAll).
		Where("id = ? AND processing_request_id = ?", id, requestID).
		Updates(map[string]any{
			"status":                u.Status,
			"question_count":        u.QuestionCount,
			"asked_topics":          u.AskedTopics,
			"skipped_topics":        u.SkippedTopics,
			"state_version":         gorm.Expr("state_version + 1"),
			"processing_request_id": nil,
			"updated_at":            time.Now().UTC(),
		})
	if res.Error != nil {
		return domain.ConsultationSession{}, fmt.Errorf("finish consultation turn: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ConsultationSession{}, apperror.Conflict("the session was modified by another request; reload it and retry")
	}
	return s, nil
}

// Archive moves a SOLUTION_READY, idle session at stateVersion to ARCHIVED with its final
// solution. It fails with Conflict when the session changed in the meantime.
func (r *ConsultationRepository) Archive(ctx context.Context, id uuid.UUID, stateVersion int64, finalMessageID uuid.UUID, at time.Time) (domain.ConsultationSession, error) {
	var s domain.ConsultationSession
	res := database.Conn(ctx, r.db).Model(&s).
		Clauses(returningAll).
		Where("id = ? AND state_version = ? AND status = ? AND processing_request_id IS NULL",
			id, stateVersion, domain.SessionSolutionReady).
		Updates(map[string]any{
			"status":                    domain.SessionArchived,
			"final_solution_message_id": finalMessageID,
			"archived_at":               at,
			"state_version":             gorm.Expr("state_version + 1"),
			"updated_at":                at,
		})
	if res.Error != nil {
		return domain.ConsultationSession{}, fmt.Errorf("archive consultation session: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ConsultationSession{}, apperror.Conflict("the session was modified by another request; reload it and retry")
	}
	return s, nil
}

// InsertMessages inserts messages in order.
func (r *ConsultationRepository) InsertMessages(ctx context.Context, msgs ...domain.ConsultationMessage) error {
	db := database.Conn(ctx, r.db)
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return apperror.Wrap(apperror.ErrConflict, "this turn was already processed", err)
			}
			return fmt.Errorf("insert consultation message: %w", err)
		}
	}
	return nil
}

// ListMessages returns every message of a session in conversation order.
func (r *ConsultationRepository) ListMessages(ctx context.Context, sessionID uuid.UUID) ([]domain.ConsultationMessage, error) {
	var out []domain.ConsultationMessage
	err := database.Conn(ctx, r.db).
		Where("session_id = ?", sessionID).
		Order("created_at, id").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list consultation messages: %w", err)
	}
	return out, nil
}

// GetMessage returns a message by ID.
func (r *ConsultationRepository) GetMessage(ctx context.Context, id uuid.UUID) (domain.ConsultationMessage, error) {
	var m domain.ConsultationMessage
	err := database.Conn(ctx, r.db).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ConsultationMessage{}, apperror.NotFound("message not found")
	}
	if err != nil {
		return domain.ConsultationMessage{}, fmt.Errorf("select consultation message: %w", err)
	}
	return m, nil
}

// MaxRevision returns the highest solution revision of a session (0 when none).
func (r *ConsultationRepository) MaxRevision(ctx context.Context, sessionID uuid.UUID) (int, error) {
	var rev int
	err := database.Conn(ctx, r.db).Model(&domain.ConsultationMessage{}).
		Select("COALESCE(MAX(revision), 0)").
		Where("session_id = ? AND message_type = ?", sessionID, domain.MessageSolution).
		Scan(&rev).Error
	if err != nil {
		return 0, fmt.Errorf("select max revision: %w", err)
	}
	return rev, nil
}

// LatestArchivedSolution returns the final solution of the newest archived session on the
// issue within the shop, or nil when there is none.
func (r *ConsultationRepository) LatestArchivedSolution(ctx context.Context, shopID, issueID uuid.UUID) (*domain.PriorSolution, error) {
	var sessions []domain.ConsultationSession
	err := database.Conn(ctx, r.db).
		Omit("context_snapshot").
		Where("shop_id = ? AND issue_id = ? AND status = ? AND final_solution_message_id IS NOT NULL",
			shopID, issueID, domain.SessionArchived).
		Order("archived_at DESC").
		Limit(1).
		Find(&sessions).Error
	if err != nil {
		return nil, fmt.Errorf("select archived session: %w", err)
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	s := sessions[0]
	msg, err := r.GetMessage(ctx, *s.FinalSolutionMessageID)
	if err != nil {
		return nil, err
	}
	if !msg.SolutionData.Valid || s.ArchivedAt == nil {
		return nil, nil
	}
	return &domain.PriorSolution{
		SessionID:  s.ID,
		MessageID:  msg.ID,
		ArchivedAt: *s.ArchivedAt,
		Solution:   msg.SolutionData.V,
	}, nil
}
