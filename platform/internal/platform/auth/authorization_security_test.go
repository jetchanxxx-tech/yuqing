package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// PG in these test names includes the security contract in the cloud PG gate.
func TestAuthorizationSecurityPGRejectsRefreshTokenAsAccess(t *testing.T) {
	pair, err := GenerateTokenPair(Principal{
		UserID:       "security-user",
		TenantID:     "security-tenant",
		Email:        "security@example.com",
		Roles:        []string{"tenant_admin"},
		PlanCode:     "free",
		TenantStatus: "active",
	}, testSecret, "15m", "720h")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := ValidateAccessToken(pair.AccessToken, testSecret); err != nil {
		t.Fatalf("issued access token must be valid: %v", err)
	}
	if _, err := ValidateAccessToken(pair.RefreshToken, testSecret); err == nil {
		t.Error("ValidateAccessToken accepted a refresh token")
	}
}

func TestAuthorizationSecurityPGRejectsAccessTokenForRefresh(t *testing.T) {
	svc, _ := newTestAuthService(t)
	_, pair := mustRegister(t, svc, "access-refresh@example.com", testPassword, "AccessRefresh")
	ctx := context.Background()
	if _, err := svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("issued refresh token must be valid: %v", err)
	}
	if _, err := svc.Refresh(ctx, pair.AccessToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("Refresh(access token) error = %v, want ErrUnauthorized", err)
	}
}

func TestAuthorizationSecurityPGPasswordChangeRevokesIssuedTokens(t *testing.T) {
	svc, st := newTestAuthService(t)
	svc.EnableUserCenter(st, NewMemoryVerificationStore(), nil, nil, "https://test.example.com")
	p, pair := mustRegister(t, svc, "password-revoke@example.com", testPassword, "PasswordRevoke")
	ctx := context.Background()
	const newPassword = "changed-password-2"

	if err := svc.ChangePassword(ctx, p.UserID, testPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := svc.Authenticate(ctx, pair.AccessToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("Authenticate(old access token) error = %v, want ErrUnauthorized", err)
	}
	if _, err := svc.Refresh(ctx, pair.RefreshToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("Refresh(old refresh token) error = %v, want ErrUnauthorized", err)
	}

	_, freshPair, err := svc.Login(ctx, p.Email, newPassword)
	if err != nil {
		t.Fatalf("Login with changed password: %v", err)
	}
	if _, err := svc.Authenticate(ctx, freshPair.AccessToken); err != nil {
		t.Errorf("Authenticate(new access token): %v", err)
	}
	if _, err := svc.Refresh(ctx, freshPair.RefreshToken); err != nil {
		t.Errorf("Refresh(new refresh token): %v", err)
	}
}

func TestAuthorizationSecurityPGRejectsMissingTokenSecurityClaims(t *testing.T) {
	svc, _ := newTestAuthService(t)
	p, _ := mustRegister(t, svc, "legacy-token@example.com", testPassword, "LegacyToken")
	ctx := context.Background()

	tests := []struct {
		name   string
		claims jwt.MapClaims
	}{
		{name: "legacy_without_kind_or_version"},
		{name: "missing_kind", claims: jwt.MapClaims{"token_version": 0}},
		{name: "missing_version", claims: jwt.MapClaims{"token_kind": "access"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			claims := jwt.MapClaims{
				"sub":   p.UserID,
				"uid":   p.UserID,
				"tid":   p.TenantID,
				"email": p.Email,
				"roles": []string{"tenant_admin"},
				"plan":  "free",
				"ts":    "active",
				"iat":   now.Unix(),
				"exp":   now.Add(15 * time.Minute).Unix(),
				"jti":   "legacy-security-test",
			}
			for key, value := range tt.claims {
				claims[key] = value
			}
			legacyToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
			if err != nil {
				t.Fatalf("sign legacy token: %v", err)
			}
			if _, err := ValidateAccessToken(legacyToken, testSecret); err == nil {
				t.Error("ValidateAccessToken accepted missing token security claims")
			}
			if _, err := svc.Authenticate(ctx, legacyToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Errorf("Authenticate(legacy token) error = %v, want ErrUnauthorized", err)
			}
			if _, err := svc.Refresh(ctx, legacyToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Errorf("Refresh(legacy token) error = %v, want ErrUnauthorized", err)
			}
		})
	}
}
