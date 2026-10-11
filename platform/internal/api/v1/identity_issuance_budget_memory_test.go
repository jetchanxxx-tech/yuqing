package v1_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/yuqing/platform/internal/api"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/auth"
)

func TestIdentityIssuanceMemorySharesAttemptsAcrossPurposesAndReconstruction(t *testing.T) {
	router, deps := newContractEnv(t)
	fixture := contractFixture(t)
	_, _, user := mustRegister(t, router, "memory-issuance@example.invalid", "Memory issuance")
	if err := fixture.store.UpdatePassword(context.Background(), user["user_id"].(string), issuanceEntryHash, 0); err != nil {
		t.Fatal(err)
	}
	pair, err := auth.GenerateTokenPair(auth.Principal{UserID: user["user_id"].(string), TenantID: user["tenant_id"].(string), TokenVersion: 1}, testJWTSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	verifications := auth.NewMemoryVerificationStore()
	box := &identitySandbox{}
	cfg := &config.Config{}
	cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL = testJWTSecret, "15m", "720h"
	rebuild := func() {
		deps.Auth = auth.NewService(fixture.store, testJWTSecret, "15m", "720h")
		deps.Auth.EnableUserCenter(fixture.store, verifications, identitySMS{box}, box, "https://example.invalid")
		router = api.NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	}
	rebuild()
	e := &billingActorPGEnv{router: router}
	for i := 0; i < 22; i++ {
		if i == 10 {
			rebuild()
			e.router = router
		}
		path, target := issuanceRoute(i)
		w := issuanceBudgetRequest(context.Background(), e, pair.AccessToken, path, "wrong", target, "192.0.2.77:10000", i+1)
		want := http.StatusInternalServerError
		if i >= 20 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Errorf("memory reconstructed KDF attempts request%d status=%d want=%d", i+1, w.Code, want)
		}
	}
	if w := issuanceBudgetRequest(context.Background(), e, pair.AccessToken, "/user/email-change/request", "wrong", "rotated@example.invalid", "192.0.2.88:10000", 99); w.Code != 429 {
		t.Errorf("memory immutable-account budget bypass=%d", w.Code)
	}
	if box.count() != 0 {
		t.Fatal("limited/wrong verifier sent a sandbox message")
	}
}
