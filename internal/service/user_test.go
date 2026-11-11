package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

type fakeUserRepo struct {
	created    domain.User
	changes    domain.UserChanges
	listLimit  int
	listOffset int
	updated    bool
}

func (f *fakeUserRepo) Create(_ context.Context, u domain.User) (domain.User, error) {
	f.created = u
	u.ID = uuid.New()
	return u, nil
}

func (f *fakeUserRepo) GetByID(context.Context, uuid.UUID) (domain.User, error) {
	return domain.User{}, apperror.NotFound("user not found")
}

func (f *fakeUserRepo) List(_ context.Context, limit, offset int) ([]domain.User, int64, error) {
	f.listLimit, f.listOffset = limit, offset
	return []domain.User{}, 0, nil
}

func (f *fakeUserRepo) Update(_ context.Context, _ uuid.UUID, c domain.UserChanges) (domain.User, error) {
	f.updated, f.changes = true, c
	return domain.User{}, nil
}

func (f *fakeUserRepo) Delete(context.Context, uuid.UUID) error { return nil }

func ptr[T any](v T) *T { return &v }

func TestUserService_Create(t *testing.T) {
	tests := []struct {
		name      string
		in        CreateUserInput
		wantErr   error
		wantName  string
		wantEmail string
	}{
		{
			name:      "normalizes name and email",
			in:        CreateUserInput{Name: "  Ada Lovelace ", Email: " Ada@Example.COM "},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@example.com",
		},
		{
			name:    "rejects blank name",
			in:      CreateUserInput{Name: "   ", Email: "ada@example.com"},
			wantErr: apperror.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeUserRepo{}
			_, err := NewUserService(repo).Create(context.Background(), tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if repo.created.Name != tt.wantName || repo.created.Email != tt.wantEmail {
				t.Errorf("stored %q/%q, want %q/%q", repo.created.Name, repo.created.Email, tt.wantName, tt.wantEmail)
			}
		})
	}
}

func TestUserService_Update(t *testing.T) {
	tests := []struct {
		name        string
		in          UpdateUserInput
		wantErr     error
		wantUpdated bool
	}{
		{name: "rejects empty update", in: UpdateUserInput{}, wantErr: apperror.ErrInvalidInput},
		{name: "rejects blank name", in: UpdateUserInput{Name: ptr(" ")}, wantErr: apperror.ErrInvalidInput},
		{name: "normalizes email", in: UpdateUserInput{Email: ptr(" X@Y.COM")}, wantUpdated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeUserRepo{}
			_, err := NewUserService(repo).Update(context.Background(), uuid.New(), tt.in)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if repo.updated != tt.wantUpdated {
				t.Fatalf("repo updated = %t, want %t", repo.updated, tt.wantUpdated)
			}
			if tt.wantUpdated && (repo.changes.Email == nil || *repo.changes.Email != "x@y.com" || repo.changes.Name != nil) {
				t.Errorf("unexpected changes: %+v", repo.changes)
			}
		})
	}
}

func TestUserService_GetWrapsNotFound(t *testing.T) {
	_, err := NewUserService(&fakeUserRepo{}).Get(context.Background(), uuid.New())

	var appErr *apperror.Error
	if !errors.Is(err, apperror.ErrNotFound) || !errors.As(err, &appErr) || appErr.Message != "user not found" {
		t.Fatalf("err = %v, want wrapped not-found apperror", err)
	}
}

func TestPageParams_Normalize(t *testing.T) {
	tests := []struct {
		name       string
		in         PageParams
		want       PageParams
		wantOffset int
	}{
		{name: "zero values use defaults", in: PageParams{}, want: PageParams{Page: 1, PageSize: DefaultPageSize}, wantOffset: 0},
		{name: "page size is capped", in: PageParams{Page: 3, PageSize: 1000}, want: PageParams{Page: 3, PageSize: MaxPageSize}, wantOffset: 200},
		{name: "valid values kept", in: PageParams{Page: 2, PageSize: 10}, want: PageParams{Page: 2, PageSize: 10}, wantOffset: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.Normalize()
			if got != tt.want || got.Offset() != tt.wantOffset {
				t.Errorf("Normalize() = %+v (offset %d), want %+v (offset %d)", got, got.Offset(), tt.want, tt.wantOffset)
			}
		})
	}
}
