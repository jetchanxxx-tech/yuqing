package auth

import (
	"context"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"github.com/yuqing/platform/internal/platform/tenant"
	"testing"
	"time"
)

func TestClosureMemoryCompletionRetriesBusinessRetentionAfterAnonymization(t *testing.T) {
	ctx := context.Background()
	users := NewSharedTenantStore(NewMemoryStore(), tenant.NewMemoryStore())
	v := NewMemoryVerificationStore()
	v.users = users.MemoryStore
	if err := users.RegisterAccount(ctx, User{ID: "closing", Email: "closing@example.invalid", PasswordHash: "hash", Name: "Private"}, Tenant{ID: "sole", Name: "Private Team", Slug: "sole", DBName: "sole", Status: "active", PlanCode: "free"}, Member{TenantID: "sole", UserID: "closing", Role: "tenant_admin"}, false); err != nil {
		t.Fatal(err)
	}
	m := NewMemoryClosureStore(users, v, func(_ context.Context, _ string, _ bool, fn func(func([]string)) error) error {
		return fn(func([]string) {})
	}, func(context.Context, string) (string, []string, error) { return "free", nil, nil }, func(string, []string, bool) {}, storage.NewLocalAvatar(t.TempDir()))
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	if _, err := m.Request(ctx, accountclosure.Actor{UserID: "closing", PasswordHash: "hash"}, []string{"sole"}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(m).(interface {
		SetBusinessCleanup(func(context.Context, []accountclosure.TenantImpact, int) (bool, error))
	})
	if !ok {
		t.Fatal("resumable memory business retention integration unavailable")
	}
	ready := false
	calls := 0
	p.SetBusinessCleanup(func(_ context.Context, impacts []accountclosure.TenantImpact, _ int) (bool, error) {
		calls++
		if len(impacts) != 1 || impacts[0].ID != "sole" || impacts[0].RetentionDays == nil || *impacts[0].RetentionDays != 30 {
			t.Fatal("cleanup lost confirmed catalog scope")
		}
		return ready, nil
	})
	now = now.Add(168*time.Hour + time.Second)
	r, err := m.Process(ctx, "closing", 100)
	if err != nil || r.State != "completed" || r.CleanupStatus != "retention_pending" {
		t.Fatalf("memory completion misreported retained business files: %+v %v", r, err)
	}
	if _, err = users.GetUserByEmail(ctx, "closing@example.invalid"); err == nil {
		t.Fatal("anonymization retained reusable identity")
	}
	ready = true
	r, err = m.Process(ctx, "closing", 100)
	if err != nil || r.CleanupStatus != "complete" || calls != 2 {
		t.Fatalf("completed account did not resume business cleanup: %+v %v calls=%d", r, err, calls)
	}
}
