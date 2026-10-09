package v1_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/yuqing/platform/internal/engine"
	"github.com/yuqing/platform/internal/platform/payment"
)

type legacyOwnerPaymentProbe struct {
	*payment.FakeProvider
	queries int
}

func (p *legacyOwnerPaymentProbe) QueryOrder(ctx context.Context, id string) (*payment.QueryResult, error) {
	p.queries++
	return p.FakeProvider.QueryOrder(ctx, id)
}
func legacyOwnerFixture(t *testing.T) (*billingActorPGEnv, string, string, map[string]any) {
	t.Helper()
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "historical-read-boundary@example.invalid", "Historical read boundary")
	raw := "pangu_isolated_unknown_owner_read_only"
	digest := sha256.Sum256([]byte(raw))
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO api_keys(id,tenant_id,name,key_hash,scopes,prefix) VALUES('unknown-owner',$1,'historical unknown',$2,'[]','pangu_old')`, account["tenant_id"], hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	return e, token, raw, account
}
func assertUnknownOwnerRefused(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	body := adminContractResponse(t, response, http.StatusForbidden)
	if body["code"] != "API_KEY_OWNER_UNVERIFIED" {
		t.Fatalf("unknown owner needs actionable refusal: %v", body)
	}
}

func TestBillingLegacyUnknownKeyPGCannotMutateDraftsOrCreateOrders(t *testing.T) {
	for _, operation := range []string{"preview", "create_draft", "patch_draft", "delete_draft", "create_order"} {
		t.Run(operation, func(t *testing.T) {
			e, token, raw, account := legacyOwnerFixture(t)
			ctx := context.Background()
			previewRequest := map[string]any{"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "fixture brand"}}
			preview := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/monitor-plans/preview", token, previewRequest), http.StatusOK)
			body := map[string]any{"name": "existing draft", "template_id": "brand_daily", "template_version": 1, "analysis_type": "brand", "inputs": previewRequest["inputs"], "config": preview["config"]}
			existing := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/monitor-plans", token, body), http.StatusCreated)
			id := existing["plan_id"].(string)
			// Real historical machine-owned asset, without fabricating a user row.
			if _, err := e.pool.Exec(ctx, `UPDATE monitor_plans SET owner_id='apikey:unknown-owner' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			provider := payment.NewFakeProvider(payment.ChannelAlipay)
			provider.CreateResp = &payment.CreatePaymentResp{QRCodeURL: "sandbox://pending"}
			e.deps.Payment = payment.NewService(payment.NewPGStore(e.pool), e.deps.Credits, map[string]payment.Provider{payment.ChannelAlipay: provider}, nil)
			switch operation {
			case "preview":
				assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodPost, "/api/v1/monitor-plans/preview", raw, previewRequest))
			case "create_draft":
				assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodPost, "/api/v1/monitor-plans", raw, body))
			case "patch_draft":
				assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodPatch, "/api/v1/monitor-plans/"+id, raw, map[string]any{"revision": 1, "name": "must not write"}))
			case "delete_draft":
				assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodDelete, "/api/v1/monitor-plans/"+id, raw, map[string]any{"revision": 1}))
			case "create_order":
				assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodPost, "/api/v1/billing/orders", raw, map[string]any{"sku_code": "lite", "channel": "alipay"}))
			}
			var drafts, orders, revision int
			var name string
			if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM monitor_plans),(SELECT count(*) FROM orders),revision,name FROM monitor_plans WHERE id=$1`, id).Scan(&drafts, &orders, &revision, &name); err != nil {
				t.Fatal(err)
			}
			if drafts != 1 || orders != 0 || revision != 1 || name != "existing draft" || len(provider.Created) != 0 {
				t.Fatalf("unknown owner mutated state/provider: %d/%d/%d/%s/%d", drafts, orders, revision, name, len(provider.Created))
			}
			adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/monitor-plans/"+id, raw, nil), http.StatusOK)
			_ = account
		})
	}
}

func TestBillingLegacyUnknownKeyPGOrderReadNeverReconcilesOrGrants(t *testing.T) {
	for _, state := range []string{"pending", "paid_ungranted", "pending_paypage"} {
		t.Run(state, func(t *testing.T) {
			e, token, raw, account := legacyOwnerFixture(t)
			ctx := context.Background()
			channel := payment.ChannelAlipay
			if state == "pending_paypage" {
				channel = payment.ChannelUnionPay
			}
			fake := payment.NewFakeProvider(channel)
			fake.CreateResp = &payment.CreatePaymentResp{QRCodeURL: "sandbox://pending"}
			if state == "pending_paypage" {
				fake.CreateResp.QRCodeURL = "<form>existing sandbox cashier</form>"
			}
			provider := &legacyOwnerPaymentProbe{FakeProvider: fake}
			store := payment.NewPGStore(e.pool)
			e.deps.Payment = payment.NewService(store, e.deps.Credits, map[string]payment.Provider{channel: provider}, nil)
			order, err := e.deps.Payment.Create(ctx, account["tenant_id"].(string), "lite", channel)
			if err != nil {
				t.Fatal(err)
			}
			fake.QueryResult = &payment.QueryResult{TradeState: payment.StatePaid, ProviderTxnID: "sandbox-order-paid", PaidCents: 9900}
			expectedState := payment.StatePending
			if state == "paid_ungranted" {
				if _, err := e.pool.Exec(ctx, `UPDATE orders SET state='paid',provider_txn_id='sandbox-order-paid',paid_at=now() WHERE id=$1`, order.ID); err != nil {
					t.Fatal(err)
				}
				expectedState = payment.StatePaid
			}
			read := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/orders/"+order.ID, raw, nil), http.StatusOK)
			returned := read["order"].(map[string]any)
			balance, err := e.deps.Credits.Balance(ctx, account["tenant_id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.Get(ctx, order.ID)
			if err != nil {
				t.Fatal(err)
			}
			if returned["state"] != expectedState || stored.Granted || stored.State != expectedState || balance != 1 || provider.queries != 0 {
				t.Fatalf("read-only key performed settlement: response=%v order=%+v balance=%d queries=%d", returned, stored, balance, provider.queries)
			}
			_, _, other := mustRegister(t, e.router, "foreign-order@example.invalid", "Other order tenant")
			foreign, err := e.deps.Payment.Create(ctx, other["tenant_id"].(string), "lite", channel)
			if err != nil {
				t.Fatal(err)
			}
			adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/orders/"+foreign.ID, raw, nil), http.StatusNotFound)
			if state == "pending_paypage" {
				page := doReq(t, e.router, http.MethodGet, "/api/v1/billing/orders/"+order.ID+"/paypage?token="+token, raw, nil)
				if page.Code != http.StatusOK || page.Body.String() != "<form>existing sandbox cashier</form>" {
					t.Fatalf("stored cashier read=%d/%s", page.Code, page.Body.String())
				}
				unchanged, _ := store.Get(ctx, order.ID)
				if provider.queries != 0 || unchanged.Granted || unchanged.State != payment.StatePending {
					t.Fatal("cashier read invoked payment work")
				}
			}
			known := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "known order reader"}), http.StatusCreated)
			adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/orders/"+order.ID, known["api_key"].(string), nil), http.StatusOK)
			balance, _ = e.deps.Credits.Balance(ctx, account["tenant_id"].(string))
			if balance != 5 {
				t.Fatalf("known owner lost existing settlement: %d", balance)
			}
		})
	}
}

func TestBillingLegacyUnknownKeyPGCanReadReadyHTMLButCannotRequestGeneration(t *testing.T) {
	e, token, raw, account := legacyOwnerFixture(t)
	ctx := context.Background()
	if err := e.deps.Credits.SetPlanCode(ctx, account["tenant_id"].(string), "pro"); err != nil {
		t.Fatal(err)
	}
	analysis := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "ready historical report"}), http.StatusCreated)
	if _, err := e.pool.Exec(ctx, `UPDATE analyses SET report_content='<p>ready stored report</p>' WHERE id=$1`, analysis["id"]); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"html", "docx"} {
		if _, err := e.pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,analysis_id,format,status,file_key,created_by,report_version) VALUES($1,$2,$3,$4,'completed','sandbox',$5,1)`, "ready-"+format, account["tenant_id"], analysis["id"], format, account["user_id"]); err != nil {
			t.Fatal(err)
		}
	}
	var generated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generated.Add(1)
		_, _ = w.Write([]byte("sandbox generated docx"))
	}))
	defer server.Close()
	e.deps.ReportEngine = engine.NewRealReportEngine(server.URL, "", nil)
	html := doReq(t, e.router, http.MethodGet, "/api/v1/reports/ready-html/download?format=html", raw, nil)
	if html.Code != http.StatusOK || html.Body.String() != "<p>ready stored report</p>" {
		t.Fatalf("ready static read lost: %d/%s", html.Code, html.Body.String())
	}
	assertUnknownOwnerRefused(t, doReq(t, e.router, http.MethodGet, "/api/v1/reports/ready-docx/download?format=docx", raw, nil))
	if generated.Load() != 0 {
		t.Fatal("unknown owner requested new engine generation")
	}
}
