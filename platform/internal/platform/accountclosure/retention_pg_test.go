package accountclosure

import (
	"context"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
)

func TestClosurePGCatalogRetentionPreservesRunFactsAndOtherTenants(t *testing.T) {
	for _, policy := range []struct {
		code string
		days int
	}{{"free", 30}, {"lite", 90}, {"pro", 180}, {"enterprise", 365}} {
		t.Run(policy.code, func(t *testing.T) {
			pool := pgtest.Pool(t, "closure_retention")
			ctx := context.Background()
			var err error
			for _, query := range []string{
				`INSERT INTO users(id,email,password_hash,name) VALUES('closing','closing@example.invalid','hash','Owner'),('other','other@example.invalid','hash','Other')`,
				`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('sole','Sole','sole','sole','active'),('other','Other','other','other','active')`,
				`INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('sole','closing','tenant_admin'),('other','other','tenant_admin')`,
				`INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('sole',1,$1)`,
				`INSERT INTO analyses(id,tenant_id,name,state,created_by,created_at,report_content) VALUES('expired','sole','Private','completed','closing',now()-($2::integer+1)*interval '24 hours','private report'),('retained','sole','Retained','completed','closing',now()-($2::integer-1)*interval '24 hours','retained report'),('other-report','other','Other','completed','other',now()-interval '400 days','other report')`,
				`INSERT INTO analysis_runs(id,tenant_id,analysis_id,run_no,actor_user_id,plan_code,catalog_revision,charge_mode,state) VALUES('financial-run','sole','expired',1,'closing',$1,'historical','legacy_unbilled','completed')`,
				`INSERT INTO reports(id,tenant_id,analysis_id,created_by,created_at,file_key) VALUES('expired-report','sole','expired','closing',now()-($2::integer+1)*interval '24 hours','reports/expired-report.html'),('retained-report','sole','retained','closing',now()-($2::integer-1)*interval '24 hours','reports/retained-report.html')`,
				`INSERT INTO raw_documents(id,tenant_id,analysis_id,content) VALUES('expired-doc','sole','expired','private text'),('retained-doc','sole','retained','retained text')`,
			} {
				var args []any
				if strings.Contains(query, "$2") {
					query = strings.ReplaceAll(query, "$2", "$1")
					args = []any{policy.days}
				} else if strings.Contains(query, "$1") {
					args = []any{policy.code}
				}
				if _, err = pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			s := NewPGStore(pool)
			s.SetAvatarStorage(storage.NewLocalAvatar(t.TempDir()))
			if _, err = s.Request(ctx, Actor{UserID: "closing", PasswordHash: "hash"}, []string{"sole"}); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'`); err != nil {
				t.Fatal(err)
			}
			var result *Status
			for i := 0; i < 30; i++ {
				result, err = s.Process(ctx, "closing", 1)
				if err != nil {
					t.Fatal(err)
				}
				if result.State == "completed" {
					break
				}
			}
			if result.State != "completed" || result.CleanupStatus != "retention_pending" {
				t.Fatalf("retention was not reported truthfully: %+v", result)
			}
			var intact bool
			if err = pool.QueryRow(ctx, `SELECT (SELECT report_content='' AND closure_purged_at IS NOT NULL FROM analyses WHERE id='expired') AND (SELECT count(*)=1 FROM analysis_runs WHERE id='financial-run') AND (SELECT report_content='retained report' FROM analyses WHERE id='retained') AND (SELECT count(*)=0 FROM raw_documents WHERE id='expired-doc') AND (SELECT count(*)=1 FROM raw_documents WHERE id='retained-doc') AND (SELECT report_content='other report' FROM analyses WHERE id='other-report')`).Scan(&intact); err != nil || !intact {
				t.Fatalf("retention or financial/tenant isolation broke: %v", err)
			}
			if _, err = pool.Exec(ctx, `UPDATE analyses SET created_at=now()-interval '400 days' WHERE id='retained'; UPDATE reports SET created_at=now()-interval '400 days' WHERE id='retained-report'`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				result, err = s.Process(ctx, "closing", 1)
				if err != nil {
					t.Fatal(err)
				}
				if result.CleanupStatus == "complete" {
					break
				}
			}
			if result.CleanupStatus != "complete" {
				t.Fatalf("retention retry failed: %+v", result)
			}
		})
	}
}
