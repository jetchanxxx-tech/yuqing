package report

import (
	"context"
	"testing"
	"time"
)

func TestPGReportSnapshotsSurviveRerunAndCannotBeOverwritten(t *testing.T) {
	pool := pgTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO analyses(id,tenant_id,name,state,created_by,report_content) VALUES('snapshot-a','snapshot-t','snapshot','completed','user-1','<p>first</p>')`); err != nil {
		t.Fatal(err)
	}
	store := NewPGStore(pool)
	first := sampleReport("snapshot-a", "html")
	if err := store.Create(ctx, "snapshot-t", "user-1", first); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE analyses SET report_content='<p>second</p>' WHERE id='snapshot-a'`); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := pool.QueryRow(ctx, `SELECT content_html FROM reports WHERE id=$1`, first.ID).Scan(&content); err != nil || content != "<p>first</p>" {
		t.Fatalf("historical content changed: %q %v", content, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reports SET content_html='<p>overwrite</p>' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("snapshot is mutable")
	}
	second := sampleReport("snapshot-a", "html")
	if err := NewPGStore(pool).Create(ctx, "snapshot-t", "user-1", second); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT content_html FROM reports WHERE id=$1`, second.ID).Scan(&content); err != nil || content != "<p>second</p>" {
		t.Fatalf("new snapshot missing: %q %v", content, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE analyses SET report_content='',state='queued' WHERE id='snapshot-a'`); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT content_html FROM reports WHERE id=$1`, first.ID).Scan(&content); err != nil || content != "<p>first</p>" {
		t.Fatal(content, err)
	}
}

func TestPGReportSnapshotSerializesWithAnalysisUpdateAndRollsBack(t *testing.T) {
	pool := pgTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO analyses(id,tenant_id,name,state,created_by,report_content) VALUES('locked-a','locked-t','snapshot','completed','user-1','<p>committed</p>')`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE analyses SET report_content='<p>uncommitted</p>' WHERE id='locked-a'`); err != nil {
		t.Fatal(err)
	}
	r := sampleReport("locked-a", "html")
	done := make(chan error, 1)
	go func() { done <- NewPGStore(pool).Create(ctx, "locked-t", "user-1", r) }()
	select {
	case err := <-done:
		t.Fatalf("snapshot bypassed analysis row lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot lock not released")
	}
	var content string
	if err := pool.QueryRow(ctx, `SELECT content_html FROM reports WHERE id=$1`, r.ID).Scan(&content); err != nil || content != "<p>committed</p>" {
		t.Fatal(content, err)
	}
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx2.Exec(ctx, `INSERT INTO reports(id,tenant_id,analysis_id,created_by) VALUES('rollback-report','locked-t','locked-a','user-1')`); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reports WHERE id='rollback-report'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
