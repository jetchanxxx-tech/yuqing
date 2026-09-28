package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func TestPGWorkerProcessChild(t *testing.T) {
	if os.Getenv("YUQING_WORKER_TEST_CHILD") != "true" {
		return
	}
	verifyDisposableWorkerDSN(t, os.Getenv("YUQING_WORKER_TEST_DSN"))
	cfg := &config.Config{Store: config.StoreConfig{Driver: "postgres"}, Queue: config.QueueConfig{Driver: "postgres"}}
	cfg.DB.Primary = os.Getenv("YUQING_WORKER_TEST_DSN")
	cfg.Engines.Query.URL = os.Getenv("YUQING_WORKER_TEST_QUERY_URL")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := RunPGWorker(ctx, cfg, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPGServerWorkerAcrossProcessAndRestart(t *testing.T) {
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("YUQING_TEST_PG_URL required: disposable PostgreSQL only; server/worker not verified")
	}
	verifyDisposableWorkerDSN(t, os.Getenv(pgtest.EnvURL))
	pool := pgtest.Pool(t, "app_worker", pgtest.PlatformMigrations)
	path := pool.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(path, "pgtest_") || !strings.HasSuffix(path, ",public") {
		t.Fatalf("unsafe test search_path: %q", path)
	}
	dsn, err := url.Parse(os.Getenv(pgtest.EnvURL))
	if err != nil || (dsn.Scheme != "postgres" && dsn.Scheme != "postgresql") {
		t.Fatal("YUQING_TEST_PG_URL must be a disposable postgres URI")
	}
	query := dsn.Query()
	query.Set("search_path", path)
	dsn.RawQuery = query.Encode()
	parsed, err := pgxpool.ParseConfig(dsn.String())
	if err != nil || parsed.ConnConfig.RuntimeParams["search_path"] != path {
		t.Fatalf("test worker DSN does not target isolated schema: %v", err)
	}
	received := make(chan struct{}, 4)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.Error(w, "wrong endpoint", http.StatusNotFound)
			return
		}
		var req struct {
			DateFrom     string   `json:"date_from"`
			ExcludeWords []string `json:"exclude_words"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DateFrom != "2026-09-01" || len(req.ExcludeWords) != 1 || req.ExcludeWords[0] != "blocked" {
			http.Error(w, "filter snapshot missing", http.StatusBadRequest)
			return
		}
		select {
		case received <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"documents":[{"id":"keep","title":"keep","content":"safe","source_type":"news","published_at":"2026-09-02"},{"id":"skip","title":"blocked","content":"excluded","source_type":"news","published_at":"2026-09-02"}],"total_count":2}`)
	}))
	defer engine.Close()
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	cfg := &config.Config{Store: config.StoreConfig{Driver: "postgres"}, Queue: config.QueueConfig{Driver: "postgres"}}
	cfg.DB.Primary = dsn.String()
	cfg.Engines.Query.URL = engine.URL
	services := Build(cfg, nil)
	defer services.PGPool.Close()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,password_hash) VALUES ('worker-user','worker@example.com','test')`); err != nil {
		t.Fatal(err)
	}
	create := func() string {
		result, err := services.Analysis.Create(ctx, analysis.CreateAnalysisRequest{TenantID: "worker-tenant", UserID: "worker-user", Name: "worker run", Keywords: []string{"hello"}, DateFrom: "2026-09-01", ExcludeWords: []string{"blocked"}})
		if err != nil {
			t.Fatal(err)
		}
		return result.ID
	}
	first := create()
	if result, err := services.Analysis.Get(ctx, "worker-tenant", first); err != nil || result.State != analysis.StateQueued {
		t.Fatalf("server consumed task: %v %v", result, err)
	}
	work := func(id string) {
		childCtx, stop := context.WithCancel(ctx)
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(childCtx, binary, "-test.run=^TestPGWorkerProcessChild$", "-test.v")
		cmd.Env = append(os.Environ(), "YUQING_WORKER_TEST_CHILD=true", "YUQING_WORKER_TEST_DSN="+dsn.String(), "YUQING_WORKER_TEST_QUERY_URL="+engine.URL)
		if err := cmd.Start(); err != nil {
			stop()
			t.Fatal(err)
		}
		defer func() { stop(); _ = cmd.Wait() }()
		deadline := time.After(8 * time.Second)
		for {
			var state string
			var status string
			err := pool.QueryRow(ctx, `SELECT state FROM analyses WHERE id=$1 AND tenant_id='worker-tenant'`, id).Scan(&state)
			queueErr := pool.QueryRow(ctx, `SELECT status FROM queue_messages WHERE convert_from(body, 'UTF8') LIKE $1 ORDER BY created_at DESC LIMIT 1`, "%"+id+"%").Scan(&status)
			if err == nil && queueErr == nil && state == "completed" && status == "completed" {
				return
			}
			select {
			case <-deadline:
				t.Fatalf("worker process did not complete id=%s state=%s queue=%s errors=%v/%v", id, state, status, err, queueErr)
			case <-time.After(40 * time.Millisecond):
			}
		}
	}
	work(first)
	second := create()
	if err := services.Analysis.Transition(ctx, "worker-tenant", second, string(analysis.StateAcquiringBudget)); err != nil {
		t.Fatal(err)
	}
	work(second)
	if len(received) != 2 {
		t.Fatalf("query engine received %d requests, want two", len(received))
	}
	for _, id := range []string{first, second} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM raw_documents WHERE tenant_id='worker-tenant' AND analysis_id=$1`, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("filtered document count=%d err=%v", count, err)
		}
	}
	producer := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	defer producer.Close()
	if err := producer.Publish(ctx, analysis.TopicAnalysisTasks, []byte(fmt.Sprintf(`{"tenant_id":"worker-tenant","analysis_id":%q}`, first))); err != nil {
		t.Fatal(err)
	}
	work(first)
	if len(received) != 2 {
		t.Fatalf("terminal task fetched again: query engine count=%d", len(received))
	}
}

func verifyDisposableWorkerDSN(t *testing.T, dsn string) {
	t.Helper()
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.Contains(strings.ToLower(parsed.ConnConfig.Database), "test") {
		t.Fatal("worker PG contract requires a disposable test database")
	}
}
