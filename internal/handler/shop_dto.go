package handler

import (
	"time"

	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/service"
)

// shopProfileRequest is a shop profile in register and update requests.
type shopProfileRequest struct {
	Name        string  `json:"name" binding:"required,min=1,max=150"`
	Description *string `json:"description" binding:"omitempty,max=2000"`
	Website     *string `json:"website" binding:"omitempty,max=255"`
	Image       *string `json:"image" binding:"omitempty,max=524288"`
	CategoryID  string  `json:"category_id" binding:"required,uuid"`
	ScaleID     string  `json:"scale_id" binding:"required,uuid"`
}

// ownerContactRequest updates the owner's contact details. Omitted fields are unchanged;
// an empty phone_number clears it.
type ownerContactRequest struct {
	Name        *string `json:"name" binding:"omitempty,min=1,max=100"`
	PhoneNumber *string `json:"phone_number" binding:"omitempty,max=30"`
}

// updateShopRequest is the body of PUT /owner/shops/:shopId: the full shop profile and,
// optionally, the owner's contact details.
type updateShopRequest struct {
	Name        string               `json:"name" binding:"required,min=1,max=150"`
	Description *string              `json:"description" binding:"omitempty,max=2000"`
	Website     *string              `json:"website" binding:"omitempty,max=255"`
	Image       *string              `json:"image" binding:"omitempty,max=524288"`
	CategoryID  string               `json:"category_id" binding:"required,uuid"`
	ScaleID     string               `json:"scale_id" binding:"required,uuid"`
	Owner       *ownerContactRequest `json:"owner"`
}

func (r updateShopRequest) input() service.ShopInput {
	return shopProfileRequest{
		Name: r.Name, Description: r.Description, Website: r.Website, Image: r.Image,
		CategoryID: r.CategoryID, ScaleID: r.ScaleID,
	}.input()
}

type lookupResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// shopResponse is a shop profile.
type shopResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Website     *string         `json:"website"`
	Image       *string         `json:"image"`
	Category    *lookupResponse `json:"category"`
	Scale       *lookupResponse `json:"scale"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func newShopResponse(s domain.Shop) shopResponse {
	out := shopResponse{
		ID:          s.ID.String(),
		Name:        s.Name,
		Description: s.Description,
		Website:     s.Website,
		Image:       s.Image,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
	}
	if s.Category != nil {
		out.Category = &lookupResponse{ID: s.Category.ID.String(), Name: s.Category.Category}
	}
	if s.Scale != nil {
		out.Scale = &lookupResponse{ID: s.Scale.ID.String(), Name: s.Scale.Scale}
	}
	return out
}

// publicShopResponse is what customers see on the QR page.
type publicShopResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Website     *string         `json:"website"`
	Image       *string         `json:"image"`
	Category    *lookupResponse `json:"category"`
	Scale       *lookupResponse `json:"scale"`
}

func newPublicShopResponse(s domain.Shop) publicShopResponse {
	full := newShopResponse(s)
	return publicShopResponse{
		ID: full.ID, Name: full.Name, Description: full.Description, Website: full.Website,
		Image: full.Image, Category: full.Category, Scale: full.Scale,
	}
}

// userResponse is a user's own or contact details.
type userResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Email       string  `json:"email"`
	PhoneNumber *string `json:"phone_number"`
}

func newUserResponse(u domain.User) userResponse {
	return userResponse{ID: u.ID.String(), Name: u.Name, Email: u.Email, PhoneNumber: u.PhoneNumber}
}

// myShopResponse is one entry of GET /owner/shops.
type myShopResponse struct {
	Role string       `json:"role"`
	Shop shopResponse `json:"shop"`
}

// shopDetailResponse is a shop as seen by one of its members.
type shopDetailResponse struct {
	Role  string        `json:"role"`
	Shop  shopResponse  `json:"shop"`
	Owner *userResponse `json:"owner"`
}
