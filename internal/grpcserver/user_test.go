package grpcserver

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	userv1 "atlas-backend-core/gen/atlas/user/v1"
	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/requestid"
	"atlas-backend-core/internal/service"
)

var testUser = domain.User{
	ID:        uuid.MustParse("6f1c1c38-2f53-4a43-9f43-3f0a5a1f2b7e"),
	Name:      "Ada Lovelace",
	Email:     "ada@example.com",
	CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
}

func incoming(ctx context.Context, kv ...string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(kv...))
}

func TestUserServer_GetUser(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		token      string
		cancelCtx  bool
		svcErr     error
		wantCode   codes.Code
		wantFields []string
	}{
		{name: "found", id: testUser.ID.String(), token: testToken, wantCode: codes.OK},
		{name: "invalid id", id: "nope", token: testToken, wantCode: codes.InvalidArgument, wantFields: []string{"id"}},
		{name: "not found", id: uuid.NewString(), token: testToken, svcErr: apperror.NotFound("user not found"), wantCode: codes.NotFound},
		{name: "forbidden", id: testUser.ID.String(), token: testToken, svcErr: apperror.Forbidden("no access"), wantCode: codes.PermissionDenied},
		{name: "internal error hidden", id: testUser.ID.String(), token: testToken, svcErr: errors.New("pq: secret detail"), wantCode: codes.Internal},
		{name: "missing token", id: testUser.ID.String(), token: "", wantCode: codes.Unauthenticated},
		{name: "wrong token", id: testUser.ID.String(), token: "wrong", wantCode: codes.Unauthenticated},
		{name: "cancelled context", id: testUser.ID.String(), token: testToken, cancelCtx: true, wantCode: codes.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dial := testServer(t, &fakeUserService{
				getFn: func(context.Context, uuid.UUID) (domain.User, error) {
					if tt.svcErr != nil {
						return domain.User{}, tt.svcErr
					}
					return testUser, nil
				},
			})
			client := userv1.NewUserServiceClient(dial(tt.token))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancelCtx {
				cancel()
			}
			resp, err := client.GetUser(ctx, &userv1.GetUserRequest{Id: tt.id})

			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", got, tt.wantCode, err)
			}
			if tt.wantCode == codes.Internal && status.Convert(err).Message() != "internal error" {
				t.Errorf("internal error leaked: %q", status.Convert(err).Message())
			}
			for _, f := range tt.wantFields {
				if !slices.Contains(fieldViolations(err), f) {
					t.Errorf("missing field violation %q in %v", f, fieldViolations(err))
				}
			}
			if tt.wantCode == codes.OK && resp.GetUser().GetEmail() != testUser.Email {
				t.Errorf("unexpected user: %v", resp.GetUser())
			}
		})
	}
}

func TestUserServer_CreateUser(t *testing.T) {
	tests := []struct {
		name       string
		req        *userv1.CreateUserRequest
		svcErr     error
		wantCode   codes.Code
		wantFields []string
	}{
		{name: "created", req: &userv1.CreateUserRequest{Name: "Ada", Email: "ada@example.com"}, wantCode: codes.OK},
		{name: "missing fields", req: &userv1.CreateUserRequest{}, wantCode: codes.InvalidArgument, wantFields: []string{"name", "email"}},
		{name: "invalid email", req: &userv1.CreateUserRequest{Name: "Ada", Email: "nope"}, wantCode: codes.InvalidArgument, wantFields: []string{"email"}},
		{
			name:     "duplicate email",
			req:      &userv1.CreateUserRequest{Name: "Ada", Email: "ada@example.com"},
			svcErr:   apperror.Conflict("email is already registered"),
			wantCode: codes.AlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dial := testServer(t, &fakeUserService{
				createFn: func(context.Context, service.CreateUserInput) (domain.User, error) {
					if tt.svcErr != nil {
						return domain.User{}, tt.svcErr
					}
					return testUser, nil
				},
			})
			_, err := userv1.NewUserServiceClient(dial(testToken)).CreateUser(context.Background(), tt.req)

			if got := status.Code(err); got != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", got, tt.wantCode, err)
			}
			for _, f := range tt.wantFields {
				if !slices.Contains(fieldViolations(err), f) {
					t.Errorf("missing field violation %q in %v", f, fieldViolations(err))
				}
			}
		})
	}
}

func TestUserServer_UpdateUser(t *testing.T) {
	tests := []struct {
		name       string
		paths      []string
		wantCode   codes.Code
		wantFields []string
		wantInput  service.UpdateUserInput
	}{
		{name: "mask selects name only", paths: []string{"name"}, wantCode: codes.OK},
		{name: "empty mask", paths: nil, wantCode: codes.InvalidArgument, wantFields: []string{"update_mask"}},
		{name: "unsupported path", paths: []string{"id"}, wantCode: codes.InvalidArgument, wantFields: []string{"update_mask"}},
		{name: "masked field is validated", paths: []string{"email"}, wantCode: codes.InvalidArgument, wantFields: []string{"user.email"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got service.UpdateUserInput
			dial := testServer(t, &fakeUserService{
				updateFn: func(_ context.Context, _ uuid.UUID, in service.UpdateUserInput) (domain.User, error) {
					got = in
					return testUser, nil
				},
			})
			req := &userv1.UpdateUserRequest{
				// Email is invalid on purpose: it must be ignored unless "email" is in the mask.
				User:       &userv1.User{Id: testUser.ID.String(), Name: "Grace", Email: "not-an-email"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: tt.paths},
			}
			_, err := userv1.NewUserServiceClient(dial(testToken)).UpdateUser(context.Background(), req)

			if code := status.Code(err); code != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", code, tt.wantCode, err)
			}
			for _, f := range tt.wantFields {
				if !slices.Contains(fieldViolations(err), f) {
					t.Errorf("missing field violation %q in %v", f, fieldViolations(err))
				}
			}
			if tt.wantCode == codes.OK && (got.Name == nil || *got.Name != "Grace" || got.Email != nil) {
				t.Errorf("service input = name:%v email:%v, want name only", got.Name, got.Email)
			}
		})
	}
}

func TestUserServer_ListUsers(t *testing.T) {
	tests := []struct {
		name     string
		req      *userv1.ListUsersRequest
		wantCode codes.Code
	}{
		{name: "defaults", req: &userv1.ListUsersRequest{}, wantCode: codes.OK},
		{name: "page size too large", req: &userv1.ListUsersRequest{PageSize: 500}, wantCode: codes.InvalidArgument},
		{name: "negative page", req: &userv1.ListUsersRequest{Page: -1}, wantCode: codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dial := testServer(t, &fakeUserService{
				listFn: func(context.Context, service.PageParams) (service.Page[domain.User], error) {
					return service.Page[domain.User]{Items: []domain.User{testUser}, Total: 1, Page: 1, PageSize: 20}, nil
				},
			})
			resp, err := userv1.NewUserServiceClient(dial(testToken)).ListUsers(context.Background(), tt.req)

			if code := status.Code(err); code != tt.wantCode {
				t.Fatalf("code = %v, want %v (err %v)", code, tt.wantCode, err)
			}
			if tt.wantCode == codes.OK && (len(resp.GetUsers()) != 1 || resp.GetTotalSize() != 1 || resp.GetPageSize() != 20) {
				t.Errorf("unexpected response: %v", resp)
			}
		})
	}
}

func TestUserServer_DeleteUser(t *testing.T) {
	dial := testServer(t, &fakeUserService{
		deleteFn: func(context.Context, uuid.UUID) error { return apperror.NotFound("user not found") },
	})
	_, err := userv1.NewUserServiceClient(dial(testToken)).DeleteUser(context.Background(), &userv1.DeleteUserRequest{Id: uuid.NewString()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

// TestRequestIDPropagation checks that the client sends the caller's request ID, the
// server exposes it to the service through the context, and echoes it in the header.
func TestRequestIDPropagation(t *testing.T) {
	var seen string
	dial := testServer(t, &fakeUserService{
		getFn: func(ctx context.Context, _ uuid.UUID) (domain.User, error) {
			seen = requestid.FromContext(ctx)
			return testUser, nil
		},
	})

	ctx := requestid.WithContext(context.Background(), "upstream-req-123")
	var header metadata.MD
	_, err := userv1.NewUserServiceClient(dial(testToken)).GetUser(ctx, &userv1.GetUserRequest{Id: testUser.ID.String()}, grpc.Header(&header))
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if seen != "upstream-req-123" {
		t.Errorf("service saw request id %q, want upstream-req-123", seen)
	}
	if got := header.Get(requestid.MetadataKey); len(got) != 1 || got[0] != "upstream-req-123" {
		t.Errorf("response header %s = %v", requestid.MetadataKey, got)
	}
}
