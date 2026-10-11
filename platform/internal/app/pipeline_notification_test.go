package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/api/v1"
	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"github.com/yuqing/platform/internal/platform/accountnotify"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/testsupport/notificationsandbox"
)

type completionSandboxTransport struct {
	inbox    notificationsandbox.Inbox
	messages []int
	tasks    int
}

func (s *completionSandboxTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.resend.com" || r.URL.Path != "/emails" || r.Method != "POST" {
		return nil, errors.New("only isolated Resend SDK requests allowed")
	}
	var payload struct {
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.To) != 1 || payload.To[0] != "completion@example.invalid" {
		return nil, errors.New("non-sandbox recipient")
	}
	s.messages = append(s.messages, s.inbox.Accept(payload.To[0], payload.Subject, payload.HTML))
	if strings.Contains(payload.Subject, "分析任务已完成") {
		s.tasks++
	}
	return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"id":"completion-sandbox-%d"}`, len(s.messages))))}, nil
}

type completionFetcher struct{ fail bool }

func (f completionFetcher) Fetch(context.Context, analysis.FetchRequest) ([]analysis.Document, error) {
	if f.fail {
		return nil, errors.New("isolated collection failure")
	}
	return []analysis.Document{{ID: "completion-doc", Content: "isolated evidence", SourceType: "news"}}, nil
}
func completionFixture(t *testing.T) (*v1.Services, *auth.Principal, *completionSandboxTransport) {
	t.Helper()
	// Build installs the real notification store/hook. Query URL is unset so no
	// background worker/network runs; Handle drives the real lifecycle below.
	t.Setenv("YUQING_PUBLIC_BASE_URL", "https://example.invalid")
	t.Setenv("YUQING_BOOTSTRAP_ADMIN_EMAIL", "")
	cfg := &config.Config{Store: config.StoreConfig{Driver: "memory"}, Queue: config.QueueConfig{Driver: "memory"}}
	cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL = "completion-test-secret-minimum-32-chars", "15m", "1h"
	cfg.Storage.AvatarRoot = t.TempDir()
	services := Build(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for key, value := range map[string]string{"email_provider": "resend", "resend_api_key": "isolated-fake-key", "email_from_address": "sender@example.invalid", "email_from_name": "Sandbox"} {
		if err := services.Settings.Set(context.Background(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	sandbox := &completionSandboxTransport{}
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: sandbox}
	t.Cleanup(func() { http.DefaultClient = previous })
	actor, pair, err := services.Auth.Register(context.Background(), "completion@example.invalid", "Completion12345", "Completion")
	if err != nil {
		t.Fatal(err)
	}
	actor, err = services.Auth.Authenticate(context.Background(), pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Credits.GrantPurchase(context.Background(), actor.TenantID, "isolated-credit-fixture", 20); err != nil {
		t.Fatal(err)
	}
	return services, actor, sandbox
}
func verifyCompletionActor(t *testing.T, services *v1.Services, actor *auth.Principal, sandbox *completionSandboxTransport) {
	t.Helper()
	if err := services.Auth.SendVerificationEmail(context.Background(), actor.UserID, "", actor.TokenVersion); err != nil {
		t.Fatal(err)
	}
	index := sandbox.messages[len(sandbox.messages)-1]
	if err := sandbox.inbox.Deliver(index); err != nil {
		t.Fatal(err)
	}
	message, err := sandbox.inbox.Delivered(index)
	if err != nil {
		t.Fatal(err)
	}
	token, err := url.QueryUnescape(strings.Split(strings.Split(message.Payload, "token=")[1], "\"")[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Auth.VerifyEmail(context.Background(), token); err != nil {
		t.Fatal(err)
	}
}
func completePipelineTask(t *testing.T, services *v1.Services, actor *auth.Principal, fetcher completionFetcher, cancel bool) (analysis.TaskMessage, *analysis.Pipeline) {
	t.Helper()
	task, err := services.Analysis.Create(context.Background(), analysis.CreateAnalysisRequest{TenantID: actor.TenantID, UserID: actor.UserID, Name: "actual memory lifecycle", Sources: []string{"news"}})
	if err != nil {
		t.Fatal(err)
	}
	if cancel {
		if err := services.Analysis.Cancel(context.Background(), actor.TenantID, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	pipeline := analysis.NewPipeline(services.Analysis, fetcher, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	message := analysis.TaskMessage{AnalysisID: task.ID, TenantID: actor.TenantID}
	err = pipeline.Handle(context.Background(), message)
	if (err != nil) != fetcher.fail {
		t.Fatalf("pipeline result=%v fail=%t", err, fetcher.fail)
	}
	result, err := services.Analysis.Get(context.Background(), actor.TenantID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := analysis.StateCompleted
	if fetcher.fail {
		want = analysis.StateFailed
	}
	if cancel {
		want = analysis.StateCanceled
	}
	if result.State != want || result.FinishedAt.IsZero() {
		t.Fatalf("terminal state=%s want=%s", result.State, want)
	}
	return message, pipeline
}
func dispatchCompletion(t *testing.T, services *v1.Services, sandbox *completionSandboxTransport, want int) {
	t.Helper()
	if err := services.TaskNotifications.Dispatch(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if sandbox.tasks != want {
		t.Fatalf("actual pipeline task SDK sends=%d want=%d", sandbox.tasks, want)
	}
}
func TestMemoryPipelineCompletionNotifiesOnceAndFreshRerun(t *testing.T) {
	services, actor, box := completionFixture(t)
	verifyCompletionActor(t, services, actor, box)
	message, pipeline := completePipelineTask(t, services, actor, completionFetcher{}, false)
	dispatchCompletion(t, services, box, 1)
	if err := pipeline.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	dispatchCompletion(t, services, box, 1)
	if err := services.Analysis.Rerun(context.Background(), actor.TenantID, message.AnalysisID); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.Handle(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	dispatchCompletion(t, services, box, 2)
	dispatchCompletion(t, services, box, 2)
}
func TestMemoryPipelineFailureCancellationAndPreferencesNeverSend(t *testing.T) {
	services, actor, box := completionFixture(t)
	completePipelineTask(t, services, actor, completionFetcher{}, false)
	dispatchCompletion(t, services, box, 0) // Unverified completion cannot later reactivate.
	verifyCompletionActor(t, services, actor, box)
	dispatchCompletion(t, services, box, 0)
	completePipelineTask(t, services, actor, completionFetcher{fail: true}, false)
	completePipelineTask(t, services, actor, completionFetcher{}, true)
	dispatchCompletion(t, services, box, 0)
	if err := services.TaskNotifications.SetPreferences(context.Background(), *actor, accountnotify.Preferences{TaskCompleted: false}); err != nil {
		t.Fatal(err)
	}
	completePipelineTask(t, services, actor, completionFetcher{}, false)
	dispatchCompletion(t, services, box, 0)
	if err := services.TaskNotifications.SetPreferences(context.Background(), *actor, accountnotify.Preferences{TaskCompleted: true}); err != nil {
		t.Fatal(err)
	}
	dispatchCompletion(t, services, box, 0)
	completePipelineTask(t, services, actor, completionFetcher{}, false)
	dispatchCompletion(t, services, box, 1)
}
func TestMemoryPipelineClosureCancelsUnsentCompletionAcrossWithdrawal(t *testing.T) {
	services, actor, box := completionFixture(t)
	verifyCompletionActor(t, services, actor, box)
	completePipelineTask(t, services, actor, completionFetcher{}, false)
	user, err := services.Auth.ClosureCredentialSnapshot(context.Background(), *actor, "Completion12345", "192.0.2.60")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Closure.Request(context.Background(), accountclosure.Actor{UserID: user.ID, Version: user.TokenVersion, PasswordHash: user.PasswordHash}, []string{actor.TenantID}); err != nil {
		t.Fatal(err)
	}
	dispatchCompletion(t, services, box, 0)
	actor.TokenVersion++
	user, err = services.Auth.ClosureCredentialSnapshot(context.Background(), *actor, "Completion12345", "192.0.2.61")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := services.Closure.Cancel(context.Background(), accountclosure.Actor{UserID: user.ID, Version: user.TokenVersion, PasswordHash: user.PasswordHash}); err != nil {
		t.Fatal(err)
	}
	actor.TokenVersion++
	dispatchCompletion(t, services, box, 0)
	completePipelineTask(t, services, actor, completionFetcher{}, false)
	dispatchCompletion(t, services, box, 1)
}
