package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestHashPassword_producesDifferentFromInput(t *testing.T) {
	hash, err := HashPassword("my-secret-password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
	if hash == "my-secret-password" {
		t.Fatal("hash must not equal the plaintext password")
	}
}

func TestHashPassword_sameInputDifferentHashes(t *testing.T) {
	h1, _ := HashPassword("same-password")
	h2, _ := HashPassword("same-password")
	if h1 == h2 {
		t.Fatal("same password should produce different hashes (unique salt)")
	}
}

func TestVerifyPassword_correctPassword(t *testing.T) {
	const password = "correct-horse-battery-staple"
	hash, _ := HashPassword(password)

	if !VerifyPassword(hash, password) {
		t.Fatal("VerifyPassword should return true for correct password")
	}
}

func TestVerifyPassword_wrongPassword(t *testing.T) {
	hash, _ := HashPassword("right-password")
	if VerifyPassword(hash, "wrong-password") {
		t.Fatal("VerifyPassword should return false for wrong password")
	}
}

func TestGenerateTokenPair_producesTwoTokens(t *testing.T) {
	pair, err := GenerateTokenPair(Principal{
		UserID:   "user_01ABC",
		TenantID: "tenant_01XYZ",
		Email:    "alice@example.com",
		Roles:    []string{"analyst"},
	}, "test-jwt-secret-key-min-32-chars!!", "15m", "720h")
	if err != nil {
		t.Fatalf("GenerateTokenPair failed: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if pair.RefreshToken == "" {
		t.Fatal("refresh token is empty")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access and refresh tokens should be different")
	}
}

func TestValidateAccessToken_roundTrip(t *testing.T) {
	secret := "test-jwt-secret-key-min-32-chars!!"
	principal := Principal{
		UserID:   "user_01DEF",
		TenantID: "tenant_01UVW",
		Email:    "bob@example.com",
		Roles:    []string{"platform_admin"},
	}
	pair, err := GenerateTokenPair(principal, secret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}

	got, err := ValidateAccessToken(pair.AccessToken, secret)
	if err != nil {
		t.Fatalf("ValidateAccessToken failed: %v", err)
	}
	if got.UserID != principal.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, principal.UserID)
	}
	if got.TenantID != principal.TenantID {
		t.Errorf("TenantID = %q, want %q", got.TenantID, principal.TenantID)
	}
	if got.TokenVersion != 0 || got.AuthType != "jwt" {
		t.Errorf("unexpected signed identity: %+v", got)
	}
	if got.Email != "" || len(got.Roles) != 0 || got.PlanCode != "" || got.TenantStatus != "" || got.MemberExists {
		t.Errorf("token validation must not grant authorization: %+v", got)
	}
}

func TestValidateAccessTokenRejectsOtherAlgorithmAndInvalidClaims(t *testing.T) {
	tests := []struct {
		name   string
		method jwt.SigningMethod
		alter  func(jwt.MapClaims)
	}{
		{"hs384", jwt.SigningMethodHS384, func(jwt.MapClaims) {}},
		{"hs512", jwt.SigningMethodHS512, func(jwt.MapClaims) {}},
		{"missing_exp", jwt.SigningMethodHS256, func(c jwt.MapClaims) { delete(c, "exp") }},
		{"wrong_subject", jwt.SigningMethodHS256, func(c jwt.MapClaims) { c["sub"] = "other-user" }},
		{"negative_version", jwt.SigningMethodHS256, func(c jwt.MapClaims) { c["token_version"] = -1 }},
		{"null_version", jwt.SigningMethodHS256, func(c jwt.MapClaims) { c["token_version"] = nil }},
		{"fractional_version", jwt.SigningMethodHS256, func(c jwt.MapClaims) { c["token_version"] = 0.5 }},
		{"unknown_kind", jwt.SigningMethodHS256, func(c jwt.MapClaims) { c["token_kind"] = "session" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := jwt.MapClaims{"uid": "u1", "sub": "u1", "tid": "t1", "exp": time.Now().Add(time.Hour).Unix(), "token_kind": "access", "token_version": 0}
			tt.alter(claims)
			token, err := jwt.NewWithClaims(tt.method, claims).SignedString([]byte(testSecret))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateAccessToken(token, testSecret); err == nil {
				t.Fatal("accepted invalid security claims or signing algorithm")
			}
		})
	}
}

func TestValidateAccessToken_invalidToken_returnsError(t *testing.T) {
	secret := "test-jwt-secret-key-min-32-chars!!"
	_, err := ValidateAccessToken("not-a-valid-token", secret)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestRoleHasPermission(t *testing.T) {
	tests := []struct {
		role       string
		permission string
		want       bool
	}{
		{"platform_admin", "analyses:create", true},
		{"platform_admin", "admin:tenants:list", true},
		{"tenant_admin", "analyses:create", true},
		{"tenant_admin", "members:manage", true},
		{"tenant_admin", "admin:tenants:list", false}, // platform-only
		{"analyst", "analyses:create", true},
		{"analyst", "analyses:list", true},
		{"analyst", "members:manage", false},
		{"analyst", "admin:tenants:list", false},
		{"viewer", "analyses:list", true},
		{"viewer", "analyses:create", false},
		{"viewer", "members:manage", false},
		{"viewer", "admin:tenants:list", false},
		{"unknown_role", "analyses:create", false},
	}

	for _, tt := range tests {
		got := RoleHasPermission(tt.role, tt.permission)
		if got != tt.want {
			t.Errorf("RoleHasPermission(%q, %q) = %v, want %v",
				tt.role, tt.permission, got, tt.want)
		}
	}
}
