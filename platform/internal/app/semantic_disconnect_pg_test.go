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

func TestSemanticPGTransportDisconnectPreservesCheckpointForSameRun(t *testing.T) {
	pool := pgtest.Pool(t, "semantic_transport_disconnect")
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO users(id,email,password_hash,status) VALUES('actor','disconnect@example.invalid','fixture','active')`,
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
	for _, mode := range []string{"connection lost", "response read failed"} {
		t.Run(mode, func(t *testing.T) {
			a, err := svc.Create(ctx, analysis.CreateAnalysisRequest{TenantID: "team", UserID: "actor", Name: "disconnect preserves durable facts"})
			if err != nil {
				t.Fatal(err)
			}
			// The supplier search records candidates before the transport fails.
			checkpoint := &analysis.RetrievalCoverage{AdmissionVersion: "semantic-v2", ProviderCandidates: 7, AcceptedCount: 0, PerKeyword: []map[string]any{}, PerPlatform: []map[string]any{}, CandidateAudit: []map[string]any{{"id": "observed-before-disconnect", "status": "unprocessed", "reason": "network_interrupted", "content": "material fetched before connection loss"}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := svc.SaveRetrievalAudit(ctx, a.ID, analysis.RetrievalAuditRequest{RunID: a.CurrentRunID, TenantID: "team", Coverage: checkpoint}); err != nil {
					t.Error(err)
				}
				if mode == "response read failed" {
					w.Header().Set("Content-Length", "4096")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"documents":`))
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
			}))
			defer server.Close()
			pipeline := analysis.NewPipeline(svc, &engineFetcher{crawler: engine.NewRealCrawlerEngine(server.URL, "", nil)}, time.Minute, nil)

			if err := pipeline.Handle(ctx, analysis.TaskMessage{TenantID: "team", AnalysisID: a.ID, RunID: a.CurrentRunID}); err == nil {
				t.Fatal("disconnected acquisition reported success")
			}
			current, err := svc.Get(ctx, "team", a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.RetrievalCoverage == nil || current.RetrievalCoverage.AdmissionVersion != "semantic-v2" || current.RetrievalCoverage.ProviderCandidates != 7 || len(current.RetrievalCoverage.CandidateAudit) != 1 {
				t.Fatalf("transport failure overwrote same-run durable checkpoint: %+v", current.RetrievalCoverage)
			}
			var runCandidates int
			if err := pool.QueryRow(ctx, `SELECT (retrieval_coverage->>'provider_candidates')::int FROM analysis_runs WHERE id=$1`, a.CurrentRunID).Scan(&runCandidates); err != nil || runCandidates != 7 {
				t.Fatalf("run ledger candidate checkpoint=%d/%v", runCandidates, err)
			}
		})
	}
}
