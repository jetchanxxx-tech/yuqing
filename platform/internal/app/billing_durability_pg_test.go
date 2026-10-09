package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/engine"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/platform/credit"
)

type durabilityFetcher struct{}

func (durabilityFetcher) Fetch(context.Context, analysis.FetchRequest) ([]analysis.Document, error) {
	return []analysis.Document{{ID: "one", Title: "evidence", Content: "sandbox"}}, nil
}

func TestBillingDurabilityPGReportFailureCannotCompleteOrKeepReportCharge(t *testing.T) {
	pool := pgtest.Pool(t, "accounting_durability")
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO users(id,email,password_hash,status) VALUES('actor','actor@example.invalid','fixture','active')`,
		`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('team','team','team','team','active')`,
		`INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('team','actor','tenant_admin')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	credits := credit.NewService(credit.NewPGStore(pool))
	if err := credits.GrantTrial(ctx, "team", 1); err != nil {
		t.Fatal(err)
	}
	q := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	defer q.Close()
	svc := analysis.NewPGService(pool, q, 1)
	a, err := svc.Create(ctx, analysis.CreateAnalysisRequest{TenantID: "team", UserID: "actor", Name: "report durable error"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"ACCOUNTING_DURABILITY_ERROR","message":"usage persistence unavailable"}`))
	}))
	defer server.Close()
	pipeline := analysis.NewPipeline(svc, durabilityFetcher{}, time.Minute, nil).WithGenerator(&engineReportAdapter{rep: engine.NewRealReportEngine(server.URL, "", nil)})
	err = pipeline.Handle(ctx, analysis.TaskMessage{TenantID: "team", AnalysisID: a.ID, RunID: a.CurrentRunID})
	if err == nil {
		t.Error("pipeline returned success after engine accounting durability failed")
	}
	result, err := svc.Get(ctx, "team", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != analysis.StateFailed || result.ReportContent != "" {
		t.Fatalf("durability failure published output: %+v", result)
	}
	balance, err := credits.Balance(ctx, "team")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 1 {
		t.Fatalf("failed report retained credit charge: %d", balance)
	}
	var refunds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_transactions WHERE run_id=$1 AND reason='refund'`, a.CurrentRunID).Scan(&refunds); err != nil || refunds != 1 {
		t.Fatalf("current run refund=%d/%v", refunds, err)
	}
}
