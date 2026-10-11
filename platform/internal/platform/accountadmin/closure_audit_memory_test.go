package accountadmin

import (
	"context"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
	"testing"
)

func TestClosureMemoryAuditBatchesPreserveLedgerAndRoleHistory(t *testing.T) {
	ctx := context.Background()
	tenants := tenant.NewMemoryStore()
	users := auth.NewSharedTenantStore(auth.NewMemoryStore(), tenants)
	if err := users.RegisterAccount(ctx, auth.User{ID: "closing", Email: "closing@example.invalid", PasswordHash: "fixture"}, auth.Tenant{ID: "team", Name: "Team", Slug: "team", DBName: "team", Status: "active"}, auth.Member{TenantID: "team", UserID: "closing", Role: "tenant_admin"}, false); err != nil {
		t.Fatal(err)
	}
	s := NewMemoryStore(users, tenants, nil, nil)
	for i := 0; i < 8; i++ {
		s.audits = append(s.audits, Audit{ID: int64(i + 1), ActorID: "closing", TargetType: "user", TargetID: "other", Reason: "Private personal reason", RequestID: "private-request", Before: map[string]any{"role": "viewer", "name": "Private name"}, After: map[string]any{"role": "analyst", "ledger_id": "retained-ledger", "balance": 3}})
	}
	for step := 0; step < 3; step++ {
		err := s.WithAccountClosure(ctx, "closing", true, func(redact func([]string, int) (bool, int64)) error {
			done, n := redact(nil, 3)
			if n > 3 || done != (step == 2) {
				t.Fatalf("memory audit budget/progress: done=%v processed=%d step=%d", done, n, step)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range s.audits {
		if a.TargetID != "other" || a.Before["role"] != "viewer" || a.After["role"] != "analyst" || a.After["ledger_id"] != "retained-ledger" || a.Before["name"] != nil || a.RequestID != "" {
			t.Fatalf("memory audit privacy/links: %+v", a)
		}
	}
}
