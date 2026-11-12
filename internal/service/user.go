// Package service implements the application's business logic. It is transport-agnostic:
// nothing here knows about HTTP or gin, and every operation takes a context.Context first.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// UserRepository is the persistence contract UserService depends on.
type UserRepository interface {
	Create(ctx context.Context, user domain.User) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	List(ctx context.Context, limit, offset int) ([]domain.User, int64, error)
	Update(ctx context.Context, id uuid.UUID, changes domain.UserChanges) (domain.User, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// CreateUserInput holds the data needed to create a user.
type CreateUserInput struct {
	Name  string
	Email string
}

// UpdateUserInput holds optional changes to a user. Nil fields are left untouched.
type UpdateUserInput struct {
	Name  *string
	Email *string
}

// UserService implements the user use cases.
type UserService struct {
	repo UserRepository
}

// NewUserService returns a UserService backed by repo.
func NewUserService(repo UserRepository) *UserService {
	return &UserService{repo: repo}
}

// Create registers a new user. Names are trimmed and emails are lower-cased.
func (s *UserService) Create(ctx context.Context, in CreateUserInput) (domain.User, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return domain.User{}, err
	}
	email, err := validateEmail(in.Email)
	if err != nil {
		return domain.User{}, err
	}

	user, err := s.repo.Create(ctx, domain.User{Name: name, Email: email})
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

// Get returns the user with the given ID.
func (s *UserService) Get(ctx context.Context, id uuid.UUID) (domain.User, error) {
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.User{}, fmt.Errorf("get user %s: %w", id, err)
	}
	return user, nil
}

// List returns one page of users.
func (s *UserService) List(ctx context.Context, params PageParams) (Page[domain.User], error) {
	params = params.Normalize()

	users, total, err := s.repo.List(ctx, params.PageSize, params.Offset())
	if err != nil {
		return Page[domain.User]{}, fmt.Errorf("list users: %w", err)
	}
	return Page[domain.User]{Items: users, Total: total, Page: params.Page, PageSize: params.PageSize}, nil
}

// Update applies a partial update to the user with the given ID.
func (s *UserService) Update(ctx context.Context, id uuid.UUID, in UpdateUserInput) (domain.User, error) {
	if in.Name == nil && in.Email == nil {
		return domain.User{}, apperror.InvalidInput("at least one field must be provided")
	}

	var changes domain.UserChanges
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return domain.User{}, err
		}
		changes.Name = &name
	}
	if in.Email != nil {
		email, err := validateEmail(*in.Email)
		if err != nil {
			return domain.User{}, err
		}
		changes.Email = &email
	}

	user, err := s.repo.Update(ctx, id, changes)
	if err != nil {
		return domain.User{}, fmt.Errorf("update user %s: %w", id, err)
	}
	return user, nil
}

// Delete removes the user with the given ID.
func (s *UserService) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete user %s: %w", id, err)
	}
	return nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apperror.InvalidInput("name must not be blank")
	}
	return name, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateEmail(email string) (string, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return "", apperror.InvalidInput("email must be a valid email address")
	}
	return email, nil
}
