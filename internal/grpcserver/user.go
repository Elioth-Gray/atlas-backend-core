package grpcserver

import (
	"context"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "atlas-backend-core/gen/atlas/user/v1"
	"atlas-backend-core/internal/domain"
	"atlas-backend-core/internal/service"
)

const maxPage = 100000

// UserService is the business logic UserServer depends on: the same service the REST
// handlers use.
type UserService interface {
	Create(ctx context.Context, in service.CreateUserInput) (domain.User, error)
	Get(ctx context.Context, id uuid.UUID) (domain.User, error)
	List(ctx context.Context, params service.PageParams) (service.Page[domain.User], error)
	Update(ctx context.Context, id uuid.UUID, in service.UpdateUserInput) (domain.User, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// UserServer implements userv1.UserServiceServer. Each RPC validates its request, maps it
// to service input, and maps the result (or error) back to protobuf.
type UserServer struct {
	userv1.UnimplementedUserServiceServer

	svc      UserService
	validate *validator.Validate
}

// NewUserServer returns a UserServer backed by svc.
func NewUserServer(svc UserService) *UserServer {
	return &UserServer{svc: svc, validate: validator.New(validator.WithRequiredStructEnabled())}
}

func (s *UserServer) newBadRequest() *badRequest { return &badRequest{validate: s.validate} }

// GetUser implements userv1.UserServiceServer.
func (s *UserServer) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	bad := s.newBadRequest()
	id := bad.uuid("id", req.GetId())
	if err := bad.err(); err != nil {
		return nil, err
	}

	user, err := s.svc.Get(ctx, id)
	if err != nil {
		return nil, toStatus(ctx, err)
	}
	return &userv1.GetUserResponse{User: toProtoUser(user)}, nil
}

// ListUsers implements userv1.UserServiceServer.
func (s *UserServer) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	bad := s.newBadRequest()
	bad.check("page", req.GetPage(), fmt.Sprintf("gte=0,lte=%d", maxPage))
	bad.check("page_size", req.GetPageSize(), fmt.Sprintf("gte=0,lte=%d", service.MaxPageSize))
	if err := bad.err(); err != nil {
		return nil, err
	}

	page, err := s.svc.List(ctx, service.PageParams{Page: int(req.GetPage()), PageSize: int(req.GetPageSize())})
	if err != nil {
		return nil, toStatus(ctx, err)
	}
	users := make([]*userv1.User, 0, len(page.Items))
	for _, u := range page.Items {
		users = append(users, toProtoUser(u))
	}
	return &userv1.ListUsersResponse{
		Users:     users,
		TotalSize: page.Total,
		Page:      int32(min(page.Page, maxPage)),                 //nolint:gosec // bounded by maxPage
		PageSize:  int32(min(page.PageSize, service.MaxPageSize)), //nolint:gosec // bounded by MaxPageSize
	}, nil
}

// CreateUser implements userv1.UserServiceServer.
func (s *UserServer) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	bad := s.newBadRequest()
	bad.check("name", req.GetName(), "required,max=100")
	bad.check("email", req.GetEmail(), "required,email,max=254")
	if err := bad.err(); err != nil {
		return nil, err
	}

	user, err := s.svc.Create(ctx, service.CreateUserInput{Name: req.GetName(), Email: req.GetEmail()})
	if err != nil {
		return nil, toStatus(ctx, err)
	}
	return &userv1.CreateUserResponse{User: toProtoUser(user)}, nil
}

// UpdateUser implements userv1.UserServiceServer. Only the paths in update_mask are read
// from the request; supported paths are "name" and "email".
func (s *UserServer) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	bad := s.newBadRequest()
	u := req.GetUser()
	id := bad.uuid("user.id", u.GetId())

	paths := req.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		bad.add("update_mask", "update_mask must list at least one field")
	}
	var in service.UpdateUserInput
	for _, path := range paths {
		switch path {
		case "name":
			name := u.GetName()
			bad.check("user.name", name, "required,max=100")
			in.Name = &name
		case "email":
			email := u.GetEmail()
			bad.check("user.email", email, "required,email,max=254")
			in.Email = &email
		default:
			bad.add("update_mask", fmt.Sprintf("unsupported field %q (supported: name, email)", path))
		}
	}
	if err := bad.err(); err != nil {
		return nil, err
	}

	user, err := s.svc.Update(ctx, id, in)
	if err != nil {
		return nil, toStatus(ctx, err)
	}
	return &userv1.UpdateUserResponse{User: toProtoUser(user)}, nil
}

// DeleteUser implements userv1.UserServiceServer.
func (s *UserServer) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	bad := s.newBadRequest()
	id := bad.uuid("id", req.GetId())
	if err := bad.err(); err != nil {
		return nil, err
	}

	if err := s.svc.Delete(ctx, id); err != nil {
		return nil, toStatus(ctx, err)
	}
	return &userv1.DeleteUserResponse{}, nil
}

func toProtoUser(u domain.User) *userv1.User {
	return &userv1.User{
		Id:         u.ID.String(),
		Name:       u.Name,
		Email:      u.Email,
		CreateTime: timestamppb.New(u.CreatedAt),
		UpdateTime: timestamppb.New(u.UpdatedAt),
	}
}
