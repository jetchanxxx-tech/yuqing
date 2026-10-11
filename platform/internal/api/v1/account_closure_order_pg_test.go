package v1_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/payment"
)

func TestClosurePGQueuedOrderRetainsOriginalActorInSharedTenant(t *testing.T) {
	for _, change := range []string{"password", "closure_pending"} {
		t.Run(change, func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			access, _, owner := mustRegister(t, e.router, "queued-closure-order@example.invalid", "Owner")
			_, _, other := mustRegister(t, e.router, "queued-closure-other@example.invalid", "Other")
			uid, tid := owner["user_id"].(string), owner["tenant_id"].(string)
			k9Exec(t, e, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'tenant_admin')`, tid, other["user_id"])
			provider := payment.NewFakeProvider(payment.ChannelAlipay)
			e.deps.Payment = payment.NewService(payment.NewPGStore(e.pool), e.deps.Credits, map[string]payment.Provider{payment.ChannelAlipay: provider}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			gate, err := e.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err = gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741915))`, tid); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err = gate.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			out := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				out <- doReq(t, e.router, "POST", "/api/v1/billing/orders", access, map[string]string{"sku_code": "lite", "channel": payment.ChannelAlipay})
			}()
			blocked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if k9Count(t, e, `SELECT count(*) FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))`, pid) > 0 {
					blocked = true
					break
				}
				select {
				case w := <-out:
					t.Fatalf("order did not reach commit lock, status=%d", w.Code)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("request did not reach order commit lock")
			}
			if change == "password" {
				_, err = gate.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, uid)
			} else {
				_, err = gate.Exec(ctx, `UPDATE users SET status='closure_pending',token_version=token_version+1 WHERE id=$1`, uid)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			w := <-out
			adminContractResponse(t, w, 409)
			if k9Count(t, e, `SELECT count(*) FROM orders WHERE tenant_id=$1`, tid) != 0 || len(provider.Created) != 0 {
				t.Fatal("revoked original actor created an order or supplier side effect in another member's active team")
			}
		})
	}
}
