package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"atlas-backend-core/internal/config"
)

func newManager(secret string, now time.Time) *TokenManager {
	m := NewTokenManager(config.AuthConfig{JWTSecret: secret, JWTIssuer: "atlas", AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour})
	m.now = func() time.Time { return now }
	return m
}

func TestTokenManager(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	userID := uuid.New()
	m := newManager("secret-one", now)
	pair, err := m.Issue(userID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	tests := []struct {
		name    string
		parse   func() (uuid.UUID, error)
		wantErr bool
	}{
		{name: "access token is accepted as access", parse: func() (uuid.UUID, error) { return m.ParseAccess(pair.AccessToken) }},
		{name: "refresh token is accepted as refresh", parse: func() (uuid.UUID, error) { return m.ParseRefresh(pair.RefreshToken) }},
		{name: "refresh token is rejected as access", parse: func() (uuid.UUID, error) { return m.ParseAccess(pair.RefreshToken) }, wantErr: true},
		{name: "access token is rejected as refresh", parse: func() (uuid.UUID, error) { return m.ParseRefresh(pair.AccessToken) }, wantErr: true},
		{name: "expired access token", parse: func() (uuid.UUID, error) {
			return newManager("secret-one", now.Add(16*time.Minute)).ParseAccess(pair.AccessToken)
		}, wantErr: true},
		{name: "other secret", parse: func() (uuid.UUID, error) { return newManager("secret-two", now).ParseAccess(pair.AccessToken) }, wantErr: true},
		{name: "garbage", parse: func() (uuid.UUID, error) { return m.ParseAccess("not.a.jwt") }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.parse()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidToken) {
					t.Fatalf("err = %v, want ErrInvalidToken", err)
				}
				return
			}
			if err != nil || got != userID {
				t.Fatalf("got %s, %v; want %s", got, err, userID)
			}
		})
	}

	if again, _ := m.Issue(userID); again.RefreshToken == pair.RefreshToken {
		t.Error("refresh tokens issued in the same second must differ")
	}
	if HashToken("a") == HashToken("b") || len(HashToken("a")) != 64 {
		t.Error("HashToken must be a hex SHA-256")
	}
}
