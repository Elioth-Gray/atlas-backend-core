package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// TxRunner runs fn in a database transaction. *database.Transactor satisfies it.
type TxRunner interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// MembershipReader looks up a user's membership in a shop. It must answer NotFound for
// unknown shops, deleted shops, and shops the user does not belong to alike.
type MembershipReader interface {
	GetMembership(ctx context.Context, userID, shopID uuid.UUID) (domain.ShopUser, error)
}

// requireMember enforces tenant isolation: the user must be an active member of the shop.
func requireMember(ctx context.Context, members MembershipReader, userID, shopID uuid.UUID) (domain.ShopUser, error) {
	m, err := members.GetMembership(ctx, userID, shopID)
	if err != nil {
		return domain.ShopUser{}, fmt.Errorf("check shop access: %w", err)
	}
	return m, nil
}

// requireOwner is requireMember restricted to the Owner role.
func requireOwner(ctx context.Context, members MembershipReader, userID, shopID uuid.UUID) error {
	m, err := requireMember(ctx, members, userID, shopID)
	if err != nil {
		return err
	}
	if m.Role != domain.ShopRoleOwner {
		return apperror.Forbidden("only the shop owner can do this")
	}
	return nil
}

// optionalText trims s and returns nil when it is blank.
func optionalText(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// requiredText trims s and fails when it is blank.
func requiredText(field, s string) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", apperror.InvalidInput(field + " must not be blank")
	}
	return t, nil
}
