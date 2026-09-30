package queue

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func pgQueueTest(t *testing.T) (*PGQueue, *PGQueue) {
	t.Helper()
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("YUQING_TEST_PG_URL 未配置：无法验证 PostgreSQL 队列合约；必须使用 disposable 测试库，禁止生产库")
	}
	config, err := pgxpool.ParseConfig(os.Getenv(pgtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(config.ConnConfig.Database), "test") {
		t.Fatal("YUQING_TEST_PG_URL 必须指向名称含 test 的 disposable 测试库")
	}
	pool := pgtest.Pool(t, "queue", pgtest.PlatformMigrations)
	otherPool, err := pgxpool.NewWithConfig(context.Background(), pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(otherPool.Close)
	options := PGQueueOptions{PollInterval: 10 * time.Millisecond, LeaseDuration: 250 * time.Millisecond, RetryDelay: 20 * time.Millisecond, MaxAttempts: 3}
	first := NewPGQueue(pool, options)
	second := NewPGQueue(otherPool, options)
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	return first, second
}

func waitForQueue(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for PostgreSQL queue")
		case <-ticker.C:
		}
	}
}

func TestPGQueueDoesNotFallBackToMemoryWithoutPool(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil PostgreSQL pool silently accepted")
		}
	}()
	NewPGQueue(nil, PGQueueOptions{})
}

func TestPGQueueShutdownLeavesLeasedMessageRecoverable(t *testing.T) {
	first, second := pgQueueTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claimed := make(chan struct{}, 1)
	if err := first.Subscribe(ctx, "shutdown-recovery", func(ctx context.Context, _ Message) error {
		claimed <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(context.Background(), "shutdown-recovery", []byte("durable")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-claimed:
	case <-time.After(3 * time.Second):
		t.Fatal("message was not claimed")
	}
	cancel()
	waitForQueue(t, func() bool {
		var status string
		return first.pool.QueryRow(context.Background(), `SELECT status FROM queue_messages WHERE topic='shutdown-recovery'`).Scan(&status) == nil && status == "processing"
	})
	recovered := make(chan struct{}, 1)
	if err := second.Subscribe(context.Background(), "shutdown-recovery", func(_ context.Context, _ Message) error {
		recovered <- struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recovered:
	case <-time.After(3 * time.Second):
		t.Fatal("leased task was not redelivered")
	}
	waitForQueue(t, func() bool {
		var status string
		var attempts int
		return first.pool.QueryRow(context.Background(), `SELECT status,attempts FROM queue_messages WHERE topic='shutdown-recovery'`).Scan(&status, &attempts) == nil && status == "completed" && attempts == 2
	})
}

func TestPGQueuePersistsAndPublishesWithTransaction(t *testing.T) {
	producer, consumer := pgQueueTest(t)
	ctx := context.Background()
	var queueInterface Queue = producer
	if err := queueInterface.Publish(ctx, "analysis.tasks", []byte("persisted")); err != nil {
		t.Fatal(err)
	}
	if err := producer.Close(); err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 3)
	if err := consumer.Subscribe(ctx, "analysis.tasks", func(_ context.Context, msg Message) error {
		seen <- string(msg.Body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		if got != "persisted" {
			t.Fatalf("first message = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cross-instance publish not delivered")
	}
	pool := producer.pool
	waitForQueue(t, func() bool {
		var count int
		return pool.QueryRow(ctx, `SELECT count(*) FROM queue_messages WHERE topic = $1 AND status = 'completed'`, "analysis.tasks").Scan(&count) == nil && count == 1
	})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := consumer.PublishTx(ctx, tx, "analysis.tasks", []byte("rolled-back")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := consumer.PublishTx(ctx, tx, "analysis.tasks", []byte("committed")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		if got != "committed" {
			t.Fatalf("after transaction = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("committed message not delivered")
	}
	select {
	case got := <-seen:
		t.Fatalf("unexpected duplicate/rollback delivery: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	var rolledBack int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM queue_messages WHERE body = $1`, []byte("rolled-back")).Scan(&rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("rolled-back message persisted: count=%d err=%v", rolledBack, err)
	}
}

func TestPGQueueLeaseAndConcurrentConsumers(t *testing.T) {
	first, second := pgQueueTest(t)
	ctx := context.Background()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	if err := first.Subscribe(ctx, "lease", func(_ context.Context, msg Message) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(ctx, "lease", []byte("one")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first consumer did not claim")
	}
	if err := second.Subscribe(ctx, "lease", func(_ context.Context, msg Message) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond) // longer than a lease: heartbeat must prevent double claim
	if calls.Load() != 1 {
		t.Fatalf("duplicate in-flight processing: %d", calls.Load())
	}
	close(release)
	waitForQueue(t, func() bool {
		var status string
		return first.pool.QueryRow(ctx, `SELECT status FROM queue_messages WHERE topic = 'lease'`).Scan(&status) == nil && status == "completed"
	})
	if err := first.Publish(ctx, "recovery", []byte("orphan")); err != nil {
		t.Fatal(err)
	}
	_, err := first.pool.Exec(ctx, `UPDATE queue_messages SET status = 'processing', attempts = 1, locked_by = 'dead-worker', locked_until = now() - interval '1 second' WHERE topic = 'recovery'`)
	if err != nil {
		t.Fatal(err)
	}
	recovered := make(chan Message, 1)
	if err := second.Subscribe(ctx, "recovery", func(_ context.Context, msg Message) error { recovered <- msg; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-recovered:
		if string(msg.Body) != "orphan" {
			t.Fatalf("recovered = %q", msg.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expired lease not recovered")
	}
}

func TestPGQueueRetriesAndKeepsFailedRecord(t *testing.T) {
	producer, consumer := pgQueueTest(t)
	ctx := context.Background()
	var attempts atomic.Int32
	if err := consumer.Subscribe(ctx, "failure", func(_ context.Context, msg Message) error {
		attempts.Add(1)
		return errors.New("transient consumer failure")
	}); err != nil {
		t.Fatal(err)
	}
	if err := producer.Publish(ctx, "failure", []byte("keep me")); err != nil {
		t.Fatal(err)
	}
	waitForQueue(t, func() bool {
		var status, lastError string
		var storedAttempts int
		err := producer.pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM queue_messages WHERE topic = 'failure'`).Scan(&status, &storedAttempts, &lastError)
		return err == nil && status == "failed" && storedAttempts == 3 && strings.Contains(lastError, "transient consumer failure")
	})
	if attempts.Load() != 3 {
		t.Fatalf("consumer called %d times; want 3", attempts.Load())
	}
	select {
	case err := <-consumer.Errors():
		if !strings.Contains(err.Error(), "transient consumer failure") {
			t.Fatalf("reported error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer errors were swallowed")
	}
	time.Sleep(120 * time.Millisecond)
	if attempts.Load() != 3 {
		t.Fatalf("unbounded retries: %d", attempts.Load())
	}
}

func TestPGQueueExpiredFinalLeaseBecomesQueryableFailure(t *testing.T) {
	producer, consumer := pgQueueTest(t)
	ctx := context.Background()
	if err := producer.Publish(ctx, "expired", []byte("abandoned")); err != nil {
		t.Fatal(err)
	}
	_, err := producer.pool.Exec(ctx, `UPDATE queue_messages SET status = 'processing', attempts = max_attempts, locked_by = 'dead-worker', locked_until = now() - interval '1 second' WHERE topic = 'expired'`)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	if err := consumer.Subscribe(ctx, "expired", func(_ context.Context, _ Message) error { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	waitForQueue(t, func() bool {
		var status, detail string
		err := producer.pool.QueryRow(ctx, `SELECT status, last_error FROM queue_messages WHERE topic = 'expired'`).Scan(&status, &detail)
		return err == nil && status == "failed" && strings.Contains(detail, "lease expired")
	})
	if calls.Load() != 0 {
		t.Fatalf("expired final attempt redelivered %d times", calls.Load())
	}
}

func TestPGQueueHandlerPanicIsRetriedAndRecorded(t *testing.T) {
	producer, consumer := pgQueueTest(t)
	ctx := context.Background()
	if err := consumer.Subscribe(ctx, "panic", func(_ context.Context, _ Message) error { panic("unexpected consumer panic") }); err != nil {
		t.Fatal(err)
	}
	if err := producer.Publish(ctx, "panic", []byte("replay me")); err != nil {
		t.Fatal(err)
	}
	waitForQueue(t, func() bool {
		var status, detail string
		var attempts int
		err := producer.pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM queue_messages WHERE topic = 'panic'`).Scan(&status, &attempts, &detail)
		return err == nil && status == "failed" && attempts == 3 && strings.Contains(detail, "unexpected consumer panic")
	})
}
