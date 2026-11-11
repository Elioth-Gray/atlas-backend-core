package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/httpx"
	"atlas-backend-core/internal/service"
)

var testUser = domain.User{
	ID:        uuid.MustParse("6f1c1c38-2f53-4a43-9f43-3f0a5a1f2b7e"),
	Name:      "Ada Lovelace",
	Email:     "ada@example.com",
	CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
}

func TestUserHandler_Create(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		svcErr       error
		wantStatus   int
		wantCode     string
		wantFields   []string
		wantSvcCalls int
	}{
		{
			name:         "valid request creates user",
			body:         `{"name":"Ada Lovelace","email":"ada@example.com"}`,
			wantStatus:   http.StatusCreated,
			wantSvcCalls: 1,
		},
		{
			name:       "empty body",
			body:       "",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeBadRequest,
		},
		{
			name:       "malformed JSON",
			body:       `{"name":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeBadRequest,
		},
		{
			name:       "wrong field type",
			body:       `{"name":123,"email":"ada@example.com"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeBadRequest,
		},
		{
			name:       "missing fields reports each field",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeValidation,
			wantFields: []string{"name", "email"},
		},
		{
			name:       "invalid email",
			body:       `{"name":"Ada","email":"not-an-email"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeValidation,
			wantFields: []string{"email"},
		},
		{
			name:         "duplicate email maps to conflict",
			body:         `{"name":"Ada","email":"ada@example.com"}`,
			svcErr:       apperror.Conflict("email is already registered"),
			wantStatus:   http.StatusConflict,
			wantCode:     httpx.CodeConflict,
			wantSvcCalls: 1,
		},
		{
			name:         "business validation maps to invalid input",
			body:         `{"name":"   ","email":"ada@example.com"}`,
			svcErr:       apperror.InvalidInput("name must not be blank"),
			wantStatus:   http.StatusBadRequest,
			wantCode:     httpx.CodeInvalidInput,
			wantSvcCalls: 1,
		},
		{
			name:         "unexpected error is hidden behind 500",
			body:         `{"name":"Ada","email":"ada@example.com"}`,
			svcErr:       errors.New("connection refused to 10.0.0.5:5432"),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     httpx.CodeInternal,
			wantSvcCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			svc := &fakeUserService{
				createFn: func(_ context.Context, in service.CreateUserInput) (domain.User, error) {
					calls++
					if tt.svcErr != nil {
						return domain.User{}, tt.svcErr
					}
					u := testUser
					u.Name, u.Email = in.Name, in.Email
					return u, nil
				},
			}

			w := doRequest(t, newUserRouter(svc), http.MethodPost, "/api/v1/users", tt.body)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if calls != tt.wantSvcCalls {
				t.Errorf("service called %d times, want %d", calls, tt.wantSvcCalls)
			}
			if tt.wantCode == "" {
				var resp httpx.DataResponse[userResponse]
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if resp.Data.ID != testUser.ID.String() || resp.Data.Email != "ada@example.com" {
					t.Errorf("unexpected response data: %+v", resp.Data)
				}
				return
			}

			apiErr := decodeError(t, w)
			if apiErr.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			if tt.wantStatus == http.StatusInternalServerError && apiErr.Message != "internal server error" {
				t.Errorf("internal error leaked: %q", apiErr.Message)
			}
			assertFields(t, apiErr.Details, tt.wantFields)
		})
	}
}

func TestUserHandler_Get(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		svcErr     error
		wantStatus int
		wantCode   string
		wantFields []string
	}{
		{name: "found", path: "/api/v1/users/" + testUser.ID.String(), wantStatus: http.StatusOK},
		{
			name:       "invalid uuid",
			path:       "/api/v1/users/not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeValidation,
			wantFields: []string{"id"},
		},
		{
			name:       "not found",
			path:       "/api/v1/users/" + uuid.NewString(),
			svcErr:     apperror.NotFound("user not found"),
			wantStatus: http.StatusNotFound,
			wantCode:   httpx.CodeNotFound,
		},
		{
			name:       "client gone",
			path:       "/api/v1/users/" + testUser.ID.String(),
			svcErr:     context.Canceled,
			wantStatus: statusClientClosedRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeUserService{
				getFn: func(_ context.Context, _ uuid.UUID) (domain.User, error) {
					if tt.svcErr != nil {
						return domain.User{}, tt.svcErr
					}
					return testUser, nil
				},
			}

			w := doRequest(t, newUserRouter(svc), http.MethodGet, tt.path, "")

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantCode != "" {
				apiErr := decodeError(t, w)
				if apiErr.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
				}
				assertFields(t, apiErr.Details, tt.wantFields)
			}
		})
	}
}

func TestUserHandler_List(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantParams service.PageParams
		wantCode   string
	}{
		{name: "defaults are left to the service", query: "", wantStatus: http.StatusOK, wantParams: service.PageParams{}},
		{name: "explicit page", query: "?page=2&page_size=5", wantStatus: http.StatusOK, wantParams: service.PageParams{Page: 2, PageSize: 5}},
		{name: "page size above limit", query: "?page_size=500", wantStatus: http.StatusBadRequest, wantCode: httpx.CodeValidation},
		{name: "non numeric page", query: "?page=abc", wantStatus: http.StatusBadRequest, wantCode: httpx.CodeBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got service.PageParams
			svc := &fakeUserService{
				listFn: func(_ context.Context, p service.PageParams) (service.Page[domain.User], error) {
					got = p
					return service.Page[domain.User]{Items: []domain.User{testUser}, Total: 11, Page: 2, PageSize: 5}, nil
				},
			}

			w := doRequest(t, newUserRouter(svc), http.MethodGet, "/api/v1/users"+tt.query, "")

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantCode != "" {
				if apiErr := decodeError(t, w); apiErr.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
				}
				return
			}
			if got != tt.wantParams {
				t.Errorf("service params = %+v, want %+v", got, tt.wantParams)
			}
			var resp httpx.ListResponse[userResponse]
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			wantMeta := httpx.PageMeta{Page: 2, PageSize: 5, Total: 11, TotalPages: 3}
			if resp.Meta != wantMeta || len(resp.Data) != 1 {
				t.Errorf("meta = %+v (data len %d), want %+v (data len 1)", resp.Meta, len(resp.Data), wantMeta)
			}
		})
	}
}

func TestUserHandler_Update(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
		wantInput  func(t *testing.T, in service.UpdateUserInput)
	}{
		{
			name:       "partial update passes only provided fields",
			body:       `{"name":"Grace"}`,
			wantStatus: http.StatusOK,
			wantInput: func(t *testing.T, in service.UpdateUserInput) {
				if in.Name == nil || *in.Name != "Grace" || in.Email != nil {
					t.Errorf("unexpected input: name=%v email=%v", in.Name, in.Email)
				}
			},
		},
		{
			name:       "provided field is still validated",
			body:       `{"email":"nope"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   httpx.CodeValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeUserService{
				updateFn: func(_ context.Context, _ uuid.UUID, in service.UpdateUserInput) (domain.User, error) {
					if tt.wantInput != nil {
						tt.wantInput(t, in)
					}
					return testUser, nil
				},
			}

			w := doRequest(t, newUserRouter(svc), http.MethodPatch, "/api/v1/users/"+testUser.ID.String(), tt.body)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
			if tt.wantCode != "" {
				if apiErr := decodeError(t, w); apiErr.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", apiErr.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestUserHandler_Delete(t *testing.T) {
	tests := []struct {
		name       string
		svcErr     error
		wantStatus int
	}{
		{name: "deleted", wantStatus: http.StatusNoContent},
		{name: "not found", svcErr: apperror.NotFound("user not found"), wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeUserService{
				deleteFn: func(context.Context, uuid.UUID) error { return tt.svcErr },
			}

			w := doRequest(t, newUserRouter(svc), http.MethodDelete, "/api/v1/users/"+testUser.ID.String(), "")

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}
		})
	}
}

func assertFields(t *testing.T, details []httpx.FieldError, want []string) {
	t.Helper()
	if len(want) == 0 {
		return
	}
	got := make(map[string]bool, len(details))
	for _, d := range details {
		got[d.Field] = true
	}
	for _, f := range want {
		if !got[f] {
			t.Errorf("missing validation detail for field %q in %+v", f, details)
		}
	}
}
