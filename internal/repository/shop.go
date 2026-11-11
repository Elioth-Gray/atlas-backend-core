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

// ShopRepository stores shops, their master data, and user memberships.
type ShopRepository struct {
	db *gorm.DB
}

// NewShopRepository returns a ShopRepository backed by db.
func NewShopRepository(db *gorm.DB) *ShopRepository {
	return &ShopRepository{db: db}
}

// ListCategories returns every active shop category, alphabetically.
func (r *ShopRepository) ListCategories(ctx context.Context) ([]domain.ShopCategory, error) {
	var out []domain.ShopCategory
	if err := database.Conn(ctx, r.db).Where("is_deleted = false").Order("category").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("list shop categories: %w", err)
	}
	return out, nil
}

// ListScales returns every active shop scale in creation order.
func (r *ShopRepository) ListScales(ctx context.Context) ([]domain.ShopScale, error) {
	var out []domain.ShopScale
	if err := database.Conn(ctx, r.db).Where("is_deleted = false").Order("created_at, scale").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("list shop scales: %w", err)
	}
	return out, nil
}

// CategoryExists reports whether an active category with the given ID exists.
func (r *ShopRepository) CategoryExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var n int64
	err := database.Conn(ctx, r.db).Model(&domain.ShopCategory{}).Where("id = ? AND is_deleted = false", id).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("check shop category: %w", err)
	}
	return n > 0, nil
}

// ScaleExists reports whether an active scale with the given ID exists.
func (r *ShopRepository) ScaleExists(ctx context.Context, id uuid.UUID) (bool, error) {
	var n int64
	err := database.Conn(ctx, r.db).Model(&domain.ShopScale{}).Where("id = ? AND is_deleted = false", id).Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("check shop scale: %w", err)
	}
	return n > 0, nil
}

// Create inserts a shop.
func (r *ShopRepository) Create(ctx context.Context, shop domain.Shop) (domain.Shop, error) {
	if err := database.Conn(ctx, r.db).Omit(clause.Associations).Create(&shop).Error; err != nil {
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return domain.Shop{}, apperror.Wrap(apperror.ErrInvalidInput, "unknown shop category or scale", err)
		}
		return domain.Shop{}, fmt.Errorf("insert shop: %w", err)
	}
	return shop, nil
}

// AddMember links a user to a shop with a role.
func (r *ShopRepository) AddMember(ctx context.Context, m domain.ShopUser) error {
	if err := database.Conn(ctx, r.db).Omit(clause.Associations).Create(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return apperror.Wrap(apperror.ErrConflict, "user is already a member of this shop", err)
		}
		return fmt.Errorf("insert shop membership: %w", err)
	}
	return nil
}

// GetActive returns an active shop with its category and scale.
func (r *ShopRepository) GetActive(ctx context.Context, id uuid.UUID) (domain.Shop, error) {
	var shop domain.Shop
	err := database.Conn(ctx, r.db).
		Preload("Category").
		Preload("Scale").
		Where("id = ? AND is_deleted = false", id).
		First(&shop).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Shop{}, apperror.NotFound("shop not found")
	}
	if err != nil {
		return domain.Shop{}, fmt.Errorf("select shop: %w", err)
	}
	return shop, nil
}

// GetMembership returns the active membership of userID in an active shop. This is the
// tenant isolation check behind every /owner endpoint.
func (r *ShopRepository) GetMembership(ctx context.Context, userID, shopID uuid.UUID) (domain.ShopUser, error) {
	var m domain.ShopUser
	err := database.Conn(ctx, r.db).
		Joins("JOIN shops s ON s.id = shops_users.shop_id AND s.is_deleted = false").
		Where("shops_users.user_id = ? AND shops_users.shop_id = ? AND shops_users.is_deleted = false", userID, shopID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Same answer as a missing shop, so other tenants' shop IDs cannot be probed.
		return domain.ShopUser{}, apperror.NotFound("shop not found")
	}
	if err != nil {
		return domain.ShopUser{}, fmt.Errorf("select shop membership: %w", err)
	}
	return m, nil
}

// ListMemberships returns one page of the user's active shop memberships (with the shop,
// its category and scale), oldest first, and their total count.
func (r *ShopRepository) ListMemberships(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.ShopUser, int64, error) {
	base := func() *gorm.DB {
		return database.Conn(ctx, r.db).Model(&domain.ShopUser{}).
			Joins("JOIN shops s ON s.id = shops_users.shop_id AND s.is_deleted = false").
			Where("shops_users.user_id = ? AND shops_users.is_deleted = false", userID)
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count shop memberships: %w", err)
	}
	if total == 0 {
		return []domain.ShopUser{}, 0, nil
	}
	var out []domain.ShopUser
	err := base().
		Preload("Shop").
		Preload("Shop.Category").
		Preload("Shop.Scale").
		Order("shops_users.created_at, shops_users.id").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list shop memberships: %w", err)
	}
	return out, total, nil
}

// Update replaces the editable profile fields of an active shop.
func (r *ShopRepository) Update(ctx context.Context, id uuid.UUID, c domain.ShopChanges) error {
	res := database.Conn(ctx, r.db).Model(&domain.Shop{}).
		Where("id = ? AND is_deleted = false", id).
		Updates(map[string]any{
			"name":             c.Name,
			"description":      c.Description,
			"website":          c.Website,
			"image":            c.Image,
			"shop_category_id": c.ShopCategoryID,
			"shop_scale_id":    c.ShopScaleID,
			"updated_at":       time.Now().UTC(),
		})
	switch {
	case errors.Is(res.Error, gorm.ErrForeignKeyViolated):
		return apperror.Wrap(apperror.ErrInvalidInput, "unknown shop category or scale", res.Error)
	case res.Error != nil:
		return fmt.Errorf("update shop: %w", res.Error)
	case res.RowsAffected == 0:
		return apperror.NotFound("shop not found")
	}
	return nil
}

// GetOwner returns the shop's (first) active Owner, whose contact details are shown on the
// shop profile.
func (r *ShopRepository) GetOwner(ctx context.Context, shopID uuid.UUID) (domain.User, error) {
	var u domain.User
	err := database.Conn(ctx, r.db).
		Joins("JOIN shops_users su ON su.user_id = users.id AND su.is_deleted = false AND su.shop_id = ? AND su.role = ?",
			shopID, domain.ShopRoleOwner).
		Where("users.is_deleted = false").
		Order("su.created_at").
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.User{}, apperror.NotFound("shop owner not found")
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select shop owner: %w", err)
	}
	return u, nil
}
