package accountadmin

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// This RED contract catches an account-creation implementation that leaves
// the durable administrator audit trail empty even though the account and
// trial grant were committed.
func TestK9CreatePendingWritesCreationAudit(t *testing.T) {
	ctx := context.Background()
	tenants := tenant.NewMemoryStore()
	base := auth.NewMemoryStore()
	accounts := auth.NewSharedTenantStore(base, tenants)
	if err := accounts.RegisterAccount(ctx,
		auth.User{ID: "admin", Email: "admin@example.invalid", Name: "Admin", PasswordHash: "hash", Status: "active", CreatedAt: time.Now()},
		auth.Tenant{ID: "admin-tenant", Name: "Admin team", Slug: "admin-team", DBName: "yuqing_admin", Status: "active", PlanCode: "free"},
		auth.Member{TenantID: "admin-tenant", UserID: "admin", Role: "tenant_admin"}, true); err != nil {
		t.Fatal(err)
	}
	credits := credit.NewService(credit.NewMemoryStore())
	store := NewMemoryStore(accounts, tenants, credits, nil)
	result, err := store.CreatePending(ctx, CreateRequest{ActorID: "admin", ActorTokenVersion: 0, Email: "pending@example.invalid", Name: "Pending"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.User(ctx, result.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.AuditLogs) != 1 || detail.AuditLogs[0].Action != "account.create" {
		t.Fatalf("creation must leave one account.create audit entry, got %#v", detail.AuditLogs)
	}
}
