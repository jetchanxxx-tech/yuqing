package payment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yuqing/platform/internal/platform/billing"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// 订单有效期：二维码超时关闭。渠道侧通常 2h-24h，我们收窄到 15 分钟
// （价格类信息新鲜度 + 减少悬挂订单）。
const orderTTL = 15 * time.Minute

// CreditGranter 是额度发放契约（生产实现为 credit.Service）。
type CreditGranter interface {
	// GrantPurchase 按订单幂等入账（同一订单重复调用只入一次）。
	GrantPurchase(ctx context.Context, tenantID, orderID string, credits int) error
	// SetPlanCode 记录租户当前套餐（购买套餐 SKU 时）。
	SetPlanCode(ctx context.Context, tenantID, planCode string) error
}

// Service 是订单核验服务：下单 / 回调核销 / 主动查单对账。
type Service struct {
	store     Store
	credits   CreditGranter
	providers map[string]Provider
	// reload 生产经 Registry 注入：每次取渠道实时读配置（内部有哈希缓存），
	// admin 后台改配置零重启生效。nil 时用静态 providers（测试）。
	reload func(ctx context.Context) (map[string]Provider, error)
	log    *slog.Logger
	now    func() time.Time
}

// NewService 装配订单服务。providers 注册全部渠道实现（含未配置的）。
func NewService(store Store, credits CreditGranter, providers map[string]Provider, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:     store,
		credits:   credits,
		providers: providers,
		log:       log,
		now:       time.Now,
	}
}

// SetProviderReload 注入渠道解析器（配置热更新）。
func (s *Service) SetProviderReload(fn func(ctx context.Context) (map[string]Provider, error)) {
	s.reload = fn
}

// provider 取渠道实现；reload 优先，静态 map 兜底。
func (s *Service) provider(ctx context.Context, channel string) (Provider, error) {
	if s.reload != nil {
		m, err := s.reload(ctx)
		if err != nil {
			return nil, err
		}
		p, ok := m[channel]
		if !ok {
			return nil, ErrNotConfigured
		}
		return p, nil
	}
	p, ok := s.providers[channel]
	if !ok {
		return nil, ErrNotConfigured
	}
	return p, nil
}

// Create 创建订单并发起渠道预下单，返回带二维码的 pending 订单。
// 金额与额度一律取自服务端目录（billing.ResolveSKU），绝不信任前端。
func (s *Service) Create(ctx context.Context, tenantID, skuCode, channel string) (*Order, error) {
	sku := billing.ResolveSKU(skuCode)
	if sku == nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "payment: 未知商品 "+skuCode)
	}
	p, err := s.provider(ctx, channel)
	if err != nil {
		return nil, err
	}
	if !p.Configured() {
		return nil, ErrNotConfigured
	}

	o := &Order{
		ID:          id.New(),
		TenantID:    tenantID,
		SKUCode:     sku.Code,
		Kind:        sku.Kind,
		Credits:     sku.Credits,
		AmountCents: sku.PriceCents,
		Channel:     channel,
		State:       StatePending,
		ExpiresAt:   s.now().Add(orderTTL),
		CreatedAt:   s.now(),
	}

	resp, err := p.CreatePayment(ctx, &CreatePaymentReq{
		OrderID:     o.ID,
		Subject:     "盘古舆情 · " + sku.Name,
		AmountCents: o.AmountCents,
	})
	if err != nil {
		return nil, fmt.Errorf("payment: 渠道预下单失败: %w", err)
	}
	o.QRCodeURL = resp.QRCodeURL
	o.ProviderTxnID = resp.ProviderTxnID
	o.TxnTime = resp.TxnTime

	if err := s.store.Create(ctx, o); err != nil {
		return nil, err
	}
	// 渠道元数据（预下单号/银联 txnTime）随单落库，查单与核销依赖
	if resp.ProviderTxnID != "" || resp.TxnTime != "" {
		if err := s.store.SaveChannelMeta(ctx, o.ID, resp.ProviderTxnID, resp.TxnTime); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// Get 查询订单（租户隔离）。paid 但未发放的订单顺带自愈 —— 发放是幂等的，
// 崩溃窗口（已 paid 未 granted）由每次查询补齐。
func (s *Service) Get(ctx context.Context, tenantID, orderID string) (*Order, error) {
	o, err := s.store.Get(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o.TenantID != tenantID {
		return nil, ErrNotFound // 跨租户探测按不存在处理
	}
	if s.ensureGranted(ctx, o) {
		o.Granted = true
	}
	return o, nil
}

// List 租户订单历史。
func (s *Service) List(ctx context.Context, tenantID string, limit int) ([]*Order, error) {
	return s.store.List(ctx, tenantID, limit)
}

// HandleCallback 处理渠道回调，返回渠道要求的应答报文。
// 防线顺序：验签 → 语义解析 → 订单存在性 → 金额核验 → 状态跃迁 → 幂等发放。
func (s *Service) HandleCallback(ctx context.Context, channel string, in *CallbackInput) (status int, contentType, body string, err error) {
	p, err := s.provider(ctx, channel)
	if err != nil {
		return 0, "", "", err
	}

	res, err := p.VerifyCallback(ctx, in)
	if err != nil {
		// 防线 1：验签失败不入账、不 ACK —— 渠道会重试，运营侧能从日志看到攻击面
		s.log.Warn("payment: 回调验签失败", slog.String("channel", channel), slog.String("err", err.Error()))
		return 0, "", "", ErrBadSignature
	}
	if res == nil || !res.Success {
		// 中间态/失败通知：按渠道语义 ACK，不做任何业务动作
		st, ct, body := p.Ack()
		return st, ct, body, nil
	}

	o, err := s.store.Get(ctx, res.OrderID)
	if err != nil {
		// 未知订单号：ACK 掉（避免渠道无限重试），但留痕告警 —— 可能是伪造或环境错配
		s.log.Warn("payment: 回调命中未知订单", slog.String("channel", channel), slog.String("order_id", res.OrderID))
		st, ct, body := p.Ack()
		return st, ct, body, nil
	}

	// 防线 2：金额核验 —— 渠道实付必须与服务端目录价分毫不差
	if res.PaidCents != o.AmountCents {
		s.log.Error("payment: 回调金额与订单不符，拒绝入账",
			slog.String("order_id", o.ID),
			slog.Int("paid", res.PaidCents), slog.Int("want", o.AmountCents))
		return 0, "", "", ErrAmountMismatch
	}

	if err := s.settle(ctx, p, o, res.ProviderTxnID, res.PaidCents); err != nil {
		return 0, "", "", err
	}
	st, ct, body := p.Ack()
	return st, ct, body, nil
}

// Reconcile 主动查单对账：回调丢失（付款了没到账）时由前端轮询触发，
// 渠道侧已支付的订单走与回调完全相同的核验入账路径。
func (s *Service) Reconcile(ctx context.Context, tenantID, orderID string) (*Order, error) {
	o, err := s.Get(ctx, tenantID, orderID) // Get 已做租户隔离 + 发放自愈
	if err != nil {
		return nil, err
	}
	if o.State != StatePending {
		return o, nil
	}

	p, perr := s.provider(ctx, o.Channel)
	if perr != nil {
		return o, nil
	}
	if !p.Configured() {
		return o, nil
	}
	// 银联查单必须携带下单时的 txnTime（随单落库）；其他渠道忽略该值
	qctx := context.WithValue(ctx, txnTimeKey{}, o.TxnTime)
	q, err := p.QueryOrder(qctx, o.ID)
	if err != nil {
		// 查单失败不影响订单本身：下次轮询再试
		s.log.Warn("payment: 查单失败", slog.String("order_id", o.ID), slog.String("err", err.Error()))
		return o, nil
	}

	switch q.TradeState {
	case StatePaid:
		if q.PaidCents != o.AmountCents {
			s.log.Error("payment: 查单金额与订单不符，拒绝入账",
				slog.String("order_id", o.ID),
				slog.Int("paid", q.PaidCents), slog.Int("want", o.AmountCents))
			return o, ErrAmountMismatch
		}
		if err := s.settle(ctx, p, o, q.ProviderTxnID, q.PaidCents); err != nil {
			return o, err
		}
		return s.store.Get(ctx, o.ID)
	case StateClosed:
		_ = s.store.MarkClosed(ctx, o.ID)
		return s.store.Get(ctx, o.ID)
	default: // pending
		return o, nil
	}
}

// settle 核销订单：pending→paid 原子跃迁 + 幂等发放。
// 已非 pending 的订单（重复回调/查单竞态）按状态分别处理，绝不二次发放。
func (s *Service) settle(ctx context.Context, p Provider, o *Order, providerTxnID string, paidCents int) error {
	claimed, err := s.store.ClaimPaid(ctx, o.ID, providerTxnID)
	if err != nil {
		return err
	}
	if !claimed {
		// 竞态/重复回调：重读最新状态决定动作
		latest, gerr := s.store.Get(ctx, o.ID)
		if gerr != nil {
			return gerr
		}
		switch latest.State {
		case StatePaid:
			return nil // 已核销（本次发放前的重复通知）—— 幂等
		case StateClosed:
			// 渠道关闭后仍收到成功回调：钱已扣、单已关 —— 人工介入
			s.log.Error("payment: 已关闭订单收到成功回调，标记待退款",
				slog.String("order_id", o.ID))
			return s.store.MarkRefundNeeded(ctx, o.ID)
		case StateRefundNeeded:
			return nil // 已在人工流程中
		case StatePending:
			// 跃迁失败但订单仍是 pending：渠道流水号已被其他订单占用
			// （伪造回调/渠道异常）—— 拒绝且不 ACK
			s.log.Error("payment: 渠道流水号冲突，拒绝核销",
				slog.String("order_id", o.ID), slog.String("txn", providerTxnID))
			return fmt.Errorf("payment: 渠道流水号冲突")
		}
		return nil
	}

	// 发放（幂等）：额度入账 + 套餐标记
	if err := s.grant(ctx, o); err != nil {
		// 发放失败不回滚 paid 状态：钱已真实到账，ensureGranted 会在
		// 后续查询中补发放（credit 层按 order_id 幂等，重复调用无害）
		s.log.Error("payment: 额度发放失败（待自愈重试）",
			slog.String("order_id", o.ID), slog.String("err", err.Error()))
		return nil
	}
	s.log.Info("payment: 订单核销完成",
		slog.String("order_id", o.ID), slog.String("channel", p.Channel()),
		slog.Int("cents", paidCents), slog.Int("credits", o.Credits))
	return nil
}

// grant 发放额度并标记。两次写都幂等（credit 唯一索引 + granted 标志）。
func (s *Service) grant(ctx context.Context, o *Order) error {
	if o.Granted {
		return nil
	}
	if err := s.credits.GrantPurchase(ctx, o.TenantID, o.ID, o.Credits); err != nil {
		return err
	}
	if o.Kind == "plan" {
		if err := s.credits.SetPlanCode(ctx, o.TenantID, o.SKUCode); err != nil {
			return err
		}
	}
	return s.store.MarkGranted(ctx, o.ID)
}

// ensureGranted 补发放（崩溃窗口自愈）：paid 且未 granted 的订单在
// 每次查询时补齐。grant 全程幂等。返回本次是否发生了（或已是）发放完成。
func (s *Service) ensureGranted(ctx context.Context, o *Order) bool {
	if o.State != StatePaid || o.Granted {
		return false
	}
	if err := s.grant(ctx, o); err != nil {
		s.log.Error("payment: 自愈发放失败", slog.String("order_id", o.ID), slog.String("err", err.Error()))
		return false
	}
	return true
}
