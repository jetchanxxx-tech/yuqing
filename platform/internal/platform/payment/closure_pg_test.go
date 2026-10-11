package payment

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestPaymentPGClosureFenceRejectsLateOrderButKeepsSettlement(t *testing.T) {
	pool := pgtest.Pool(t, "payment_closure")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES('owner','owner@example.invalid','hash','active'); INSERT INTO tenants(id,name,slug,db_name,status) VALUES('tenant','Team','team','team','active'); INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('tenant','owner','tenant_admin')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewPGStore(pool)
	order := &Order{ID: "before", TenantID: "tenant", SKUCode: "free", Kind: "plan", Credits: 1, AmountCents: 1, Channel: "test", State: StatePending, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
	if err = s.Create(ctx, order); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='closure_pending',token_version=1`); err != nil {
		t.Fatal(err)
	}
	order.ID = "after"
	if err = s.Create(ctx, order); err == nil {
		t.Fatal("a previously admitted request created a new order after account restriction")
	}
	if paid, err := s.ClaimPaid(ctx, "before", "existing-provider-transaction"); err != nil || !paid {
		t.Fatalf("legitimate late settlement lost: %v %v", paid, err)
	}
}
