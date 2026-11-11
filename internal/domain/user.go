// Package domain holds the core business entities. They double as GORM models (via struct
// tags only, no GORM import), but are never serialized directly as API payloads: handlers
// map them to dedicated request/response DTOs.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// User is a registered user of the platform.
type User struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Name      string    `gorm:"not null"`
	Email     string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name instead of relying on GORM's pluralization rules.
func (User) TableName() string { return "users" }

// UserChanges is a partial update to a User. Nil fields are left unchanged.
type UserChanges struct {
	Name  *string
	Email *string
}
