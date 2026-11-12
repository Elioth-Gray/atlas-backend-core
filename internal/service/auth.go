package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"atlas-backend-core/internal/apperror"
	"atlas-backend-core/internal/auth"
	"atlas-backend-core/internal/domain"
)

// bcrypt only uses the first 72 bytes of a password; longer ones are rejected instead of
// silently truncated.
const (
	minPasswordLength = 8
	maxPasswordBytes  = 72
)

// dummyHash is compared against when the email is unknown, so login takes the same time
// whether or not the account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("atlas-timing-equalizer"), bcrypt.DefaultCost)

var errInvalidCredentials = apperror.Unauthorized("invalid email or password")

// AuthUserRepository is the user persistence AuthService needs.
type AuthUserRepository interface {
	Create(ctx context.Context, user domain.User) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetByEmail(ctx context.Context, email string) (domain.User, error)
	SetRefreshTokenHash(ctx context.Context, id uuid.UUID, hash, expected *string) error
}

// ShopCreator creates the owner's first shop during registration.
type ShopCreator interface {
	CategoryExists(ctx context.Context, id uuid.UUID) (bool, error)
	ScaleExists(ctx context.Context, id uuid.UUID) (bool, error)
	Create(ctx context.Context, shop domain.Shop) (domain.Shop, error)
	AddMember(ctx context.Context, m domain.ShopUser) error
}

// TokenIssuer issues and verifies JWTs. *auth.TokenManager satisfies it.
type TokenIssuer interface {
	Issue(userID uuid.UUID) (auth.TokenPair, error)
	ParseRefresh(token string) (uuid.UUID, error)
}

// RegisterInput holds a new owner account and their shop.
type RegisterInput struct {
	Name        string
	Email       string
	PhoneNumber *string
	Password    string
	Shop        ShopInput
}

// ShopInput holds a shop profile.
type ShopInput struct {
	Name        string
	Description *string
	Website     *string
	Image       *string
	CategoryID  uuid.UUID
	ScaleID     uuid.UUID
}

// AuthResult is returned by register, login, and refresh.
type AuthResult struct {
	User   domain.User
	Shop   *domain.Shop
	Tokens auth.TokenPair
}

// AuthService implements registration, login, and token refresh.
type AuthService struct {
	users  AuthUserRepository
	shops  ShopCreator
	tx     TxRunner
	tokens TokenIssuer
}

// NewAuthService returns an AuthService.
func NewAuthService(users AuthUserRepository, shops ShopCreator, tx TxRunner, tokens TokenIssuer) *AuthService {
	return &AuthService{users: users, shops: shops, tx: tx, tokens: tokens}
}

// Register creates an owner account together with their shop (the user becomes its
// Owner) and logs them in.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (AuthResult, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return AuthResult{}, err
	}
	if err := validatePassword(in.Password); err != nil {
		return AuthResult{}, err
	}
	shopIn, err := normalizeShopInput(ctx, s.shops, in.Shop)
	if err != nil {
		return AuthResult{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResult{}, fmt.Errorf("hash password: %w", err)
	}
	hashStr := string(hash)

	var res AuthResult
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		user, err := s.users.Create(ctx, domain.User{
			Name:         name,
			Email:        normalizeEmail(in.Email),
			PhoneNumber:  optionalText(in.PhoneNumber),
			PasswordHash: &hashStr,
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		shop, err := s.shops.Create(ctx, domain.Shop{
			Name:           shopIn.Name,
			Description:    shopIn.Description,
			Website:        shopIn.Website,
			Image:          shopIn.Image,
			ShopCategoryID: shopIn.CategoryID,
			ShopScaleID:    shopIn.ScaleID,
		})
		if err != nil {
			return fmt.Errorf("create shop: %w", err)
		}
		if err := s.shops.AddMember(ctx, domain.ShopUser{Role: domain.ShopRoleOwner, UserID: user.ID, ShopID: shop.ID}); err != nil {
			return fmt.Errorf("add shop owner: %w", err)
		}
		tokens, err := s.issue(ctx, user.ID, nil)
		if err != nil {
			return err
		}
		res = AuthResult{User: user, Shop: &shop, Tokens: tokens}
		return nil
	})
	if err != nil {
		return AuthResult{}, fmt.Errorf("register: %w", err)
	}
	return res, nil
}

// Login verifies email and password and issues a new token pair. Any previous refresh
// token stops working.
func (s *AuthService) Login(ctx context.Context, email, password string) (AuthResult, error) {
	user, err := s.users.GetByEmail(ctx, normalizeEmail(email))
	if err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
			return AuthResult{}, errInvalidCredentials
		}
		return AuthResult{}, fmt.Errorf("login: %w", err)
	}
	if user.PasswordHash == nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return AuthResult{}, errInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)) != nil {
		return AuthResult{}, errInvalidCredentials
	}
	tokens, err := s.issue(ctx, user.ID, nil)
	if err != nil {
		return AuthResult{}, fmt.Errorf("login: %w", err)
	}
	return AuthResult{User: user, Tokens: tokens}, nil
}

// Refresh exchanges a valid refresh token for a new pair. Refresh tokens are single use:
// the stored hash is rotated atomically, so a replayed token is rejected.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (AuthResult, error) {
	invalid := apperror.Unauthorized("invalid or expired refresh token")
	userID, err := s.tokens.ParseRefresh(refreshToken)
	if err != nil {
		return AuthResult{}, invalid
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			return AuthResult{}, invalid
		}
		return AuthResult{}, fmt.Errorf("refresh: %w", err)
	}
	presented := auth.HashToken(refreshToken)
	if user.RefreshTokenHash == nil || subtle.ConstantTimeCompare([]byte(*user.RefreshTokenHash), []byte(presented)) != 1 {
		return AuthResult{}, invalid
	}
	tokens, err := s.issue(ctx, user.ID, &presented)
	if err != nil {
		if errors.Is(err, apperror.ErrUnauthorized) {
			return AuthResult{}, invalid
		}
		return AuthResult{}, fmt.Errorf("refresh: %w", err)
	}
	return AuthResult{User: user, Tokens: tokens}, nil
}

// issue creates a token pair and stores the refresh token hash (replacing expected when
// set, for atomic rotation).
func (s *AuthService) issue(ctx context.Context, userID uuid.UUID, expected *string) (auth.TokenPair, error) {
	tokens, err := s.tokens.Issue(userID)
	if err != nil {
		return auth.TokenPair{}, fmt.Errorf("issue tokens: %w", err)
	}
	hash := auth.HashToken(tokens.RefreshToken)
	if err := s.users.SetRefreshTokenHash(ctx, userID, &hash, expected); err != nil {
		return auth.TokenPair{}, fmt.Errorf("store refresh token: %w", err)
	}
	return tokens, nil
}

// masterDataChecker verifies category and scale IDs.
type masterDataChecker interface {
	// CategoryExists also rejects disabled categories (only Bakery is enabled for now).
	CategoryExists(ctx context.Context, id uuid.UUID) (bool, error)
	ScaleExists(ctx context.Context, id uuid.UUID) (bool, error)
}

// normalizeShopInput trims a shop profile and checks its master data references.
func normalizeShopInput(ctx context.Context, master masterDataChecker, in ShopInput) (ShopInput, error) {
	name, err := requiredText("shop name", in.Name)
	if err != nil {
		return ShopInput{}, err
	}
	ok, err := master.CategoryExists(ctx, in.CategoryID)
	if err != nil {
		return ShopInput{}, err
	}
	if !ok {
		return ShopInput{}, apperror.InvalidInput("unknown or unavailable shop category")
	}
	ok, err = master.ScaleExists(ctx, in.ScaleID)
	if err != nil {
		return ShopInput{}, err
	}
	if !ok {
		return ShopInput{}, apperror.InvalidInput("unknown shop scale")
	}
	return ShopInput{
		Name:        name,
		Description: optionalText(in.Description),
		Website:     optionalText(in.Website),
		Image:       optionalText(in.Image),
		CategoryID:  in.CategoryID,
		ScaleID:     in.ScaleID,
	}, nil
}

func validatePassword(p string) error {
	if len([]rune(p)) < minPasswordLength {
		return apperror.InvalidInput(fmt.Sprintf("password must be at least %d characters long", minPasswordLength))
	}
	if len(p) > maxPasswordBytes {
		return apperror.InvalidInput(fmt.Sprintf("password must be at most %d bytes long", maxPasswordBytes))
	}
	return nil
}
