// Package auth issues and verifies the JWTs used by the REST API and carries the
// authenticated user through context.Context.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"atlas-backend-core/internal/config"
)

// Token types, stored in the "typ" claim so a refresh token is never accepted as an access
// token (and vice versa).
const (
	typeAccess  = "access"
	typeRefresh = "refresh"
)

// ErrInvalidToken is returned for any token that is malformed, expired, forged, or of the
// wrong type. Callers must not reveal which.
var ErrInvalidToken = errors.New("invalid token")

// TokenPair is the result of a login or refresh.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

type claims struct {
	Type string `json:"typ"`
	jwt.RegisteredClaims
}

// TokenManager signs and verifies HS256 JWTs.
type TokenManager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

// NewTokenManager returns a TokenManager configured from cfg.
func NewTokenManager(cfg config.AuthConfig) *TokenManager {
	return &TokenManager{
		secret:     []byte(cfg.JWTSecret),
		issuer:     cfg.JWTIssuer,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Issue returns a new access/refresh token pair for userID.
func (m *TokenManager) Issue(userID uuid.UUID) (TokenPair, error) {
	now := m.now()
	access, accessExp, err := m.sign(userID, typeAccess, now, m.accessTTL)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, refreshExp, err := m.sign(userID, typeRefresh, now, m.refreshTTL)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// ParseAccess verifies an access token and returns its user ID.
func (m *TokenManager) ParseAccess(token string) (uuid.UUID, error) {
	return m.parse(token, typeAccess)
}

// ParseRefresh verifies a refresh token and returns its user ID. The caller must also
// compare HashToken(token) with the stored hash, which makes refresh tokens revocable.
func (m *TokenManager) ParseRefresh(token string) (uuid.UUID, error) {
	return m.parse(token, typeRefresh)
}

func (m *TokenManager) sign(userID uuid.UUID, typ string, now time.Time, ttl time.Duration) (string, time.Time, error) {
	exp := now.Add(ttl)
	c := claims{
		Type: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			// A unique ID makes every refresh token distinct, even within the same second.
			ID: uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign %s token: %w", typ, err)
	}
	return signed, exp, nil
}

func (m *TokenManager) parse(token, wantType string) (uuid.UUID, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return m.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || c.Type != wantType {
		return uuid.Nil, ErrInvalidToken
	}
	id, err := uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return id, nil
}

// HashToken returns the hex SHA-256 of a token, the form in which refresh tokens are
// stored so a database leak does not expose usable tokens.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type userIDKey struct{}

// WithUserID returns a copy of ctx carrying the authenticated user's ID.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey{}, id)
}

// UserIDFromContext returns the authenticated user's ID, if any.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}
