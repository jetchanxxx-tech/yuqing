package analysis

import (
	"testing"
	"time"
)

func TestResilientPGCompletedBriefRefundsExactlyItsConsumeAndSurvivesReconstruction(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	f := newK4BillingPGFixture(t, 1)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "retained brief"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPipeline(f.svc, &fakeFetcher{docs: []Document{{ID: "retained", Title: "source", Content: "retained body", SourceType: "weibo"}}}, time.Minute, nil)
	msg := TaskMessage{TenantID: f.tenantID, AnalysisID: a.ID, RunID: a.CurrentRunID}
	if err = p.Handle(f.ctx, msg); err != nil {
		t.Fatal(err)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 1, 1, 1, 1)
	var state, level, content string
	if err = f.pool.QueryRow(f.ctx, `SELECT state,delivery_level,report_content_snapshot FROM analysis_runs WHERE id=$1`, a.CurrentRunID).Scan(&state, &level, &content); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || level != "brief" || content == "" {
		t.Fatalf("durable brief = %s/%s/%q", state, level, content)
	}
	if err = p.Handle(f.ctx, msg); err != nil {
		t.Fatal(err)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 1, 1, 1, 1)
}

func TestResilientPGRefundFailureRollsBackCompletedDelivery(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	f := newK4BillingPGFixture(t, 1)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "refund atomicity"))
	if err != nil {
		t.Fatal(err)
	}
	f.rejectRefundWrites(t)
	p := NewPipeline(f.svc, &fakeFetcher{}, time.Minute, nil)
	if err = p.Handle(f.ctx, TaskMessage{TenantID: f.tenantID, AnalysisID: a.ID, RunID: a.CurrentRunID}); err == nil {
		t.Fatal("delivery committed although the paired refund was rejected")
	}
	f.assertBalance(t, 0)
	var state string
	if err = f.pool.QueryRow(f.ctx, `SELECT state FROM analyses WHERE id=$1`, a.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "completed" {
		t.Fatal("completed delivery escaped its failed refund transaction")
	}
}
