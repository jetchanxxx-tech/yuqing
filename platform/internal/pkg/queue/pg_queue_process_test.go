package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

const (
	childRoleEnv  = "YUQING_QUEUE_TEST_CHILD_ROLE"
	childTopicEnv = "YUQING_QUEUE_TEST_CHILD_TOPIC"
	childPathEnv  = "YUQING_QUEUE_TEST_CHILD_SEARCH_PATH"
)

func runQueueProcess(t *testing.T, pool *pgxpool.Pool, role, topic string) {
	t.Helper()
	path := pool.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(path, "pgtest_") || !strings.HasSuffix(path, ",public") {
		t.Fatalf("expected disposable pgtest schema, got %q", path)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestPGQueueProcessChild$", "-test.v")
	command.Env = append(os.Environ(), childRoleEnv+"="+role, childTopicEnv+"="+topic, childPathEnv+"="+path)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("queue process %s failed: %v; output: %s", role, err, output)
	}
}

func TestPGQueueCrossProcessPublishAndAck(t *testing.T) {
	_, consumer := pgQueueTest(t)
	ctx := context.Background()
	topic := "cross-process-publish"
	runQueueProcess(t, consumer.pool, "publish", topic)
	seen := make(chan Message, 1)
	if err := consumer.Subscribe(ctx, topic, func(_ context.Context, message Message) error { seen <- message; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-seen:
		if string(message.Body) != "durable" || message.MessageID == "" {
			t.Fatalf("invalid cross-process message: %+v", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subprocess publish not delivered")
	}
	waitForQueue(t, func() bool {
		var state string
		return consumer.pool.QueryRow(ctx, `SELECT status FROM queue_messages WHERE topic = $1`, topic).Scan(&state) == nil && state == "completed"
	})
}

func TestPGQueueCrossProcessCrashAndHandlerErrorRedelivery(t *testing.T) {
	producer, _ := pgQueueTest(t)
	ctx := context.Background()
	for _, testCase := range []struct{ topic, payload, firstRole string }{
		{topic: "crashed-worker", payload: "crash", firstRole: "crash"},
		{topic: "failed-worker", payload: "fail", firstRole: "fail"},
	} {
		t.Run(testCase.topic, func(t *testing.T) {
			if err := producer.Publish(ctx, testCase.topic, []byte(testCase.payload)); err != nil {
				t.Fatal(err)
			}
			runQueueProcess(t, producer.pool, testCase.firstRole, testCase.topic)
			var beforeState, beforeError string
			var beforeAttempts int
			if err := producer.pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM queue_messages WHERE topic = $1`, testCase.topic).Scan(&beforeState, &beforeAttempts, &beforeError); err != nil {
				t.Fatal(err)
			}
			if beforeAttempts != 1 {
				t.Fatalf("first delivery attempts=%d, want 1", beforeAttempts)
			}
			if testCase.firstRole == "crash" && beforeState != "processing" {
				t.Fatalf("crashed delivery status=%s, want processing", beforeState)
			}
			if testCase.firstRole == "fail" && (beforeState != "pending" || !strings.Contains(beforeError, "forced consumer error")) {
				t.Fatalf("failed delivery status=%s last_error=%q", beforeState, beforeError)
			}
			runQueueProcess(t, producer.pool, "ack", testCase.topic)
			var state, lastError string
			var attempts int
			if err := producer.pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM queue_messages WHERE topic = $1`, testCase.topic).Scan(&state, &attempts, &lastError); err != nil {
				t.Fatal(err)
			}
			if state != "completed" || attempts != 2 {
				t.Fatalf("replay: status=%s attempts=%d last_error=%q", state, attempts, lastError)
			}
			if testCase.firstRole == "fail" && !strings.Contains(lastError, "forced consumer error") {
				t.Fatalf("missing persisted error: %q", lastError)
			}
		})
	}
}

func TestPGQueueProcessChild(t *testing.T) {
	role := os.Getenv(childRoleEnv)
	if role == "" {
		return
	}
	path := os.Getenv(childPathEnv)
	if !strings.HasPrefix(path, "pgtest_") || !strings.HasSuffix(path, ",public") {
		t.Fatal("child requires isolated pgtest schema")
	}
	url := os.Getenv(pgtest.EnvURL)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(config.ConnConfig.Database), "test") {
		t.Fatal("child requires disposable test database")
	}
	config.ConnConfig.RuntimeParams["search_path"] = path
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	queue := NewPGQueue(pool, PGQueueOptions{PollInterval: 10 * time.Millisecond, LeaseDuration: 250 * time.Millisecond, RetryDelay: 500 * time.Millisecond, MaxAttempts: 3})
	defer queue.Close()
	topic := os.Getenv(childTopicEnv)
	if topic == "" {
		t.Fatal("child topic missing")
	}
	if role == "publish" {
		if err := queue.Publish(ctx, topic, []byte("durable")); err != nil {
			t.Fatal(err)
		}
		return
	}
	processed := make(chan struct{}, 1)
	if err := queue.Subscribe(ctx, topic, func(_ context.Context, message Message) error {
		switch role {
		case "crash":
			os.Exit(0)
		case "fail":
			processed <- struct{}{}
			return errors.New("forced consumer error")
		case "ack":
			processed <- struct{}{}
			return nil
		default:
			return fmt.Errorf("unknown child role: %s", role)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-processed:
	case <-ctx.Done():
		t.Fatal("child did not receive queued message")
	}
	wantState := "completed"
	if role == "fail" {
		wantState = "pending"
	}
	waitForQueue(t, func() bool {
		var state string
		return pool.QueryRow(ctx, `SELECT status FROM queue_messages WHERE topic = $1`, topic).Scan(&state) == nil && state == wantState
	})
}
