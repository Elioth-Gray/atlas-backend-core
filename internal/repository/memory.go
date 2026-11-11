package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/domain"
)

// returningAll makes UPDATE ... RETURNING * fill the model with the updated row.
var returningAll = clause.Returning{}

// MemoryRepository stores business memories.
type MemoryRepository struct {
	db *gorm.DB
}

// NewMemoryRepository returns a MemoryRepository backed by db.
func NewMemoryRepository(db *gorm.DB) *MemoryRepository {
	return &MemoryRepository{db: db}
}

// ListActiveFacts returns the active memories of a shop, oldest first.
func (r *MemoryRepository) ListActiveFacts(ctx context.Context, shopID uuid.UUID) ([]domain.MemoryFact, error) {
	var out []domain.MemoryFact
	err := database.Conn(ctx, r.db).Model(&domain.BusinessMemory{}).
		Select("id, fact_text").
		Where("shop_id = ? AND is_active = true", shopID).
		Order("confirmed_at, id").
		Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list active memories: %w", err)
	}
	return out, nil
}

// List returns one page of a shop's memories, newest first, optionally filtered by
// is_active, and their total count.
func (r *MemoryRepository) List(ctx context.Context, shopID uuid.UUID, active *bool, limit, offset int) ([]domain.BusinessMemory, int64, error) {
	base := func() *gorm.DB {
		q := database.Conn(ctx, r.db).Model(&domain.BusinessMemory{}).Where("shop_id = ?", shopID)
		if active != nil {
			q = q.Where("is_active = ?", *active)
		}
		return q
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count memories: %w", err)
	}
	if total == 0 {
		return []domain.BusinessMemory{}, 0, nil
	}
	var out []domain.BusinessMemory
	if err := base().Order("confirmed_at DESC, id DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		return nil, 0, fmt.Errorf("list memories: %w", err)
	}
	return out, total, nil
}

// Get returns a memory by ID.
func (r *MemoryRepository) Get(ctx context.Context, id uuid.UUID) (domain.BusinessMemory, error) {
	var m domain.BusinessMemory
	err := database.Conn(ctx, r.db).Where("id = ?", id).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.BusinessMemory{}, apperror.NotFound("memory not found")
	}
	if err != nil {
		return domain.BusinessMemory{}, fmt.Errorf("select memory: %w", err)
	}
	return m, nil
}

// Create inserts memories.
func (r *MemoryRepository) Create(ctx context.Context, memories []domain.BusinessMemory) ([]domain.BusinessMemory, error) {
	if len(memories) == 0 {
		return memories, nil
	}
	if err := database.Conn(ctx, r.db).Create(&memories).Error; err != nil {
		return nil, fmt.Errorf("insert memories: %w", err)
	}
	return memories, nil
}

// Update applies the non-nil fields of changes and returns the updated memory.
func (r *MemoryRepository) Update(ctx context.Context, id uuid.UUID, c domain.MemoryChanges) (domain.BusinessMemory, error) {
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if c.FactText != nil {
		updates["fact_text"] = *c.FactText
	}
	if c.IsActive != nil {
		updates["is_active"] = *c.IsActive
	}
	var m domain.BusinessMemory
	res := database.Conn(ctx, r.db).Model(&m).Clauses(returningAll).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return domain.BusinessMemory{}, fmt.Errorf("update memory: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.BusinessMemory{}, apperror.NotFound("memory not found")
	}
	return m, nil
}
