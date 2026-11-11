package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	httpx.RegisterValidator()
	os.Exit(m.Run())
}

// fakeUserService implements UserService with overridable functions.
type fakeUserService struct {
	createFn func(ctx context.Context, in service.CreateUserInput) (domain.User, error)
	getFn    func(ctx context.Context, id uuid.UUID) (domain.User, error)
	listFn   func(ctx context.Context, p service.PageParams) (service.Page[domain.User], error)
	updateFn func(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error)
	deleteFn func(ctx context.Context, id uuid.UUID) error
}

func (f *fakeUserService) Create(ctx context.Context, in service.CreateUserInput) (domain.User, error) {
	return f.createFn(ctx, in)
}

func (f *fakeUserService) Get(ctx context.Context, id uuid.UUID) (domain.User, error) {
	return f.getFn(ctx, id)
}

func (f *fakeUserService) List(ctx context.Context, p service.PageParams) (service.Page[domain.User], error) {
	return f.listFn(ctx, p)
}

func (f *fakeUserService) Update(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error) {
	return f.updateFn(ctx, id, in)
}

func (f *fakeUserService) Delete(ctx context.Context, id uuid.UUID) error {
	return f.deleteFn(ctx, id)
}

// newUserRouter mounts UserHandler on a bare gin engine with no middleware.
func newUserRouter(svc UserService) *gin.Engine {
	r := gin.New()
	h := NewUserHandler(svc)
	g := r.Group("/api/v1/users")
	g.POST("", h.Create)
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.PATCH("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	return r
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) httpx.APIError {
	t.Helper()
	var resp httpx.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return resp.Error
}
