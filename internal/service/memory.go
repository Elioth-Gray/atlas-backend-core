package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// UpdateMemoryInput holds optional changes to a business memory.
type UpdateMemoryInput struct {
	FactText *string
	IsActive *bool
}

// MemoryService manages a shop's business memories.
type MemoryService struct {
	repo    MemoryRepository
	members MembershipReader
}

// NewMemoryService returns a MemoryService.
func NewMemoryService(repo MemoryRepository, members MembershipReader) *MemoryService {
	return &MemoryService{repo: repo, members: members}
}

// List returns one page of the shop's memories, optionally filtered by is_active.
func (s *MemoryService) List(ctx context.Context, userID, shopID uuid.UUID, active *bool, params PageParams) (Page[domain.BusinessMemory], error) {
	if _, err := requireMember(ctx, s.members, userID, shopID); err != nil {
		return Page[domain.BusinessMemory]{}, err
	}
	params = params.Normalize()
	items, total, err := s.repo.List(ctx, shopID, active, params.PageSize, params.Offset())
	if err != nil {
		return Page[domain.BusinessMemory]{}, fmt.Errorf("list memories: %w", err)
	}
	return Page[domain.BusinessMemory]{Items: items, Total: total, Page: params.Page, PageSize: params.PageSize}, nil
}

// Update edits the fact or toggles is_active of a memory of a shop the user belongs to.
func (s *MemoryService) Update(ctx context.Context, userID, memoryID uuid.UUID, in UpdateMemoryInput) (domain.BusinessMemory, error) {
	if in.FactText == nil && in.IsActive == nil {
		return domain.BusinessMemory{}, apperror.InvalidInput("at least one field must be provided")
	}
	mem, err := s.repo.Get(ctx, memoryID)
	if err != nil {
		return domain.BusinessMemory{}, err
	}
	if _, err := requireMember(ctx, s.members, userID, mem.ShopID); err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			// Memories of other tenants look exactly like missing ones.
			return domain.BusinessMemory{}, apperror.NotFound("memory not found")
		}
		return domain.BusinessMemory{}, err
	}
	changes := domain.MemoryChanges{IsActive: in.IsActive}
	if in.FactText != nil {
		fact := strings.TrimSpace(*in.FactText)
		if fact == "" {
			return domain.BusinessMemory{}, apperror.InvalidInput("fact_text must not be blank")
		}
		if len([]rune(fact)) > MaxMemoryFactLength {
			return domain.BusinessMemory{}, apperror.InvalidInput(fmt.Sprintf("fact_text must be at most %d characters long", MaxMemoryFactLength))
		}
		changes.FactText = &fact
	}
	updated, err := s.repo.Update(ctx, memoryID, changes)
	if err != nil {
		return domain.BusinessMemory{}, fmt.Errorf("update memory %s: %w", memoryID, err)
	}
	return updated, nil
}
