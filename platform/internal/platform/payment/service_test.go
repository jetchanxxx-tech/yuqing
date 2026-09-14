package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/credit"
)

// fakeCredits 是 CreditGranter 替身（生产为 credit.Service，幂等语义一致）。
type fakeCredits struct {
	grants    map[string]int // orderID → credits（重复 grant 只记第一次）
	planCodes map[string]string
}

func newFakeCredits() *fakeCredits {
	return &fakeCredits{grants: map[string]int{}, planCodes: map[string]string{}}
}

func (f *fakeCredits) GrantPurchase(_ context.Context, tenantID, orderID string, credits int) error {
	if _, done := f.grants[orderID]; done {
		return nil // 模拟 credit 层按 order_id 幂等
	}
	f.grants[orderID] = credits
	_ = tenantID
	return nil
}

func (f *fakeCredits) SetPlanCode(_ context.Context, tenantID, planCode string) error {
	f.planCodes[tenantID] = planCode
	return nil
}

// newTestService 装配已配置的假渠道。返回 (svc, provider, credits)。
func newTestService(t *testing.T) (*Service, *FakeProvider, *fakeCredits) {
	t.Helper()
	store := NewMemoryStore()
	credits := newFakeCredits()
	prov := NewFakeProvider(ChannelAlipay)
	svc := NewService(store, credits, map[string]Provider{ChannelAlipay: prov}, nil)
	return svc, prov, credits
}

func createOrder(t *testing.T, svc *Service, tenantID, sku, channel string) *Order {
	t.Helper()
	o, err := svc.Create(context.Background(), tenantID, sku, channel)
	if err != nil {
		t.Fatalf("Create order: %v", err)
	}
	return o
}

// ── 下单 ─────────────────────────────────────────────────────────

func TestCreate_usesServerCatalogAndQR(t *testing.T) {
	svc, prov, _ := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)

	if o.AmountCents != 9900 || o.Credits != 4 {
		t.Fatalf("amount=%d credits=%d, want 9900/4（金额必须来自服务端目录）",
			o.AmountCents, o.Credits)
	}
	if o.State != StatePending || o.QRCodeURL == "" {
		t.Fatalf("state=%s qr=%q, want pending + QR", o.State, o.QRCodeURL)
	}
	if o.ExpiresAt.Sub(o.CreatedAt) != orderTTL {
		t.Fatalf("ttl = %v, want %v", o.ExpiresAt.Sub(o.CreatedAt), orderTTL)
	}
	if len(prov.Created) != 1 || prov.Created[0].AmountCents != 9900 {
		t.Fatal("渠道预下单参数必须与目录一致")
	}
}

func TestCreate_rejectsUnknownSkuAndUnconfiguredChannel(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Create(context.Background(), "t1", "nope", ChannelAlipay); err == nil {
		t.Fatal("未知商品必须拒绝")
	}
	unconfigured := NewFakeProvider(ChannelWechat)
	unconfigured.ConfiguredFlag = false
	svc2 := NewService(NewMemoryStore(), newFakeCredits(),
		map[string]Provider{ChannelWechat: unconfigured}, nil)
	if _, err := svc2.Create(context.Background(), "t1", "lite", ChannelWechat); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("未配置渠道 err = %v, want ErrNotConfigured", err)
	}
}

// ── 防线 1：验签 ────────────────────────────────────────────────

func TestCallback_badSignatureRejected(t *testing.T) {
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)

	prov.CallbackErr = ErrBadSignature
	_, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
	if len(credits.grants) != 0 {
		t.Fatal("验签失败绝不入账")
	}
	latest, _ := svc.store.Get(context.Background(), o.ID)
	if latest.State != StatePending {
		t.Fatalf("state = %s, want pending", latest.State)
	}
}

func TestCallback_unknownChannelRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, _, _, err := svc.HandleCallback(context.Background(), "skrill", &CallbackInput{}); err == nil {
		t.Fatal("未知渠道必须拒绝")
	}
}

// ── 防线 2：金额核验 ────────────────────────────────────────────

func TestCallback_amountMismatchNeverGrants(t *testing.T) {
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)

	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-1",
		PaidCents: 1, // 攻击者篡改：1 分钱买 Lite
		Success:   true,
	}
	_, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("err = %v, want ErrAmountMismatch", err)
	}
	if len(credits.grants) != 0 {
		t.Fatal("金额不符绝不入账")
	}
}

// ── 防线 3：幂等 / 状态跃迁 ─────────────────────────────────────

func TestCallback_happyPathGrantsOnceAndUpdatesPlan(t *testing.T) {
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)

	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-1", PaidCents: 9900, Success: true,
	}
	status, _, body, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if body != "success" {
		t.Fatalf("ack body = %q, want success", body)
	}
	_ = status

	latest, _ := svc.store.Get(context.Background(), o.ID)
	if latest.State != StatePaid || !latest.Granted || latest.ProviderTxnID != "txn-1" {
		t.Fatalf("order = %+v", latest)
	}
	if credits.grants[o.ID] != 4 {
		t.Fatalf("grants = %v, want 4 credits", credits.grants)
	}
	if credits.planCodes["t1"] != "lite" {
		t.Fatalf("plan = %q, want lite（套餐购买要更新档位）", credits.planCodes["t1"])
	}

	// 渠道重放同一回调：再次 ACK 但绝不重复入账
	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-1", PaidCents: 9900, Success: true,
	}
	_, _, _, err = svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if err != nil {
		t.Fatalf("replay callback: %v", err)
	}
	if len(credits.grants) != 1 || credits.grants[o.ID] != 4 {
		t.Fatalf("重复回调改变了入账：%v", credits.grants)
	}
}

func TestCallback_txnIdConflictBlocked(t *testing.T) {
	// 同一渠道流水号喂给两个订单：第二个必须被拒（刷单防线）
	svc, prov, credits := newTestService(t)
	o1 := createOrder(t, svc, "t1", "lite", ChannelAlipay)
	o2 := createOrder(t, svc, "t1", "addon_report", ChannelAlipay)

	prov.CallbackResult = &CallbackResult{
		OrderID: o1.ID, ProviderTxnID: "txn-x", PaidCents: 9900, Success: true,
	}
	if _, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatalf("first: %v", err)
	}
	prov.CallbackResult = &CallbackResult{
		OrderID: o2.ID, ProviderTxnID: "txn-x", PaidCents: 6900, Success: true,
	}
	_, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if err == nil {
		t.Fatal("流水号已被订单1占用的回调必须被拒绝（刷单防线）")
	}
	if got := credits.grants[o2.ID]; got != 0 {
		t.Fatalf("订单2入账 %d, want 0", got)
	}
}

func TestCallback_unknownOrderAckedNotErrored(t *testing.T) {
	svc, prov, _ := newTestService(t)
	prov.CallbackResult = &CallbackResult{
		OrderID: "no-such-order", ProviderTxnID: "txn-9", PaidCents: 9900, Success: true,
	}
	_, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{})
	if err != nil {
		t.Fatalf("未知订单应 ACK（防渠道重试风暴），got %v", err)
	}
}

func TestCallback_nonSuccessNotifyIsNoop(t *testing.T) {
	svc, prov, credits := newTestService(t)
	createOrder(t, svc, "t1", "lite", ChannelAlipay)
	prov.CallbackResult = &CallbackResult{Success: false}
	if _, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatalf("中间态通知: %v", err)
	}
	if len(credits.grants) != 0 {
		t.Fatal("中间态通知不得入账")
	}
}

func TestCallback_afterClosedMarksRefundNeeded(t *testing.T) {
	// 渠道关闭订单后仍收到成功回调：钱已扣 → 人工退款流程，不自动发放
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)
	_ = svc.store.MarkClosed(context.Background(), o.ID)

	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-late", PaidCents: 9900, Success: true,
	}
	if _, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatalf("late callback: %v", err)
	}
	latest, _ := svc.store.Get(context.Background(), o.ID)
	if latest.State != StateRefundNeeded {
		t.Fatalf("state = %s, want refund_needed", latest.State)
	}
	if len(credits.grants) != 0 {
		t.Fatal("待退款订单不得自动入账")
	}
}

func TestAddonPurchaseDoesNotChangePlan(t *testing.T) {
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "addon_report", ChannelAlipay)
	credits.planCodes["t1"] = "pro" // 已是 Pro 用户加购

	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-a", PaidCents: 6900, Success: true,
	}
	if _, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatalf("addon callback: %v", err)
	}
	if credits.grants[o.ID] != 1 {
		t.Fatalf("addon grant = %d, want 1", credits.grants[o.ID])
	}
	if credits.planCodes["t1"] != "pro" {
		t.Fatalf("plan = %q, want pro（加购不得改套餐）", credits.planCodes["t1"])
	}
}

// ── 主动查单对账（防「付款了没到账」）─────────────────────────

func TestReconcile_queryPaidSelfHealsLostCallback(t *testing.T) {
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "pro", ChannelAlipay)

	// 回调从未到达；渠道侧实际已支付
	prov.QueryResult = &QueryResult{
		TradeState: StatePaid, ProviderTxnID: "txn-q", PaidCents: 99900,
	}
	got, err := svc.Reconcile(context.Background(), "t1", o.ID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got.State != StatePaid || !got.Granted {
		t.Fatalf("state=%s granted=%t, want paid+granted", got.State, got.Granted)
	}
	if credits.grants[o.ID] != 10 {
		t.Fatalf("grant = %d, want 10", credits.grants[o.ID])
	}
}

func TestReconcile_queryClosesOrder(t *testing.T) {
	svc, prov, _ := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)
	prov.QueryResult = &QueryResult{TradeState: StateClosed}

	got, err := svc.Reconcile(context.Background(), "t1", o.ID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got.State != StateClosed {
		t.Fatalf("state = %s, want closed", got.State)
	}
}

func TestReconcile_paidWithoutGrantSelfHeals(t *testing.T) {
	// 崩溃窗口：已 paid 未 granted。后续任意查询必须补发放（幂等）。
	svc, _, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)
	if ok, _ := svc.store.ClaimPaid(context.Background(), o.ID, "txn-crash"); !ok {
		t.Fatal("claim failed")
	}
	_ = svc.store.SaveChannelMeta(context.Background(), o.ID, "txn-crash", "")

	got, err := svc.Get(context.Background(), "t1", o.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Granted || credits.grants[o.ID] != 4 {
		t.Fatalf("granted=%t grants=%v, want 自愈补发放", got.Granted, credits.grants)
	}
}

func TestGet_crossTenantProbeReturnsNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)
	if _, err := svc.Get(context.Background(), "attacker", o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound（跨租户探测）", err)
	}
}

func TestCallback_expiredButRealPaymentStillGrants(t *testing.T) {
	// 15 分钟后用户才扫码付款：渠道侧真实成功回调必须照常入账
	//（宁可发放，不可「扣了钱不给货」—— 这是防付款未到账的反向面）。
	svc, prov, credits := newTestService(t)
	o := createOrder(t, svc, "t1", "lite", ChannelAlipay)

	svc.now = func() time.Time { return o.CreatedAt.Add(2 * time.Hour) } // 时间旅行
	prov.CallbackResult = &CallbackResult{
		OrderID: o.ID, ProviderTxnID: "txn-t", PaidCents: 9900, Success: true,
	}
	if _, _, _, err := svc.HandleCallback(context.Background(), ChannelAlipay, &CallbackInput{}); err != nil {
		t.Fatalf("expired-but-paid callback: %v", err)
	}
	if credits.grants[o.ID] != 4 {
		t.Fatalf("grant = %d, want 4", credits.grants[o.ID])
	}
	_ = credit.TrialCredits
}
