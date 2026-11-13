package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

// AuthService is the business logic AuthHandler depends on.
type AuthService interface {
	Register(ctx context.Context, in service.RegisterInput) (service.AuthResult, error)
	Login(ctx context.Context, email, password string) (service.AuthResult, error)
	Refresh(ctx context.Context, refreshToken string) (service.AuthResult, error)
}

// AuthHandler serves /api/v1/auth.
type AuthHandler struct {
	svc AuthService
}

// NewAuthHandler returns an AuthHandler.
func NewAuthHandler(svc AuthService) *AuthHandler { return &AuthHandler{svc: svc} }

// registerRequest is the body of POST /auth/signup.
type registerRequest struct {
	Name        string              `json:"name" binding:"required,min=1,max=100"`
	Email       string              `json:"email" binding:"required,email,max=254"`
	PhoneNumber *string             `json:"phone_number" binding:"omitempty,max=30"`
	Password    string              `json:"password" binding:"required,min=8,max=72"`
	Shop        *shopProfileRequest `json:"shop" binding:"required"`
}

// loginRequest is the body of POST /auth/signin.
type loginRequest struct {
	Email    string `json:"email" binding:"required,email,max=254"`
	Password string `json:"password" binding:"required,max=72"`
}

// refreshRequest is the body of POST /auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required,max=4096"`
}

// authResponse carries the tokens and the logged-in user.
type authResponse struct {
	TokenType             string        `json:"token_type"`
	AccessToken           string        `json:"access_token"`
	AccessTokenExpiresAt  time.Time     `json:"access_token_expires_at"`
	RefreshToken          string        `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time     `json:"refresh_token_expires_at"`
	User                  userResponse  `json:"user"`
	Shop                  *shopResponse `json:"shop,omitempty"`
}

func newAuthResponse(r service.AuthResult) authResponse {
	out := authResponse{
		TokenType:             "Bearer",
		AccessToken:           r.Tokens.AccessToken,
		AccessTokenExpiresAt:  r.Tokens.AccessExpiresAt,
		RefreshToken:          r.Tokens.RefreshToken,
		RefreshTokenExpiresAt: r.Tokens.RefreshExpiresAt,
		User:                  newUserResponse(r.User),
	}
	if r.Shop != nil {
		shop := newShopResponse(*r.Shop)
		out.Shop = &shop
	}
	return out
}

// Register handles POST /api/v1/auth/signup: creates an owner account and their shop.
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}
	res, err := h.svc.Register(c.Request.Context(), service.RegisterInput{
		Name:        req.Name,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Password:    req.Password,
		Shop:        req.Shop.input(),
	})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, httpx.DataResponse[authResponse]{Data: newAuthResponse(res)})
}

// Login handles POST /api/v1/auth/signin.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[authResponse]{Data: newAuthResponse(res)})
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}
	res, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[authResponse]{Data: newAuthResponse(res)})
}

// input maps a validated shop profile to service input.
func (r shopProfileRequest) input() service.ShopInput {
	return service.ShopInput{
		Name:        r.Name,
		Description: r.Description,
		Website:     r.Website,
		Image:       r.Image,
		CategoryID:  uuid.MustParse(r.CategoryID),
		ScaleID:     uuid.MustParse(r.ScaleID),
	}
}
