package analysis

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func TestPGCreateAndRerunPublishAtomically(t *testing.T) {
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("YUQING_TEST_PG_URL required: disposable PostgreSQL only")
	}
	parsed, err := pgxpool.ParseConfig(os.Getenv(pgtest.EnvURL))
	if err != nil || !strings.Contains(strings.ToLower(parsed.ConnConfig.Database), "test") {
		t.Fatal("PG outbox contract requires a disposable test database")
	}
	pool := pgTestPool(t)
	ctx := context.Background()
	svc := NewPGService(pool, queue.NewPGQueue(pool, queue.PGQueueOptions{}), 1)
	svc.SetBetaSkipCredits(true)
	created, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "outbox-t1", UserID: "user-1", Name: "persisted", Keywords: []string{"topic"}})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM queue_messages WHERE topic = 'analysis.tasks' AND convert_from(body, 'UTF8') LIKE $1`, "%"+created.ID+"%").Scan(&count); err != nil || count != 1 {
		t.Fatalf("atomic create message count=%d err=%v", count, err)
	}
	if err := svc.Transition(ctx, "outbox-t1", created.ID, string(StateFailed)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rerun(ctx, "outbox-t1", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Transition(ctx, "outbox-t1", created.ID, string(StateAcquiringBudget)); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecoverInterrupted(ctx, "outbox-t1", created.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := svc.Get(ctx, "outbox-t1", created.ID)
	if err != nil || recovered.State != StateQueued {
		t.Fatalf("recovery state=%v err=%v", recovered, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM queue_messages WHERE convert_from(body, 'UTF8') LIKE $1`, "%"+created.ID+"%").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rerun message count=%d err=%v", count, err)
	}
	if err := svc.Transition(ctx, "outbox-t1", created.ID, string(StateFailed)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rerun(ctx, "other-tenant", created.ID); err == nil {
		t.Fatal("cross-tenant rerun accepted")
	}
	if _, err := pool.Exec(ctx, `DROP TABLE queue_messages`); err != nil {
		t.Fatal(err)
	}
	if err := svc.Rerun(ctx, "outbox-t1", created.ID); err == nil {
		t.Fatal("rerun succeeded without queue")
	}
	unchanged, err := svc.Get(ctx, "outbox-t1", created.ID)
	if err != nil || unchanged.State != StateFailed {
		t.Fatalf("rerun rollback state=%v err=%v", unchanged, err)
	}
	_, err = svc.Create(ctx, CreateAnalysisRequest{TenantID: "outbox-t1", UserID: "user-1", Name: "rolled back"})
	if err == nil {
		t.Fatal("create succeeded without outbox")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analyses WHERE tenant_id = 'outbox-t1' AND name = 'rolled back'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan analysis count=%d err=%v", count, err)
	}
}

func TestPGCreateRejectsWithoutExplicitBetaCreditBypass(t *testing.T) {
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("YUQING_TEST_PG_URL required: disposable PostgreSQL only")
	}
	parsed, err := pgxpool.ParseConfig(os.Getenv(pgtest.EnvURL))
	if err != nil || !strings.Contains(strings.ToLower(parsed.ConnConfig.Database), "test") {
		t.Fatal("PG contract requires disposable test database")
	}
	pool := pgTestPool(t)
	q := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	defer q.Close()
	svc := NewPGService(pool, q, 1)
	_, err = svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "no-bypass", UserID: "user-1", Name: "must reject"})
	if err == nil || !strings.Contains(err.Error(), "atomic reservation") {
		t.Fatalf("default PG create err=%v; must fail closed", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM analyses WHERE tenant_id = 'no-bypass'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected analysis count=%d err=%v", count, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM queue_messages WHERE topic = 'analysis.tasks'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected message count=%d err=%v", count, err)
	}
}

func TestPGCanceledTaskRejectsDocumentsAndDatabaseErrorsFailPipeline(t *testing.T) {
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("YUQING_TEST_PG_URL required: disposable PostgreSQL only")
	}
	parsed, err := pgxpool.ParseConfig(os.Getenv(pgtest.EnvURL))
	if err != nil || !strings.Contains(strings.ToLower(parsed.ConnConfig.Database), "test") {
		t.Fatal("PG contract requires disposable test database")
	}
	pool := pgTestPool(t)
	ctx := context.Background()
	svc := NewPGService(pool, queue.NewPGQueue(pool, queue.PGQueueOptions{}), 1)
	svc.SetBetaSkipCredits(true)
	canceled, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "documents-t1", UserID: "user-1", Name: "canceled"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, "documents-t1", canceled.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDocuments(ctx, "documents-t1", canceled.ID, sampleDocs(1)); err == nil {
		t.Fatal("late document accepted")
	}
	if err := svc.SaveDocuments(ctx, "documents-t2", canceled.ID, sampleDocs(1)); err == nil {
		t.Fatal("cross-tenant document accepted")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM raw_documents WHERE analysis_id=$1`, canceled.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late documents=%d err=%v", count, err)
	}
	active, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "documents-t1", UserID: "user-1", Name: "storage failure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE raw_documents`); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDocuments(ctx, "documents-t1", active.ID, sampleDocs(1)); err == nil {
		t.Fatal("database write failure swallowed")
	}
	p := NewPipeline(svc, &fakeFetcher{docs: sampleDocs(1)}, 5*time.Second, nil)
	if err := p.Handle(ctx, TaskMessage{TenantID: "documents-t1", AnalysisID: active.ID}); err == nil {
		t.Fatal("pipeline reported success without documents")
	}
	got, err := svc.Get(ctx, "documents-t1", active.ID)
	if err != nil || got.State == StateCompleted {
		t.Fatalf("false pipeline success: %+v err=%v", got, err)
	}
}
