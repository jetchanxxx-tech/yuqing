package analysis

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type k4BlockedFetcher struct {
	entered chan struct{}
	release chan struct{}
}

func (f *k4BlockedFetcher) Fetch(ctx context.Context, req FetchRequest) ([]Document, error) {
	close(f.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
		return []Document{{ID: "stale-doc", Title: "stale output", Content: "old run data", SourceType: "weibo"}}, nil
	}
}

func TestK4PGStaleWorkerCannotWriteIntoNewRerun(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	// RED exercises the existing production beta path; admission later uses real credits.
	f.svc.SetBetaSkipCredits(true)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "stale worker isolation"))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT body FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	msg, err := DecodeTaskMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &k4BlockedFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	pipeline := NewPipeline(f.svc, fetcher, time.Minute, nil)
	finished := make(chan error, 1)
	go func() { finished <- pipeline.Handle(f.ctx, msg) }()
	select {
	case <-fetcher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	if err := f.svc.Cancel(f.ctx, f.tenantID, a.ID); err != nil {
		close(fetcher.release)
		t.Fatal(err)
	}
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID); err != nil {
		close(fetcher.release)
		t.Fatal(err)
	}
	close(fetcher.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("old worker did not finish")
	}
	current, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != StateQueued || current.DocCount != 0 || current.Summary != "" || current.ReportContent != "" {
		t.Fatalf("stale worker changed new run: %+v", current)
	}
	var docs int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM raw_documents WHERE analysis_id=$1`, a.ID).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("stale worker persisted %d old documents into new run", docs)
	}
}

func TestK4PGTaskMessageContainsPersistedRunIdentity(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	f.svc.SetBetaSkipCredits(true)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "durable run identity"))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT body FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(body, &message); err != nil {
		t.Fatal(err)
	}
	runID, _ := message["run_id"].(string)
	if runID == "" {
		t.Fatal("committed task lacks durable per-execution run_id")
	}
	var persisted string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_run_id FROM analyses WHERE id=$1`, a.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != runID {
		t.Fatalf("message run %s differs from analysis run %s", runID, persisted)
	}
}
