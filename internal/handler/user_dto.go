package handler

import (
	"time"

	"atlas-backend-core/internal/domain"
)

// createUserRequest is the body of POST /api/v1/users.
type createUserRequest struct {
	Name  string `json:"name" binding:"required,min=1,max=100"`
	Email string `json:"email" binding:"required,email,max=254"`
}

// updateUserRequest is the body of PATCH /api/v1/users/:id. Omitted fields are unchanged.
type updateUserRequest struct {
	Name  *string `json:"name" binding:"omitempty,min=1,max=100"`
	Email *string `json:"email" binding:"omitempty,email,max=254"`
}

// listUsersQuery is the query string of GET /api/v1/users.
type listUsersQuery struct {
	Page     int `form:"page" binding:"omitempty,min=1,max=100000"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// userResponse is the public representation of a user.
type userResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newUserResponse(u domain.User) userResponse {
	return userResponse{
		ID:        u.ID.String(),
		Name:      u.Name,
		Email:     u.Email,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}
