package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func TestBuildRejectsUnsafeQueueWiringBeforeConnecting(t *testing.T) {
	for _, testCase := range []struct {
		name, store, driver, want string
	}{
		{"pg memory fallback", "postgres", "memory", "persistent PostgreSQL queue"},
		{"pg requires beta opt-in", "postgres", "postgres", "YUQING_BETA_SKIP_CREDITS"},
		{"unknown driver", "memory", "redis", "unsupported queue driver"},
		{"memory store with pg queue", "memory", "postgres", "PostgreSQL store"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil || !strings.Contains(recovered.(string), testCase.want) {
					t.Fatalf("Build panic = %v; want %q", recovered, testCase.want)
				}
			}()
			Build(&config.Config{Store: config.StoreConfig{Driver: testCase.store}, Queue: config.QueueConfig{Driver: testCase.driver}}, nil)
		})
	}
}

func TestBetaPGModeRequiresExplicitCreditSkip(t *testing.T) {
	cfg := &config.Config{Store: config.StoreConfig{Driver: "postgres"}, Queue: config.QueueConfig{Driver: "postgres"}}
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "")
	if err := checkQueueReadiness(cfg); err == nil || !strings.Contains(err.Error(), "YUQING_BETA_SKIP_CREDITS") {
		t.Fatalf("default mode err=%v", err)
	}
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	if err := checkQueueReadiness(cfg); err != nil {
		t.Fatalf("explicit beta mode rejected: %v", err)
	}
}

func TestPGWorkerRefusesMissingCrawler(t *testing.T) {
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	err := RunPGWorker(context.Background(), &config.Config{Store: config.StoreConfig{Driver: "postgres"}, Queue: config.QueueConfig{Driver: "postgres"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "engines.query.url") {
		t.Fatalf("missing crawler err=%v", err)
	}
}

func TestAnalysisTaskHandlerRejectsFailedMessages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, testCase := range []struct {
		name, body string
		handleErr  error
		called     bool
	}{
		{"malformed", "{", nil, false},
		{"missing tenant", `{"analysis_id":"a1"}`, nil, false},
		{"handler fails", `{"analysis_id":"a1","tenant_id":"t1"}`, errors.New("fetch failed"), true},
		{"success", `{"analysis_id":"a1","tenant_id":"t1"}`, nil, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			handler := analysisTaskHandler(logger, func(_ context.Context, task analysis.TaskMessage) error {
				called = true
				return testCase.handleErr
			})
			err := handler(context.Background(), queue.Message{Topic: analysis.TopicAnalysisTasks, Body: []byte(testCase.body)})
			if (err != nil) != (testCase.name != "success") || called != testCase.called {
				t.Fatalf("handler err=%v, called=%t", err, called)
			}
		})
	}
}
