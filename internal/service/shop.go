package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
)

// ShopRepository is the shop persistence ShopService needs.
type ShopRepository interface {
	MembershipReader
	masterDataChecker
	ListCategories(ctx context.Context) ([]domain.ShopCategory, error)
	ListScales(ctx context.Context) ([]domain.ShopScale, error)
	GetActive(ctx context.Context, id uuid.UUID) (domain.Shop, error)
	GetOwner(ctx context.Context, shopID uuid.UUID) (domain.User, error)
	ListMemberships(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.ShopUser, int64, error)
	Update(ctx context.Context, id uuid.UUID, c domain.ShopChanges) error
}

// UserUpdater updates the owner's contact details.
type UserUpdater interface {
	Update(ctx context.Context, id uuid.UUID, changes domain.UserChanges) (domain.User, error)
}

// ShopDetail is a shop as seen by one of its members.
type ShopDetail struct {
	Shop  domain.Shop
	Role  domain.ShopRole
	Owner domain.User
}

// UpdateShopInput replaces a shop profile and optionally the owner's contact details.
type UpdateShopInput struct {
	Shop             ShopInput
	OwnerName        *string
	OwnerPhoneNumber *string
}

// ShopService implements the public shop profile and the owner's shop management.
type ShopService struct {
	shops ShopRepository
	users UserUpdater
	tx    TxRunner
}

// NewShopService returns a ShopService.
func NewShopService(shops ShopRepository, users UserUpdater, tx TxRunner) *ShopService {
	return &ShopService{shops: shops, users: users, tx: tx}
}

// Categories returns the shop categories for forms.
func (s *ShopService) Categories(ctx context.Context) ([]domain.ShopCategory, error) {
	return s.shops.ListCategories(ctx)
}

// Scales returns the shop scales for forms.
func (s *ShopService) Scales(ctx context.Context) ([]domain.ShopScale, error) {
	return s.shops.ListScales(ctx)
}

// GetPublic returns the public profile of an active shop (customer QR page).
func (s *ShopService) GetPublic(ctx context.Context, shopID uuid.UUID) (domain.Shop, error) {
	shop, err := s.shops.GetActive(ctx, shopID)
	if err != nil {
		return domain.Shop{}, fmt.Errorf("get public shop: %w", err)
	}
	return shop, nil
}

// ListMine returns one page of the shops the user belongs to, with their role.
func (s *ShopService) ListMine(ctx context.Context, userID uuid.UUID, params PageParams) (Page[domain.ShopUser], error) {
	params = params.Normalize()
	items, total, err := s.shops.ListMemberships(ctx, userID, params.PageSize, params.Offset())
	if err != nil {
		return Page[domain.ShopUser]{}, fmt.Errorf("list my shops: %w", err)
	}
	return Page[domain.ShopUser]{Items: items, Total: total, Page: params.Page, PageSize: params.PageSize}, nil
}

// GetMine returns a shop the user belongs to.
func (s *ShopService) GetMine(ctx context.Context, userID, shopID uuid.UUID) (ShopDetail, error) {
	m, err := requireMember(ctx, s.shops, userID, shopID)
	if err != nil {
		return ShopDetail{}, err
	}
	return s.detail(ctx, shopID, m.Role)
}

// UpdateMine replaces the shop profile and updates the owner's contact details. Only the
// shop's Owner may do this.
func (s *ShopService) UpdateMine(ctx context.Context, userID, shopID uuid.UUID, in UpdateShopInput) (ShopDetail, error) {
	if err := requireOwner(ctx, s.shops, userID, shopID); err != nil {
		return ShopDetail{}, err
	}
	shopIn, err := normalizeShopInput(ctx, s.shops, in.Shop)
	if err != nil {
		return ShopDetail{}, err
	}
	var contact domain.UserChanges
	if in.OwnerName != nil {
		name, err := normalizeName(*in.OwnerName)
		if err != nil {
			return ShopDetail{}, err
		}
		contact.Name = &name
	}
	if in.OwnerPhoneNumber != nil {
		// An empty phone number clears it.
		phone := ""
		if p := optionalText(in.OwnerPhoneNumber); p != nil {
			phone = *p
		}
		contact.PhoneNumber = &phone
	}

	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.shops.Update(ctx, shopID, domain.ShopChanges{
			Name:           shopIn.Name,
			Description:    shopIn.Description,
			Website:        shopIn.Website,
			Image:          shopIn.Image,
			ShopCategoryID: shopIn.CategoryID,
			ShopScaleID:    shopIn.ScaleID,
		}); err != nil {
			return err
		}
		if contact.Name == nil && contact.PhoneNumber == nil {
			return nil
		}
		_, err := s.users.Update(ctx, userID, contact)
		return err
	})
	if err != nil {
		return ShopDetail{}, fmt.Errorf("update shop %s: %w", shopID, err)
	}
	return s.detail(ctx, shopID, domain.ShopRoleOwner)
}

func (s *ShopService) detail(ctx context.Context, shopID uuid.UUID, role domain.ShopRole) (ShopDetail, error) {
	shop, err := s.shops.GetActive(ctx, shopID)
	if err != nil {
		return ShopDetail{}, fmt.Errorf("get shop: %w", err)
	}
	owner, err := s.shops.GetOwner(ctx, shopID)
	if err != nil && !errors.Is(err, apperror.ErrNotFound) {
		return ShopDetail{}, fmt.Errorf("get shop owner: %w", err)
	}
	return ShopDetail{Shop: shop, Role: role, Owner: owner}, nil
}
