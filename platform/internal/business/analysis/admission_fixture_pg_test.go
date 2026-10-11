package analysis

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/credit"
	"testing"
)

func seedPGAdmission(t *testing.T, pool *pgxpool.Pool, tenant string, balance int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO tenants(id,name,slug,db_name,status) VALUES($1,$1,$1,$1,'active') ON CONFLICT DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,'user-1','tenant_admin') ON CONFLICT DO NOTHING`, tenant); err != nil {
		t.Fatal(err)
	}
	credits := credit.NewService(credit.NewPGStore(pool))
	if err := credits.SetPlanCode(ctx, tenant, "lite"); err != nil {
		t.Fatal(err)
	}
	if balance > 0 {
		if err := credits.GrantPurchase(ctx, tenant, "fixture-"+id.New(), balance); err != nil {
			t.Fatal(err)
		}
	}
}
