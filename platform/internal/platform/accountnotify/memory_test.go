package accountnotify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/accountnotify"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
)

type sender struct {
	mu               sync.Mutex
	calls            int
	entered, release chan struct{}
}

func (s *sender) Send(ctx context.Context, to, subject, body string) error {
	if s.entered != nil {
		close(s.entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.release:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil
}
func memoryFixture(t *testing.T) (*accountnotify.MemoryStore, *auth.SharedTenantStore) {
	t.Helper()
	ctx := context.Background()
	teams := tenant.NewMemoryStore()
	users := auth.NewSharedTenantStore(auth.NewMemoryStore(), teams)
	now := time.Now()
	if err := users.CreateUser(ctx, auth.User{ID: "owner", Email: "owner@example.invalid", PasswordHash: "hash", Status: "active", EmailVerifiedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := teams.Create(ctx, tenant.Tenant{ID: "team", Name: "Team", Slug: "team", DBName: "team", Status: tenant.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if err := users.CreateMember(ctx, auth.Member{TenantID: "team", UserID: "owner", Role: "tenant_admin"}); err != nil {
		t.Fatal(err)
	}
	return accountnotify.NewMemoryStore(users, teams), users
}

func TestMemoryPreferencesAndTerminalEventNeverResend(t *testing.T) {
	store, _ := memoryFixture(t)
	box := &sender{}
	svc := accountnotify.NewService(store, box)
	ctx := context.Background()
	actor := auth.Principal{UserID: "owner", TokenVersion: 0, AuthType: "jwt"}
	if err := svc.SetPreferences(ctx, actor, accountnotify.Preferences{TaskCompleted: false}); err != nil {
		t.Fatal(err)
	}
	store.Enqueue("owner", "team", "analysis", "first-completion")
	if err := svc.Dispatch(ctx, 25); err != nil || box.calls != 0 {
		t.Fatal("opted-out event sent")
	}
	if err := svc.SetPreferences(ctx, actor, accountnotify.Preferences{TaskCompleted: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(ctx, 25); err != nil || box.calls != 0 {
		t.Fatal("preference retry reactivated cancelled event")
	}
	store.Enqueue("owner", "team", "analysis", "second-completion")
	store.Enqueue("owner", "team", "analysis", "second-completion")
	if err := svc.Dispatch(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(ctx, 25); err != nil || box.calls != 1 {
		t.Fatal("completion/receipt replay resent accepted event")
	}
	if err := svc.SetPreferences(ctx, auth.Principal{UserID: "owner", TokenVersion: 1, AuthType: "jwt"}, accountnotify.Preferences{}); err == nil {
		t.Fatal("stale original preference actor mutated")
	}
}

func TestMemoryClosureGenerationPreservesInFlightAttemptAndCancelsOldUnsentWork(t *testing.T) {
	store, users := memoryFixture(t)
	box := &sender{entered: make(chan struct{}), release: make(chan struct{})}
	svc := accountnotify.NewService(store, box)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store.Enqueue("owner", "team", "analysis", "inflight")
	done := make(chan error, 1)
	go func() { done <- svc.Dispatch(ctx, 1) }()
	select {
	case <-box.entered:
	case <-ctx.Done():
		t.Fatal("sender never reached boundary")
	}
	if store.CanClose("owner") {
		t.Fatal("in-flight attempt was invisible to closure")
	}
	store.Enqueue("owner", "team", "analysis", "unsent")
	store.CancelPending("owner")
	if err := users.UpdateAdministration(ctx, func(state *auth.AdministrationState) error {
		u := state.Users["owner"]
		u.Status = "closure_pending"
		u.TokenVersion++
		state.Users["owner"] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if store.CanClose("owner") {
		t.Fatal("cancellation erased processing evidence")
	}
	close(box.release)
	if err := <-done; err != nil {
		t.Fatal("closure invalidated original receipt")
	}
	if !store.CanClose("owner") {
		t.Fatal("recorded outcome did not clear in-flight blocker")
	}
	if err := users.UpdateAdministration(ctx, func(state *auth.AdministrationState) error {
		u := state.Users["owner"]
		u.Status = "active"
		u.TokenVersion++
		state.Users["owner"] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store.Resume("owner")
	if err := svc.Dispatch(ctx, 25); err != nil || box.calls != 1 {
		t.Fatal("withdrawal resumed old invalidated unsent event")
	}
}
