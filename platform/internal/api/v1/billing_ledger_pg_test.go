package v1_test

import (
	"context"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"testing"
)

func TestBillingLedgerPGEnforcesBidirectionalImmutableRunConsumption(t *testing.T) {
	for name, mutation := range map[string]string{
		"delta":                  `UPDATE credit_transactions SET delta=0 WHERE id='consume'`,
		"run_link":               `UPDATE credit_transactions SET run_id=NULL WHERE id='consume'`,
		"actor":                  `UPDATE credit_transactions SET actor_user_id='fixed' WHERE id='consume'`,
		"plan":                   `UPDATE credit_transactions SET plan_code_snapshot='enterprise' WHERE id='consume'`,
		"exempt_reverse_consume": `INSERT INTO credit_transactions(id,tenant_id,analysis_id,run_id,actor_user_id,plan_code_snapshot,delta,reason,balance_after) VALUES('forbidden','team','free-analysis','exempt-run','fixed','lite',-1,'consume',8)`,
	} {
		t.Run(name, func(t *testing.T) {
			pool := pgtest.Pool(t, "billing_ledger_invariant")
			ctx := context.Background()
			for _, sql := range []string{
				`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('team','team','team','team','active')`,
				`INSERT INTO users(id,email,password_hash,status) VALUES('normal','normal@example.invalid','fixture','active'),('fixed','admin@pangu.com','fixture','active')`,
				`INSERT INTO billing_exempt_principals(policy_key,user_id,bound_by) VALUES('fixed_admin_v1','fixed','isolated-test')`,
				`INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('team',10,'lite')`,
				`INSERT INTO analyses(id,tenant_id,name,created_by,state) VALUES('paid-analysis','team','paid','normal','queued'),('free-analysis','team','free','fixed','queued')`,
			} {
				if _, err := pool.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			for _, sql := range []string{
				`INSERT INTO analysis_runs(id,tenant_id,analysis_id,run_no,actor_user_id,plan_code,catalog_revision,charge_mode,state) VALUES('normal-run','team','paid-analysis',1,'normal','lite','fixture','normal','queued')`,
				`INSERT INTO credit_transactions(id,tenant_id,analysis_id,run_id,actor_user_id,plan_code_snapshot,delta,reason,balance_after) VALUES('consume','team','paid-analysis','normal-run','normal','lite',-1,'consume',9)`,
				`UPDATE report_credits SET balance=9 WHERE tenant_id='team'`,
				`UPDATE analysis_runs SET consume_tx_id='consume' WHERE id='normal-run'`,
				`UPDATE analyses SET current_run_id='normal-run' WHERE id='paid-analysis'`,
				`INSERT INTO analysis_runs(id,tenant_id,analysis_id,run_no,actor_user_id,plan_code,catalog_revision,charge_mode,exempt_policy_key,exempt_policy_version,state) VALUES('exempt-run','team','free-analysis',1,'fixed','lite','fixture','exempt','fixed_admin_v1',1,'queued')`,
				`UPDATE analyses SET current_run_id='exempt-run' WHERE id='free-analysis'`,
			} {
				if _, err = tx.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatalf("valid paired normal and exempt transactions must commit: %v", err)
			}
			// Legacy facts remain unlinked: no guessed actor or rewritten old history.
			if _, err = pool.Exec(ctx, `INSERT INTO credit_transactions(id,tenant_id,analysis_id,delta,reason,balance_after) VALUES('legacy','team','historical-unmapped',-1,'consume',8)`); err != nil {
				t.Fatalf("legacy unlinked fact rejected: %v", err)
			}
			changed, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = changed.Exec(ctx, mutation)
			if err == nil {
				err = changed.Commit(ctx)
			}
			_ = changed.Rollback(ctx)
			if err == nil {
				t.Error("ledger-only mutation bypassed the final run/consume invariant")
			}
			var delta int
			var run, actor, plan string
			if err := pool.QueryRow(ctx, `SELECT delta,COALESCE(run_id,''),COALESCE(actor_user_id,''),COALESCE(plan_code_snapshot,'') FROM credit_transactions WHERE id='consume'`).Scan(&delta, &run, &actor, &plan); err != nil {
				t.Fatal(err)
			}
			if delta != -1 || run != "normal-run" || actor != "normal" || plan != "lite" {
				t.Errorf("paired financial fact changed: %d/%s/%s/%s", delta, run, actor, plan)
			}
			var reverse int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_transactions WHERE run_id='exempt-run' AND reason='consume'`).Scan(&reverse); err != nil {
				t.Fatal(err)
			}
			if reverse != 0 {
				t.Errorf("exempt run acquired %d reverse-linked consumes", reverse)
			}
		})
	}
}
