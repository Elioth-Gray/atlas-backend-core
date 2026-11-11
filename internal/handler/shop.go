package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

// ShopService is the business logic ShopHandler depends on.
type ShopService interface {
	Categories(ctx context.Context) ([]domain.ShopCategory, error)
	Scales(ctx context.Context) ([]domain.ShopScale, error)
	GetPublic(ctx context.Context, shopID uuid.UUID) (domain.Shop, error)
	ListMine(ctx context.Context, userID uuid.UUID, params service.PageParams) (service.Page[domain.ShopUser], error)
	GetMine(ctx context.Context, userID, shopID uuid.UUID) (service.ShopDetail, error)
	UpdateMine(ctx context.Context, userID, shopID uuid.UUID, in service.UpdateShopInput) (service.ShopDetail, error)
}

// FeedbackService is the business logic behind customer feedback submission.
type FeedbackService interface {
	Submit(ctx context.Context, shopID uuid.UUID, in service.SubmitFeedbackInput) (domain.Feedback, error)
}

// ShopHandler serves the public shop endpoints and /owner/shops.
type ShopHandler struct {
	shops     ShopService
	feedbacks FeedbackService
}

// NewShopHandler returns a ShopHandler.
func NewShopHandler(shops ShopService, feedbacks FeedbackService) *ShopHandler {
	return &ShopHandler{shops: shops, feedbacks: feedbacks}
}

// submitFeedbackRequest is the body of POST /public/shops/:shopId/feedbacks.
type submitFeedbackRequest struct {
	Name     *string  `json:"name" binding:"omitempty,max=100"`
	Rating   *float64 `json:"rating" binding:"required,gte=1,lte=5"`
	Feedback string   `json:"feedback" binding:"required,min=1,max=5000"`
	Image    *string  `json:"image" binding:"omitempty,max=2048"`
}

// feedbackCreatedResponse acknowledges a stored feedback.
type feedbackCreatedResponse struct {
	FeedbackID string    `json:"feedback_id"`
	ShopID     string    `json:"shop_id"`
	Rating     float64   `json:"rating"`
	CreatedAt  time.Time `json:"created_at"`
}

// Categories handles GET /api/v1/public/shop-categories.
func (h *ShopHandler) Categories(c *gin.Context) {
	items, err := h.shops.Categories(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]lookupResponse, 0, len(items))
	for _, it := range items {
		out = append(out, lookupResponse{ID: it.ID.String(), Name: it.Category})
	}
	c.JSON(http.StatusOK, httpx.DataResponse[[]lookupResponse]{Data: out})
}

// Scales handles GET /api/v1/public/shop-scales.
func (h *ShopHandler) Scales(c *gin.Context) {
	items, err := h.shops.Scales(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]lookupResponse, 0, len(items))
	for _, it := range items {
		out = append(out, lookupResponse{ID: it.ID.String(), Name: it.Scale})
	}
	c.JSON(http.StatusOK, httpx.DataResponse[[]lookupResponse]{Data: out})
}

// GetPublic handles GET /api/v1/public/shops/:shopId.
func (h *ShopHandler) GetPublic(c *gin.Context) {
	shopID, err := uuidParam(c, "shopId")
	if err != nil {
		handleError(c, err)
		return
	}
	shop, err := h.shops.GetPublic(c.Request.Context(), shopID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[publicShopResponse]{Data: newPublicShopResponse(shop)})
}

// SubmitFeedback handles POST /api/v1/public/shops/:shopId/feedbacks. It answers 201 as
// soon as the feedback is stored; the AI analysis runs in the background.
func (h *ShopHandler) SubmitFeedback(c *gin.Context) {
	shopID, err := uuidParam(c, "shopId")
	if err != nil {
		handleError(c, err)
		return
	}
	var req submitFeedbackRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}
	fb, err := h.feedbacks.Submit(c.Request.Context(), shopID, service.SubmitFeedbackInput{
		Name: req.Name, Rating: *req.Rating, Text: req.Feedback, Image: req.Image,
	})
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, httpx.DataResponse[feedbackCreatedResponse]{Data: feedbackCreatedResponse{
		FeedbackID: fb.ID.String(), ShopID: fb.ShopID.String(), Rating: fb.Rating, CreatedAt: fb.CreatedAt,
	}})
}

// ListMine handles GET /api/v1/owner/shops.
func (h *ShopHandler) ListMine(c *gin.Context) {
	userID, err := currentUser(c)
	if err != nil {
		handleError(c, err)
		return
	}
	var q pageQuery
	if err := bindQuery(c, &q); err != nil {
		handleError(c, err)
		return
	}
	page, err := h.shops.ListMine(c.Request.Context(), userID, q.params())
	if err != nil {
		handleError(c, err)
		return
	}
	out := make([]myShopResponse, 0, len(page.Items))
	for _, m := range page.Items {
		if m.Shop != nil {
			out = append(out, myShopResponse{Role: string(m.Role), Shop: newShopResponse(*m.Shop)})
		}
	}
	c.JSON(http.StatusOK, httpx.ListResponse[myShopResponse]{Data: out, Meta: httpx.NewPageMeta(page.Page, page.PageSize, page.Total)})
}

// GetMine handles GET /api/v1/owner/shops/:shopId.
func (h *ShopHandler) GetMine(c *gin.Context) {
	userID, shopID, ok := userAndUUID(c, "shopId")
	if !ok {
		return
	}
	detail, err := h.shops.GetMine(c.Request.Context(), userID, shopID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[shopDetailResponse]{Data: newShopDetailResponse(detail)})
}

// UpdateMine handles PUT /api/v1/owner/shops/:shopId (Owner role only).
func (h *ShopHandler) UpdateMine(c *gin.Context) {
	userID, shopID, ok := userAndUUID(c, "shopId")
	if !ok {
		return
	}
	var req updateShopRequest
	if err := bindJSON(c, &req); err != nil {
		handleError(c, err)
		return
	}
	in := service.UpdateShopInput{Shop: req.input()}
	if req.Owner != nil {
		in.OwnerName, in.OwnerPhoneNumber = req.Owner.Name, req.Owner.PhoneNumber
	}
	detail, err := h.shops.UpdateMine(c.Request.Context(), userID, shopID, in)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.DataResponse[shopDetailResponse]{Data: newShopDetailResponse(detail)})
}

func newShopDetailResponse(d service.ShopDetail) shopDetailResponse {
	out := shopDetailResponse{Role: string(d.Role), Shop: newShopResponse(d.Shop)}
	if d.Owner.ID != uuid.Nil {
		owner := newUserResponse(d.Owner)
		out.Owner = &owner
	}
	return out
}

// userAndUUID returns the authenticated user and a UUID path parameter, writing the error
// response itself when either is missing.
func userAndUUID(c *gin.Context, param string) (userID, id uuid.UUID, ok bool) {
	userID, err := currentUser(c)
	if err != nil {
		handleError(c, err)
		return uuid.Nil, uuid.Nil, false
	}
	id, err = uuidParam(c, param)
	if err != nil {
		handleError(c, err)
		return uuid.Nil, uuid.Nil, false
	}
	return userID, id, true
}
