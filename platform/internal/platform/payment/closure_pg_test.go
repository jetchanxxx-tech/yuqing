package payment

import (
	"context"
	"errors"
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

type closurePrecreateProvider struct {
	*FakeProvider
	store    *PGStore
	admitted bool
	calls    int
}

func (p *closurePrecreateProvider) CreatePayment(ctx context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	p.calls++
	o, err := p.store.Get(ctx, req.OrderID)
	p.admitted = err == nil && o.State == StatePending
	return nil, errors.New("fixture: response lost after possible acceptance")
}
func TestPaymentPGRecordsPendingOrderBeforeSupplierAndKeepsUnknownOutcome(t *testing.T) {
	pool := pgtest.Pool(t, "payment_closure_precreate")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES('owner','owner@example.invalid','hash','active'); INSERT INTO tenants(id,name,slug,db_name,status) VALUES('tenant','Team','team','team','active'); INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('tenant','owner','tenant_admin')`); err != nil {
		t.Fatal(err)
	}
	store := NewPGStore(pool)
	provider := &closurePrecreateProvider{FakeProvider: NewFakeProvider(ChannelAlipay), store: store}
	svc := NewService(store, newFakeCredits(), map[string]Provider{ChannelAlipay: provider}, nil)
	if _, err := svc.Create(ctx, "tenant", "lite", ChannelAlipay); err == nil {
		t.Fatal("unknown supplier outcome incorrectly succeeded")
	}
	if !provider.admitted {
		t.Fatal("supplier dispatch occurred before a durable pending order existed; closure could miss the in-flight payment")
	}
	orders, err := store.List(ctx, "tenant", 10)
	if err != nil || len(orders) != 1 || orders[0].State != StatePending || orders[0].Granted {
		t.Fatalf("unknown outcome lost financial blocker: %+v %v", orders, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET status='closure_pending'`); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.Create(ctx, "tenant", "lite", ChannelAlipay)
	if provider.calls != 1 {
		t.Fatal("restricted account dispatched another supplier request")
	}
}
