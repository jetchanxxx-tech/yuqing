package v1_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/api"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/auth"
)

type finalMemoryAuthBarrier struct {
	*auth.SharedTenantStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *finalMemoryAuthBarrier) LoadAuthorizationState(ctx context.Context, uid, tid string) (*auth.AuthorizationState, error) {
	state, err := s.SharedTenantStore.LoadAuthorizationState(ctx, uid, tid)
	if err == nil {
		s.once.Do(func() { close(s.entered); <-s.release })
	}
	return state, err
}
func TestFinalMemoryQueuedMutationsRetainOriginalActor(t *testing.T) {
	for _, kind := range []string{"cancel", "revoke"} {
		for _, change := range []string{"credential_version", "disabled_user", "membership_permission", "unchanged_actor"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				router, deps := newContractEnv(t)
				fixture := contractFixture(t)
				token, _, owner := mustRegister(t, router, "memory-queued@example.invalid", "Memory actor")
				var target, path, method string
				if kind == "cancel" {
					created := adminContractResponse(t, doReq(t, router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "memory queued", "sources": []string{"news"}, "analysis_type": "sentiment"}), 201)
					target = created["id"].(string)
					path, method = "/api/v1/analyses/"+target+"/cancel", http.MethodPost
				} else {
					created := adminContractResponse(t, doReq(t, router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "memory queued"}), 201)
					target = created["id"].(string)
					path, method = "/api/v1/apikeys/"+target, http.MethodDelete
				}
				before, err := deps.Credits.Snapshot(context.Background(), owner["tenant_id"].(string))
				if err != nil {
					t.Fatal(err)
				}
				barrier := &finalMemoryAuthBarrier{SharedTenantStore: fixture.store, entered: make(chan struct{}), release: make(chan struct{})}
				cfg := &config.Config{}
				cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL = testJWTSecret, "15m", "720h"
				deps.Auth = auth.NewService(barrier, testJWTSecret, "15m", "720h")
				router = api.NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
				result := make(chan *httptest.ResponseRecorder, 1)
				go func() { result <- doReq(t, router, method, path, token, nil) }()
				var once sync.Once
				defer once.Do(func() { close(barrier.release) })
				select {
				case <-barrier.entered:
				case <-time.After(10 * time.Second):
					t.Fatal("original memory authorization snapshot not reached")
				}
				if err := fixture.store.UpdateAdministration(context.Background(), func(state *auth.AdministrationState) error {
					user := state.Users[owner["user_id"].(string)]
					switch change {
					case "credential_version":
						user.TokenVersion++
					case "disabled_user":
						user.Status = "disabled"
						user.TokenVersion++
					case "membership_permission":
						for i := range state.Members {
							if state.Members[i].UserID == user.ID {
								state.Members[i].Role = "viewer"
							}
						}
					}
					state.Users[user.ID] = user
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				once.Do(func() { close(barrier.release) })
				var response *httptest.ResponseRecorder
				select {
				case response = <-result:
				case <-time.After(10 * time.Second):
					t.Fatal("memory mutation did not finish")
				}
				want := 403
				if change == "unchanged_actor" {
					want = 200
					if kind == "revoke" {
						want = 204
					}
				}
				if response.Code != want {
					t.Errorf("queued memory %s after %s status=%d want=%d", kind, change, response.Code, want)
				}
				if kind == "cancel" {
					task, err := deps.Analysis.Get(context.Background(), owner["tenant_id"].(string), target)
					if err != nil {
						t.Fatal(err)
					}
					after, err := deps.Credits.Snapshot(context.Background(), owner["tenant_id"].(string))
					if err != nil {
						t.Fatal(err)
					}
					state, balance := "queued", before.Balance
					if change == "unchanged_actor" {
						state = "canceled"
						balance++
					}
					if string(task.State) != state || after.Balance != balance {
						t.Errorf("revoked memory cancellation changed state=%s/balance=%d", task.State, after.Balance)
					}
				} else {
					keys, err := deps.APIKey.ListKeys(context.Background(), owner["tenant_id"].(string))
					if err != nil {
						t.Fatal(err)
					}
					if len(keys) != 1 || keys[0].IsRevoked() != (change == "unchanged_actor") {
						t.Error("memory key changed after authority revoke")
					}
				}
			})
		}
	}
}
