// Package repository implements persistence with GORM. Repositories are the only layer
// that touches *gorm.DB. They translate expected conditions (record not found, duplicate
// keys) into apperror values and wrap every other database error with context.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/database"
	"atlas-backend-core/internal/domain"
)

// UserRepository stores users in PostgreSQL.
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository returns a UserRepository backed by db.
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create inserts a user and returns it with its generated ID and timestamps.
func (r *UserRepository) Create(ctx context.Context, user domain.User) (domain.User, error) {
	if err := database.Conn(ctx, r.db).Create(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return domain.User{}, apperror.Wrap(apperror.ErrConflict, "email is already registered", err)
		}
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return user, nil
}

// GetByID returns the user with the given ID.
func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	var user domain.User
	err := database.Conn(ctx, r.db).Where("id = ?", id).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.User{}, apperror.NotFound("user not found")
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user: %w", err)
	}
	return user, nil
}

// List returns one page of users, newest first, and the total number of users.
func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]domain.User, int64, error) {
	var total int64
	if err := database.Conn(ctx, r.db).Model(&domain.User{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	if total == 0 {
		return []domain.User{}, 0, nil
	}

	var users []domain.User
	err := database.Conn(ctx, r.db).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&users).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}

// Update applies the non-nil fields of changes to the user and returns the updated row.
// A map is used (rather than a struct) so that zero values such as "" or false are still
// written when explicitly provided.
func (r *UserRepository) Update(ctx context.Context, id uuid.UUID, changes domain.UserChanges) (domain.User, error) {
	updates := make(map[string]any, 2)
	if changes.Name != nil {
		updates["name"] = *changes.Name
	}
	if changes.Email != nil {
		updates["email"] = *changes.Email
	}

	var user domain.User
	res := database.Conn(ctx, r.db).
		Model(&user).
		Clauses(clause.Returning{}).
		Where("id = ?", id).
		Updates(updates)
	switch {
	case errors.Is(res.Error, gorm.ErrDuplicatedKey):
		return domain.User{}, apperror.Wrap(apperror.ErrConflict, "email is already registered", res.Error)
	case res.Error != nil:
		return domain.User{}, fmt.Errorf("update user: %w", res.Error)
	case res.RowsAffected == 0:
		return domain.User{}, apperror.NotFound("user not found")
	}
	return user, nil
}

// Delete removes the user with the given ID.
func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := database.Conn(ctx, r.db).Where("id = ?", id).Delete(&domain.User{})
	if res.Error != nil {
		return fmt.Errorf("delete user: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return apperror.NotFound("user not found")
	}
	return nil
}
