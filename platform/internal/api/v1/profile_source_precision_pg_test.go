package v1_test

import (
	"context"
	"github.com/yuqing/platform/internal/business/analysis"
	"net/http"
	"testing"
)

func TestProfileSourcePrecisionPGHTTP(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, u := mustRegister(t, e.router, "source-time@example.invalid", "Source time")
	ctx := context.Background()
	tid, uid := u["tenant_id"].(string), u["user_id"].(string)
	if _, err := e.pool.Exec(ctx, `INSERT INTO analyses(id,tenant_id,created_by,name,state) VALUES ('source-time-analysis',$1,$2,'Source precision','queued')`, tid, uid); err != nil {
		t.Fatal(err)
	}
	docs := []analysis.Document{{ID: "date-only", Title: "Only date", PublishedAt: "2026-03-08"}, {ID: "unknown-zone", Title: "Unknown zone", PublishedAt: "2026-03-08 01:30:00"}, {ID: "instant", Title: "Known instant", PublishedAt: "2026-03-08T07:00:00Z"}}
	if err := e.deps.Analysis.SaveDocuments(ctx, tid, "source-time-analysis", docs); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE analyses SET state='completed' WHERE id='source-time-analysis'`); err != nil {
		t.Fatal(err)
	}
	e.rebuild()
	w := doReq(t, e.router, http.MethodGet, "/api/v1/analyses/source-time-analysis/result", token, nil)
	if w.Code != 200 {
		t.Fatalf("read result=%d %s", w.Code, w.Body.String())
	}
	results := decodeBody(t, w)["documents"].([]any)
	byID := map[string]map[string]any{}
	for _, value := range results {
		row := value.(map[string]any)
		byID[row["id"].(string)] = row
	}
	for _, doc := range docs[:2] {
		if byID[doc.ID]["source_published_at"] != doc.PublishedAt {
			t.Errorf("source precision lost after PG restart: %s %v", doc.ID, byID[doc.ID])
		}
		if value := byID[doc.ID]["published_at"]; value != nil && value != "" {
			t.Error("unknown source time became an instant")
		}
	}
	if byID["instant"]["published_at"] != "2026-03-08T07:00:00Z" {
		t.Error("known instant changed")
	}
}
