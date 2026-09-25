// Package auth provides JWT authentication, password hashing, and RBAC.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

// Argon2id parameters — tuned for login latency < 500ms on modern hardware.
const (
	argon2Time    = 3
	argon2Memory  = 64 * 1024 // 64 MiB
	argon2Threads = 4
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// Principal is the authenticated user/tenant context extracted from a JWT.
type Principal struct {
	UserID       string   `json:"uid"`
	TenantID     string   `json:"tid"`
	Email        string   `json:"email"`
	Roles        []string `json:"roles"`
	PlanCode     string   `json:"plan"`
	TenantStatus string   `json:"ts"`
}

// TokenPair contains access and refresh tokens.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// jwtClaims carries the principal in JWT claims.
type jwtClaims struct {
	jwt.RegisteredClaims
	UserID       string   `json:"uid"`
	TenantID     string   `json:"tid"`
	Email        string   `json:"email"`
	Roles        []string `json:"roles"`
	PlanCode     string   `json:"plan"`
	TenantStatus string   `json:"ts"`
}

// HashPassword returns an argon2id hash of the password in encoded form:
// "$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>".
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2Memory, argon2Time, argon2Threads, b64Salt, b64Hash)
	return encoded, nil
}

// VerifyPassword compares a password against an encoded argon2id hash.
func VerifyPassword(encodedHash, password string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var memory, timeCost, threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, timeCost, memory, uint8(threads), uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(computed, expectedHash) == 1
}

// GenerateTokenPair creates an access token (short-lived) and a refresh token (long-lived).
func GenerateTokenPair(p Principal, secret string, accessTTL, refreshTTL string) (*TokenPair, error) {
	accessDur, err := time.ParseDuration(accessTTL)
	if err != nil {
		return nil, fmt.Errorf("auth: invalid accessTTL: %w", err)
	}
	refreshDur, err := time.ParseDuration(refreshTTL)
	if err != nil {
		return nil, fmt.Errorf("auth: invalid refreshTTL: %w", err)
	}

	now := time.Now()
	accessToken, err := signToken(p, secret, now, accessDur)
	if err != nil {
		return nil, err
	}
	refreshToken, err := signToken(p, secret, now, refreshDur)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func signToken(p Principal, secret string, now time.Time, ttl time.Duration) (string, error) {
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        generateJTI(),
			Subject:   p.UserID,
		},
		UserID:       p.UserID,
		TenantID:     p.TenantID,
		Email:        p.Email,
		Roles:        p.Roles,
		PlanCode:     p.PlanCode,
		TenantStatus: p.TenantStatus,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims)
	return token.SignedString([]byte(secret))
}

func generateJTI() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ValidateAccessToken parses and validates an access token, returning the Principal.
func ValidateAccessToken(tokenString, secret string) (*Principal, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwtClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		},
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: invalid token: %w", err)
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: invalid claims")
	}

	return &Principal{
		UserID:       claims.UserID,
		TenantID:     claims.TenantID,
		Email:        claims.Email,
		Roles:        claims.Roles,
		PlanCode:     claims.PlanCode,
		TenantStatus: claims.TenantStatus,
	}, nil
}

// RBAC: role → permission matrix.
var rolePermissions = map[string][]string{
	"platform_admin": {
		"analyses:create", "analyses:list", "analyses:read", "analyses:cancel", "analyses:rerun", "analyses:delete",
		"reports:read", "reports:download",
		"members:manage", "members:invite",
		"billing:read", "billing:manage",
		"admin:tenants:list", "admin:tenants:suspend", "admin:tenants:provision",
		"admin:plans:manage",
		"apikeys:manage",
	},
	"tenant_admin": {
		"analyses:create", "analyses:list", "analyses:read", "analyses:cancel", "analyses:rerun", "analyses:delete",
		"reports:read", "reports:download",
		"members:manage", "members:invite",
		"billing:read",
		"apikeys:manage",
	},
	"analyst": {
		"analyses:create", "analyses:list", "analyses:read", "analyses:cancel",
		"reports:read", "reports:download",
	},
	"viewer": {
		"analyses:list", "analyses:read",
		"reports:read",
	},
	// api_service is the least-privilege role granted to API-key principals
	// (machine-to-machine): the read/create surface, never key management.
	"api_service": {
		"analyses:create", "analyses:list", "analyses:read",
		"reports:read", "reports:download",
	},
}

// RoleHasPermission checks if a role grants a specific permission.
func RoleHasPermission(role, permission string) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == permission {
			return true
		}
	}
	return false
}
