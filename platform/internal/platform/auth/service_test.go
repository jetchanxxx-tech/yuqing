package auth

import (
	"context"
	"strings"
	"testing"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/llm"
)

const (
	testSecret   = "test-jwt-secret-key-min-32-chars!!"
	testEmail    = "alice@example.com"
	testPassword = "correct-password-1"
	testUserName = "Alice"
	testTeamName = "Alice的团队"
)

func newTestAuthService(t *testing.T) (*Service, *MemoryStore) {
	t.Helper()
	st := NewMemoryStore()
	svc := NewService(st, testSecret, "15m", "720h")
	return svc, st
}

func mustRegister(t *testing.T, svc *Service, email, password, name string) (*Principal, *TokenPair) {
	t.Helper()
	p, pair, err := svc.Register(context.Background(), email, password, name)
	if err != nil {
		t.Fatalf("Register(%q) failed: %v", email, err)
	}
	return p, pair
}

func TestServiceRegister_createsUserTenantMemberQuotaAndTokens(t *testing.T) {
	svc, st := newTestAuthService(t)
	p, pair := mustRegister(t, svc, testEmail, testPassword, testUserName)

	if p.UserID == "" || len(p.UserID) != 26 {
		t.Errorf("UserID = %q, want a 26-char ULID", p.UserID)
	}
	if p.TenantID == "" || len(p.TenantID) != 26 {
		t.Errorf("TenantID = %q, want a 26-char ULID", p.TenantID)
	}
	if p.Email != testEmail {
		t.Errorf("Email = %q, want %q", p.Email, testEmail)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "tenant_admin" {
		t.Errorf("Roles = %v, want [tenant_admin]", p.Roles)
	}
	if p.PlanCode != "free" {
		t.Errorf("PlanCode = %q, want free", p.PlanCode)
	}
	if p.TenantStatus != "active" {
		t.Errorf("TenantStatus = %q, want active", p.TenantStatus)
	}

	// Tokens must be issued and validate against the same secret.
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected non-empty token pair")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Error("access and refresh tokens should differ")
	}
	got, err := ValidateAccessToken(pair.AccessToken, testSecret)
	if err != nil {
		t.Fatalf("issued access token does not validate: %v", err)
	}
	if got.UserID != p.UserID || got.TenantID != p.TenantID {
		t.Errorf("token principal mismatch: got %+v, want %+v", got, p)
	}

	// User persisted with a hashed password (never the plaintext).
	u, err := st.GetUserByEmail(context.Background(), testEmail)
	if err != nil {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}
	if u.PasswordHash == testPassword {
		t.Error("password must not be stored in plaintext")
	}
	if u.Name != testUserName {
		t.Errorf("user name = %q, want %q", u.Name, testUserName)
	}

	// Tenant persisted with the derived team name, slug, db_name, plan and status.
	ten, err := st.GetUserTenant(context.Background(), p.UserID)
	if err != nil {
		t.Fatalf("GetUserTenant failed: %v", err)
	}
	if ten.Name != testTeamName {
		t.Errorf("tenant name = %q, want %q", ten.Name, testTeamName)
	}
	if !strings.HasPrefix(ten.DBName, "yuging_t_") {
		t.Errorf("tenant db_name = %q, want yuging_t_ prefix", ten.DBName)
	}
	if ten.PlanCode != "free" || ten.Status != "active" {
		t.Errorf("tenant plan/status = %q/%q, want free/active", ten.PlanCode, ten.Status)
	}

	// Membership recorded with tenant_admin role.
	role, err := st.GetUserRole(context.Background(), p.TenantID, p.UserID)
	if err != nil {
		t.Fatalf("GetUserRole failed: %v", err)
	}
	if role != "tenant_admin" {
		t.Errorf("member role = %q, want tenant_admin", role)
	}

	// Free-plan quota (1M tokens, hard cap) applied to the tenant meter.
	status, err := svc.meter.BudgetStatus(context.Background(), p.TenantID)
	if err != nil {
		t.Fatalf("BudgetStatus failed: %v", err)
	}
	if status.QuotaTokens != 1_000_000 {
		t.Errorf("QuotaTokens = %d, want 1000000", status.QuotaTokens)
	}
}

func TestServiceRegister_validationErrors(t *testing.T) {
	svc, _ := newTestAuthService(t)

	invalidEmails := []string{"", "not-an-email", "a@b", "a@b.", "a b@example.com", "@example.com"}
	for _, email := range invalidEmails {
		if _, _, err := svc.Register(context.Background(), email, testPassword, testUserName); err == nil {
			t.Errorf("Register with invalid email %q: expected error", email)
		}
	}

	for _, pw := range []string{"", "1234567"} { // < 8 chars
		if _, _, err := svc.Register(context.Background(), testEmail, pw, testUserName); err == nil {
			t.Errorf("Register with %d-char password: expected error", len(pw))
		}
	}

	if _, _, err := svc.Register(context.Background(), testEmail, testPassword, " "); err == nil {
		t.Error("Register with blank name: expected error")
	}

	// Nothing was persisted by failed registers.
	stored, err := svc.store.GetUserByEmail(context.Background(), testEmail)
	if err == nil {
		t.Errorf("expected ErrNotFound after failed registers, got user %+v", stored)
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceRegister_duplicateEmail(t *testing.T) {
	svc, _ := newTestAuthService(t)
	mustRegister(t, svc, testEmail, testPassword, testUserName)

	_, _, err := svc.Register(context.Background(), testEmail, "another-password-9", "Bob")
	if err == nil {
		t.Fatal("expected error for duplicate email registration")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestServiceLogin_success(t *testing.T) {
	svc, _ := newTestAuthService(t)
	want, _ := mustRegister(t, svc, testEmail, testPassword, testUserName)

	p, pair, err := svc.Login(context.Background(), testEmail, testPassword)
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if p.UserID != want.UserID || p.TenantID != want.TenantID || p.Email != want.Email {
		t.Errorf("principal = %+v, want %+v", p, want)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "tenant_admin" {
		t.Errorf("Roles = %v, want [tenant_admin]", p.Roles)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected non-empty token pair from Login")
	}
	if _, err := ValidateAccessToken(pair.AccessToken, testSecret); err != nil {
		t.Errorf("login access token does not validate: %v", err)
	}
}

func TestServiceLogin_wrongPassword(t *testing.T) {
	svc, _ := newTestAuthService(t)
	mustRegister(t, svc, testEmail, testPassword, testUserName)

	_, _, err := svc.Login(context.Background(), testEmail, "wrong-password-x")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceLogin_unknownEmail(t *testing.T) {
	svc, _ := newTestAuthService(t)
	mustRegister(t, svc, testEmail, testPassword, testUserName)

	_, _, err := svc.Login(context.Background(), "nobody@example.com", testPassword)
	if err == nil {
		t.Fatal("expected error for unknown email")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceLogin_upperCaseEmailIsNormalized(t *testing.T) {
	svc, _ := newTestAuthService(t)
	mustRegister(t, svc, testEmail, testPassword, testUserName)

	if _, _, err := svc.Login(context.Background(), "ALICE@example.com", testPassword); err != nil {
		t.Errorf("Login with upper-case email failed: %v", err)
	}
}

func TestServiceAuthenticate(t *testing.T) {
	svc, _ := newTestAuthService(t)
	want, pair := mustRegister(t, svc, testEmail, testPassword, testUserName)

	got, err := svc.Authenticate(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if got.UserID != want.UserID || got.TenantID != want.TenantID || got.Email != want.Email {
		t.Errorf("principal = %+v, want %+v", got, want)
	}

	if _, err := svc.Authenticate(context.Background(), "garbage-token"); err == nil {
		t.Error("expected error for garbage token")
	} else if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}

	// Token signed with another secret must be rejected.
	other, _ := GenerateTokenPair(*want, "another-secret-key-32-chars-len!!", "15m", "720h")
	if _, err := svc.Authenticate(context.Background(), other.AccessToken); err == nil {
		t.Error("expected error for token signed with a different secret")
	} else if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRefresh_reissuesTokenPair(t *testing.T) {
	svc, _ := newTestAuthService(t)
	_, pair := mustRegister(t, svc, testEmail, testPassword, testUserName)

	refreshed, err := svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
		t.Fatal("expected non-empty refreshed pair")
	}
	if refreshed.AccessToken == pair.AccessToken {
		t.Error("refreshed access token should be a new token")
	}
	if _, err := svc.Authenticate(context.Background(), refreshed.AccessToken); err != nil {
		t.Errorf("refreshed access token does not validate: %v", err)
	}
}

func TestServiceRefresh_invalidToken(t *testing.T) {
	svc, _ := newTestAuthService(t)

	if _, err := svc.Refresh(context.Background(), "not-a-refresh-token"); err == nil {
		t.Fatal("expected error for invalid refresh token")
	} else if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRefresh_expiredToken(t *testing.T) {
	st := NewMemoryStore()
	svc := NewService(st, testSecret, "15m", "-1m") // refresh TTL already expired

	p := Principal{UserID: "user1", TenantID: "tenant1", Email: testEmail, Roles: []string{"analyst"}}
	pair, err := GenerateTokenPair(p, testSecret, "15m", "-1m")
	if err != nil {
		t.Fatalf("GenerateTokenPair failed: %v", err)
	}

	if _, err := svc.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("expected error for expired refresh token")
	} else if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRegister_freePlanQuotaHardCapMode(t *testing.T) {
	svc, _ := newTestAuthService(t)
	p, _ := mustRegister(t, svc, testEmail, testPassword, testUserName)

	// Recording 1M+ tokens against the hard cap must leave the meter in exceeded status.
	_ = svc.meter.Record(context.Background(), llm.UsageEvent{
		TenantID:         p.TenantID,
		PromptTokens:     900_000,
		CompletionTokens: 150_000,
	})
	status, err := svc.meter.BudgetStatus(context.Background(), p.TenantID)
	if err != nil {
		t.Fatalf("BudgetStatus failed: %v", err)
	}
	if status.SpentTokens != 1_050_000 {
		t.Errorf("SpentTokens = %d, want 1050000", status.SpentTokens)
	}
	if status.Status != "exceeded" {
		t.Errorf("Status = %q, want exceeded (1M hard cap)", status.Status)
	}
}
