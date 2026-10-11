package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/usage"
)

// Only outbound precreate/query are replaced. Callback verification exercises
// the real Alipay adapter and SDK RSA2 verification with ephemeral sandbox keys.
type billingSandboxAlipay struct {
	*AlipayProvider
	paid map[string]*QueryResult
}

func (p *billingSandboxAlipay) CreatePayment(_ context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	return &CreatePaymentResp{QRCodeURL: "sandbox://order/" + req.OrderID}, nil
}
func (p *billingSandboxAlipay) QueryOrder(_ context.Context, id string) (*QueryResult, error) {
	if result := p.paid[id]; result != nil {
		return result, nil
	}
	return &QueryResult{TradeState: StatePending}, nil
}

func TestSandboxPlanPurchaseGrantAndUseRemainsCatalogBasedPG(t *testing.T) {
	pool := pgtest.Pool(t, "catalog_billing_integration")
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO users(id,email,password_hash,status) VALUES('normal','ordinary@example.invalid','fixture','active'),('fixed','admin@pangu.com','fixture','active')`,
		`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('team','team','team','team','active')`,
		`INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('team','normal','tenant_admin'),('team','fixed','tenant_admin')`,
		`INSERT INTO platform_user_roles(user_id,role) VALUES('fixed','platform_admin')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAlipayProvider(AlipayConfig{Enabled: true, Sandbox: true, AppID: "isolated-sandbox-app", PrivateKey: base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key)), AlipayPublicKey: base64.StdEncoding.EncodeToString(pub)})
	if err != nil {
		t.Fatal(err)
	}
	provider := &billingSandboxAlipay{AlipayProvider: adapter, paid: map[string]*QueryResult{}}
	credits := credit.NewService(credit.NewPGStore(pool))
	service := NewService(NewPGStore(pool), credits, map[string]Provider{ChannelAlipay: provider}, nil)
	signed := func(orderID, transactionID string, cents int) *CallbackInput {
		t.Helper()
		form := map[string]string{"app_id": "isolated-sandbox-app", "out_trade_no": orderID, "trade_no": transactionID, "trade_status": "TRADE_SUCCESS", "total_amount": fmt.Sprintf("%d.%02d", cents/100, cents%100), "notify_type": "trade_status_sync", "sign_type": "RSA2"}
		keys := []string{}
		for name := range form {
			if name != "sign_type" {
				keys = append(keys, name)
			}
		}
		sort.Strings(keys)
		parts := []string{}
		for _, name := range keys {
			parts = append(parts, name+"="+form[name])
		}
		digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		form["sign"] = base64.StdEncoding.EncodeToString(signature)
		return &CallbackInput{Form: form}
	}
	lite, err := service.Create(ctx, "team", "lite", ChannelAlipay)
	if err != nil {
		t.Fatal(err)
	}
	if lite.AmountCents != 9900 || lite.Credits != 4 {
		t.Fatalf("catalog price/credits=%d/%d", lite.AmountCents, lite.Credits)
	}
	invalid := signed(lite.ID, "lite-transaction", 9900)
	invalid.Form["total_amount"] = "1.00"
	if _, _, _, err := service.HandleCallback(ctx, ChannelAlipay, invalid); err == nil {
		t.Fatal("tampered signature granted credits")
	}
	if _, _, _, err := service.HandleCallback(ctx, ChannelAlipay, signed(lite.ID, "lite-transaction", 1)); err != ErrAmountMismatch {
		t.Fatalf("signed wrong amount accepted: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, _, _, err := service.HandleCallback(ctx, ChannelAlipay, signed(lite.ID, "lite-transaction", 9900)); err != nil {
			t.Fatal(err)
		}
	}
	provider.paid[lite.ID] = &QueryResult{TradeState: StatePaid, ProviderTxnID: "lite-transaction", PaidCents: 9900}
	if _, err := service.Reconcile(ctx, "team", lite.ID); err != nil {
		t.Fatal(err)
	}
	balance, err := credits.Balance(ctx, "team")
	if err != nil || balance != 4 {
		t.Fatalf("duplicate payment granted balance=%d/%v", balance, err)
	}
	q := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	defer q.Close()
	analyses := analysis.NewPGService(pool, q, 1)
	paid, err := analyses.Create(ctx, analysis.CreateAnalysisRequest{TenantID: "team", UserID: "normal", Name: "purchased ordinary run"})
	if err != nil {
		t.Fatal(err)
	}
	balance, _ = credits.Balance(ctx, "team")
	if balance != 3 {
		t.Fatalf("ordinary purchased run balance=%d", balance)
	}
	if _, err := billingpolicy.NewService(pool).Bind(ctx, "fixed", true); err != nil {
		t.Fatal(err)
	}
	free, err := analyses.Create(ctx, analysis.CreateAnalysisRequest{TenantID: "team", UserID: "fixed", Name: "fixed exempt run"})
	if err != nil {
		t.Fatal(err)
	}
	meter := usage.NewCallService(pool, "isolated-sandbox-secret", "sandbox-CNY-price", []config.ModelConfig{{ID: "sandbox-model", Provider: "sandbox", InputCostPerM: 4, OutputCostPerM: 16}})
	for index, a := range []*analysis.AnalysisResult{paid, free} {
		call := fmt.Sprintf("call-%d", index)
		permit, err := meter.Authorize(ctx, usage.CallAuthorizationRequest{CallID: call, RunID: a.CurrentRunID, Engine: "insight", Phase: "analyze", Attempt: 1, Model: "sandbox-model"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = meter.Record(ctx, usage.ProviderUsageEvent{EventID: "event-" + call, CallID: call, EventVersion: 1, Attempt: 1, ActualModel: "sandbox-model", PromptTokens: 400, CompletionTokens: 100, UsageStatus: "reported", Outcome: "sandbox_response"}, permit.Permit); err != nil {
			t.Fatal(err)
		}
	}
	if err := analyses.Cancel(ctx, "team", free.ID); err != nil {
		t.Fatal(err)
	}
	balance, _ = credits.Balance(ctx, "team")
	if balance != 3 {
		t.Fatalf("free cancellation refunded someone else: %d", balance)
	}
	if err := analyses.Cancel(ctx, "team", paid.ID); err != nil {
		t.Fatal(err)
	}
	balance, _ = credits.Balance(ctx, "team")
	if balance != 4 {
		t.Fatalf("paid current-run refund balance=%d", balance)
	}
	addon, err := service.Create(ctx, "team", "addon_report", ChannelAlipay)
	if err != nil {
		t.Fatal(err)
	}
	if addon.AmountCents != 6900 || addon.Credits != 1 {
		t.Fatalf("addon price/credits=%d/%d", addon.AmountCents, addon.Credits)
	}
	for i := 0; i < 2; i++ {
		if _, _, _, err := service.HandleCallback(ctx, ChannelAlipay, signed(addon.ID, "addon-transaction", 6900)); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := credits.PlanCode(ctx, "team")
	if err != nil || plan != "lite" {
		t.Fatalf("addon changed plan %s/%v", plan, err)
	}
	balance, _ = credits.Balance(ctx, "team")
	if balance != 5 {
		t.Fatalf("addon duplicate grant balance=%d", balance)
	}
	var purchases, consumes, refunds int
	var actual, quota, cost, billed int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE reason='purchase'),count(*) FILTER(WHERE reason='consume'),count(*) FILTER(WHERE reason='refund') FROM credit_transactions WHERE tenant_id='team'`).Scan(&purchases, &consumes, &refunds); err != nil {
		t.Fatal(err)
	}
	if purchases != 2 || consumes != 1 || refunds != 1 {
		t.Fatalf("financial entries=%d/%d/%d", purchases, consumes, refunds)
	}
	if err := pool.QueryRow(ctx, `SELECT sum(prompt_tokens+completion_tokens),sum(quota_tokens),sum(cost_micro_cny),sum(billed_micro_cny) FROM usage_events WHERE tenant_id='team'`).Scan(&actual, &quota, &cost, &billed); err != nil {
		t.Fatal(err)
	}
	if actual != 1000 || quota != 500 || cost != 6400 || billed != 0 {
		t.Fatalf("supplier facts/quotas/user charge=%d/%d/%d/%d", actual, quota, cost, billed)
	}
}
