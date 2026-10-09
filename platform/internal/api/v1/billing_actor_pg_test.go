package v1_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/api"
	"github.com/yuqing/platform/internal/api/v1"
	"github.com/yuqing/platform/internal/app"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// This fixture uses the actual composition root and an isolated PostgreSQL
// schema. It cannot create a production account or contact a supplier.
type billingActorPGEnv struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	deps   *v1.Services
	router *gin.Engine
}

func newBillingActorPGEnv(t *testing.T) *billingActorPGEnv {
	t.Helper()
	pool := pgtest.Pool(t, "billing_actor_http")
	dsn, err := url.Parse(os.Getenv(pgtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	params := dsn.Query()
	params.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = params.Encode()
	// K4 removes this old startup guard only after atomic admission lands.
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	t.Setenv("YUQING_BOOTSTRAP_ADMIN_EMAIL", "")
	cfg := &config.Config{}
	cfg.Store.Driver, cfg.Queue.Driver, cfg.DB.Primary = "postgres", "postgres", dsn.String()
	cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL = testJWTSecret, "15m", "720h"
	e := &billingActorPGEnv{pool: pool, cfg: cfg}
	e.rebuild()
	t.Cleanup(func() { e.deps.PGPool.Close() })
	return e
}

func (e *billingActorPGEnv) rebuild() {
	if e.deps != nil {
		e.deps.PGPool.Close()
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.deps = app.Build(e.cfg, logger)
	e.router = api.NewRouter(e.cfg, logger, e.deps)
}

func TestBillingActorPGKeyCreatorIsServerAssignedImmutableAndPersistent(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, owner := mustRegister(t, e.router, "key-owner@example.invalid", "Key owner")
	_, _, other := mustRegister(t, e.router, "other-key-owner@example.invalid", "Other owner")
	created := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{
		"name": "trusted owner", "scopes": []string{},
		"creator_user_id": other["user_id"], "user_id": other["user_id"], "tenant_id": other["tenant_id"],
	}), http.StatusCreated)
	if created["creator_user_id"] != owner["user_id"] || created["tenant_id"] != owner["tenant_id"] {
		t.Fatalf("key creator/tenant must come from current server identity: %v", created)
	}
	keyID := created["id"].(string)
	raw := created["api_key"].(string)
	e.rebuild()
	listed := doReq(t, e.router, http.MethodGet, "/api/v1/apikeys", token, nil)
	body := adminContractResponse(t, listed, http.StatusOK)
	keys, ok := body["keys"].([]any)
	if !ok || len(keys) != 1 || keys[0].(map[string]any)["creator_user_id"] != owner["user_id"] {
		t.Fatalf("creator must survive service reconstruction: %v", body)
	}
	if strings.Contains(listed.Body.String(), raw) || strings.Contains(listed.Body.String(), "key_hash") {
		t.Fatal("key listing must not return the raw credential or hash")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE api_keys SET creator_user_id=$2 WHERE id=$1`, keyID, other["user_id"]); err == nil {
		t.Fatal("a stored key cannot transfer its billing creator")
	}
	var creator string
	if err := e.pool.QueryRow(context.Background(), `SELECT creator_user_id FROM api_keys WHERE id=$1`, keyID).Scan(&creator); err != nil {
		t.Fatal(err)
	}
	if creator != owner["user_id"] {
		t.Fatalf("creator changed: %s", creator)
	}
}

func TestBillingActorPGLegacyUnknownKeyCanReadButCannotCreateCharges(t *testing.T) {
	e := newBillingActorPGEnv(t)
	_, _, account := mustRegister(t, e.router, "historical-key@example.invalid", "Historical key")
	raw := "pangu_isolated_historical_unknown_creator"
	sum := sha256.Sum256([]byte(raw))
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO api_keys(id,tenant_id,name,key_hash,scopes,prefix)
		VALUES('historical-key',$1,'legacy unknown owner',$2,'[]','pangu_legacy')`, account["tenant_id"], hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/analyses", raw, nil), http.StatusOK)
	rejected := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", raw, map[string]any{
		"name": "unverified creator cannot be charged", "user_id": account["user_id"], "billing_exempt": true,
	}), http.StatusForbidden)
	if rejected["code"] != "API_KEY_OWNER_UNVERIFIED" {
		t.Fatalf("unknown key owner needs actionable code: %v", rejected)
	}
	var analyses, consumes, messages int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM analyses),
		(SELECT count(*) FROM credit_transactions WHERE reason='consume'),(SELECT count(*) FROM queue_messages)`).Scan(&analyses, &consumes, &messages); err != nil {
		t.Fatal(err)
	}
	if analyses != 0 || consumes != 0 || messages != 0 {
		t.Fatalf("rejected key wrote side effects: %d/%d/%d", analyses, consumes, messages)
	}
}

func TestBillingActorPGKnownOwnerKeyRetainsMachinePermissionsAndRejectsDisabledOwner(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, owner := mustRegister(t, e.router, "platform-key-owner@example.invalid", "Platform key owner")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, owner["user_id"]); err != nil {
		t.Fatal(err)
	}
	created := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "owner status boundary"}), http.StatusCreated)
	raw := created["api_key"].(string)
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/users", raw, nil), http.StatusForbidden)
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/user/profile", raw, nil), http.StatusForbidden)
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/analyses", raw, nil), http.StatusOK)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE id=$1`, owner["user_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/analyses", raw, nil), http.StatusUnauthorized)
}

// The request authenticates before waiting for the same exclusive lock used by
// account administration. Revocation in that window must prevent a durable key.
func TestBillingActorPGQueuedKeyCreationRechecksOriginalJWTAndPermission(t *testing.T) {
	for _, kind := range []string{"credential_version", "membership_permission"} {
		t.Run(kind, func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			token, _, owner := mustRegister(t, e.router, "queued-key-"+kind+"@example.invalid", "Queued key owner")
			dsn, err := url.Parse(e.cfg.DB.Primary)
			if err != nil {
				t.Fatal(err)
			}
			application := "k4-key-race-" + kind
			values := dsn.Query()
			values.Set("application_name", application)
			dsn.RawQuery = values.Encode()
			e.cfg.DB.Primary = dsn.String()
			e.rebuild()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			blocker, err := e.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
				t.Fatal(err)
			}
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response <- doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "must not survive revocation"})
			}()
			for {
				var waiting bool
				if err = e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock_shared%')`, application).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case result := <-response:
					t.Fatalf("request did not wait for administration transaction: %d %s", result.Code, result.Body.String())
				case <-ctx.Done():
					t.Fatal("request never reached transaction lock")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if kind == "credential_version" {
				_, err = blocker.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, owner["user_id"])
			} else {
				// Permission itself is rechecked even if a historic administrator failed to
				// increment a credential version when changing membership.
				_, err = blocker.Exec(ctx, `UPDATE tenant_members SET role='viewer' WHERE tenant_id=$1 AND user_id=$2`, owner["tenant_id"], owner["user_id"])
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-response:
				adminContractResponse(t, result, http.StatusForbidden)
			case <-ctx.Done():
				t.Fatal("queued request did not complete")
			}
			var count int
			if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE tenant_id=$1`, owner["tenant_id"]).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("revoked request created %d durable keys", count)
			}
		})
	}
}
