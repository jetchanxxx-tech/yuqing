package accountclosure

import (
	"context"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

type completionPort interface {
	Process(context.Context, string, int) (*Status, error)
}

func TestClosurePGExecutionWaitsAndRechecksFreshBlockers(t *testing.T) {
	pool := pgtest.Pool(t, "accountclosure")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name) VALUES('closing','closing@example.invalid','original','Owner');
INSERT INTO tenants(id,name,slug,db_name,status) VALUES('sole','Sole','sole','sole','active');
INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('sole','closing','tenant_admin');
INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at) VALUES('old-order','sole','free','plan',1,1,'test','closed',now()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewPGStore(pool)
	if _, err = s.Request(ctx, Actor{UserID: "closing", PasswordHash: "original"}, []string{"sole"}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(completionPort)
	if !ok {
		t.Fatal("resumable closure execution is unavailable")
	}
	r, err := p.Process(ctx, "closing", 1)
	if err != nil || r.State != "pending" {
		t.Fatalf("early execution: %+v %v", r, err)
	}
	_, err = pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'; UPDATE orders SET state='refund_needed' WHERE id='old-order'`)
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.Process(ctx, "closing", 1)
	if err != nil || r.State != "pending" || r.LastError != "UNSETTLED_ORDERS" {
		t.Fatalf("fresh blocker: %+v %v", r, err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM users WHERE id='closing'`).Scan(&status); err != nil || status != "closure_pending" {
		t.Fatalf("blocked identity: %s %v", status, err)
	}
	if _, err = s.Cancel(ctx, Actor{UserID: "closing", Version: 1, PasswordHash: "original"}); err == nil {
		t.Fatal("expired withdrawal succeeded")
	}
}
