package domain

import (
	"time"

	"github.com/google/uuid"
)

// ShopRole is a user's role in a shop (shop_role_enum).
type ShopRole string

// Shop roles.
const (
	ShopRoleOwner    ShopRole = "Owner"
	ShopRoleEmployee ShopRole = "Employee"
)

// ShopCategory is a business category (master data). Only enabled categories are offered
// on forms and accepted for new or updated shops.
type ShopCategory struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Category  string    `gorm:"not null"`
	IsEnabled bool      `gorm:"not null;default:true"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	IsDeleted bool      `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (ShopCategory) TableName() string { return "shop_category" }

// ShopScale is a business scale (master data).
type ShopScale struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Scale     string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	IsDeleted bool      `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (ShopScale) TableName() string { return "shop_scale" }

// Shop is a business that collects feedback. Category and Scale are loaded on demand.
type Shop struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name           string    `gorm:"not null"`
	Description    *string
	Website        *string
	Image          *string
	ShopCategoryID uuid.UUID     `gorm:"type:uuid;not null"`
	ShopScaleID    uuid.UUID     `gorm:"type:uuid;not null"`
	Category       *ShopCategory `gorm:"foreignKey:ShopCategoryID"`
	Scale          *ShopScale    `gorm:"foreignKey:ShopScaleID"`
	CreatedAt      time.Time     `gorm:"not null"`
	UpdatedAt      time.Time     `gorm:"not null"`
	IsDeleted      bool          `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (Shop) TableName() string { return "shops" }

// ShopUser links a user to a shop with a role.
type ShopUser struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Role      ShopRole  `gorm:"type:shop_role_enum;not null"`
	UserID    uuid.UUID `gorm:"type:uuid;not null"`
	ShopID    uuid.UUID `gorm:"type:uuid;not null"`
	Shop      *Shop     `gorm:"foreignKey:ShopID"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
	IsDeleted bool      `gorm:"not null;default:false"`
}

// TableName pins the table name.
func (ShopUser) TableName() string { return "shops_users" }

// ShopChanges replaces the editable profile fields of a shop.
type ShopChanges struct {
	Name           string
	Description    *string
	Website        *string
	Image          *string
	ShopCategoryID uuid.UUID
	ShopScaleID    uuid.UUID
}
