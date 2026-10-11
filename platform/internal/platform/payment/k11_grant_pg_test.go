package payment

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/platform/credit"
)

func k11GrantFixture(t *testing.T) (*pgxpool.Pool, *Service, *FakeProvider, *credit.Service) {
	t.Helper()
	pool := pgtest.Pool(t, "k11_atomic_grant")
	if _, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,password_hash,name) VALUES('buyer','buyer@example.invalid','hash','Buyer'); INSERT INTO tenants(id,name,slug,db_name,status) VALUES('team','Team','team','team','active'); INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('team','buyer','tenant_admin'); INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('team',0,'free')`); err != nil {
		t.Fatal(err)
	}
	credits := credit.NewService(credit.NewPGStore(pool))
	provider := NewFakeProvider(ChannelAlipay)
	s := NewService(NewPGStore(pool), credits, map[string]Provider{ChannelAlipay: provider}, nil)
	return pool, s, provider, credits
}

func TestK11PGVerifiedGrantRollsBackEntitlementLedgerAndGrantedTogether(t *testing.T) {
	pool, s, provider, credits := k11GrantFixture(t)
	ctx := context.Background()
	o := createOrder(t, s, "team", "lite", ChannelAlipay)
	provider.CallbackResult = &CallbackResult{OrderID: o.ID, ProviderTxnID: "k11-transaction", PaidCents: o.AmountCents, Success: true}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_k11_granted() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.granted AND NOT OLD.granted THEN RAISE EXCEPTION 'isolated grant commit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_k11_granted BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION reject_k11_granted()`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.HandleCallback(ctx, ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatal(err)
	}
	current, err := s.store.Get(ctx, o.ID)
	if err != nil || current.State != StatePaid || current.Granted {
		t.Fatalf("actual payment fact lost or failed grant accepted: %+v %v", current, err)
	}
	poolState, err := credits.Snapshot(ctx, "team")
	var ledgers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_transactions WHERE order_id=$1`, o.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if err != nil || poolState.Balance != 0 || poolState.PlanCode != "free" || ledgers != 0 {
		t.Fatalf("failed final grant left partial credit/plan/ledger: %+v ledgers=%d err=%v", poolState, ledgers, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_k11_granted ON orders`); err != nil {
		t.Fatal(err)
	}
	provider.QueryResult = &QueryResult{TradeState: StatePaid, ProviderTxnID: "k11-transaction", PaidCents: o.AmountCents}
	if _, err := s.Reconcile(ctx, "team", o.ID); err != nil {
		t.Fatal(err)
	}
	poolState, err = credits.Snapshot(ctx, "team")
	var source string
	if err := pool.QueryRow(ctx, `SELECT plan_source FROM report_credits WHERE tenant_id='team'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if err != nil || poolState.Balance != o.Credits || poolState.PlanCode != "lite" || source != "purchase" {
		t.Fatalf("verified retry lost actual catalog grant/source: %+v source=%s err=%v", poolState, source, err)
	}
}

func TestK11PGOldPartialPurchaseReplayCannotOverwriteLaterPlan(t *testing.T) {
	pool, s, provider, credits := k11GrantFixture(t)
	ctx := context.Background()
	o := createOrder(t, s, "team", "lite", ChannelAlipay)
	// A pre-upgrade crash after credit commit left paid + a real purchase ledger
	// without granted. A later plan mutation shares the pool version boundary.
	if won, err := s.store.ClaimPaid(ctx, o.ID, "legacy-paid"); err != nil || !won {
		t.Fatal(err)
	}
	if err := credits.GrantPurchase(ctx, "team", o.ID, o.Credits); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE report_credits SET plan_code='pro' WHERE tenant_id='team'`); err != nil {
		t.Fatal(err)
	}
	before, err := credits.Snapshot(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	provider.QueryResult = &QueryResult{TradeState: StatePaid, ProviderTxnID: "legacy-paid", PaidCents: o.AmountCents}
	for i := 0; i < 2; i++ {
		if _, err := s.Reconcile(ctx, "team", o.ID); err != nil {
			t.Fatal(err)
		}
	}
	after, err := credits.Snapshot(ctx, "team")
	if err != nil || after.Balance != before.Balance || after.PlanCode != "pro" || after.Version != before.Version {
		t.Fatalf("old order replay overwrote later entitlement: before=%+v after=%+v err=%v", before, after, err)
	}
	current, err := s.store.Get(ctx, o.ID)
	if err != nil || !current.Granted {
		t.Fatalf("retained old credit fact was not reconciled: %+v %v", current, err)
	}
	newOrder := createOrder(t, s, "team", "lite", ChannelAlipay)
	provider.CallbackResult = &CallbackResult{OrderID: newOrder.ID, ProviderTxnID: "new-paid", PaidCents: newOrder.AmountCents, Success: true}
	if _, _, _, err := s.HandleCallback(ctx, ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatal(err)
	}
	after, err = credits.Snapshot(ctx, "team")
	if err != nil || after.PlanCode != newOrder.SKUCode || after.Balance != before.Balance+newOrder.Credits {
		t.Fatalf("new independent verified purchase did not apply: %+v %v", after, err)
	}
}
