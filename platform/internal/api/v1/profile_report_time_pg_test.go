package v1_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestProfileReportTimestampPGProjection(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, u := mustRegister(t, e.router, "report-time@example.invalid", "Report time")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO analyses(id,tenant_id,created_by,name,state) VALUES ('k8-time-analysis',$1,$2,'Timezone','completed');`, u["tenant_id"], u["user_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO reports(id,tenant_id,created_by,analysis_id,created_at) VALUES ('k8-time-report',$1,$2,'k8-time-analysis','2026-03-08T07:00:00Z');`, u["tenant_id"], u["user_id"]); err != nil {
		t.Fatal(err)
	}
	w := doReq(t, e.router, http.MethodGet, "/api/v1/reports", token, nil)
	if w.Code != 200 {
		t.Fatalf("report list=%d", w.Code)
	}
	reports := decodeBody(t, w)["reports"].([]any)
	if len(reports) != 1 || reports[0].(map[string]any)["created_at"] != "2026-03-08T07:00:00Z" {
		t.Fatalf("report timestamp unavailable to saved-timezone UI: %s", w.Body.String())
	}
	// A newly created response and subsequent DB read expose the same persisted
	// instant (PostgreSQL stores microsecond precision).
	created, err := e.deps.Report.CreateFromAnalysis(context.Background(), u["tenant_id"].(string), "k8-time-analysis", "html", u["user_id"].(string))
	if err != nil || created.CreatedAt == nil {
		t.Fatalf("new report missing creation instant: %v", err)
	}
	loaded, err := e.deps.Report.Get(context.Background(), u["tenant_id"].(string), created.ID)
	if err != nil || loaded.CreatedAt == nil || !loaded.CreatedAt.Equal(created.CreatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("response differs from persisted instant: %v", err)
	}

}
