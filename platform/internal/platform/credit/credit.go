// Package credit 实现报告额度池：消费（TryConsume）、入账（试用/购买/补偿）、
// 失败回补（RefundByAnalysis）。资金安全三原则：
//
//  1. 并发不超卖 —— 扣减是存储层原子操作（内存互斥 / PG 单语句 UPDATE），
//     余额 5 时 20 个并发扣减恰好成功 5 个（有 -race 测试锁定）。
//  2. 入账幂等 —— 购买入账按 order_id 唯一（回调重放/查单补偿并发只入一次），
//     回补按 consume_tx_id 唯一（一笔消费至多退一次）。
//  3. Rerun 可重复消费 —— 每一轮运行是独立的 consume 流水，回补与之配对。
package credit

import (
	"context"
	"errors"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// 流水原因。
const (
	ReasonTrial  = "trial" // 注册赠送
	ReasonGrant  = "grant" // 运营赠送 / 客服补偿
	ReasonBuy    = "purchase"
	ReasonUse    = "consume"
	ReasonRefund = "refund"
)

// TrialCredits 是新租户注册赠送的报告额度（方案 B：试用归 Lite 档体验）。
const TrialCredits = 1

// ErrInsufficientCredits 与 API 错误码映射：额度不足 → 402 NO_CREDITS。
var ErrInsufficientCredits = pkgerrors.ErrNoCredits

// Transaction 是一笔额度流水。
type Transaction struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Delta        int       `json:"delta"`
	Reason       string    `json:"reason"`
	AnalysisID   string    `json:"analysis_id,omitempty"`
	OrderID      string    `json:"order_id,omitempty"`
	ConsumeTxID  string    `json:"consume_tx_id,omitempty"`
	BalanceAfter int       `json:"balance_after"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store 是额度持久化契约：内存（测试/开发）与 PostgreSQL（生产）双实现。
// ApplyDelta 必须原子，且实现入账幂等（见包注释）。
type Store interface {
	Balance(ctx context.Context, tenantID string) (int, error)
	// ApplyDelta 调整余额并落流水，返回调整后余额。幂等键：reason=purchase
	// 时按 OrderID、reason=refund 时按 ConsumeTxID 去重 —— 命中则原样返回
	// 当前余额（no-op，nil error）。
	ApplyDelta(ctx context.Context, tenantID string, delta int, tx Transaction) (int, error)
	// FindUnrefundedConsume 返回该分析最近一笔尚未被回补的消费流水；没有则 nil。
	FindUnrefundedConsume(ctx context.Context, tenantID, analysisID string) (*Transaction, error)
	SetPlanCode(ctx context.Context, tenantID, planCode string) error
	PlanCode(ctx context.Context, tenantID string) (string, error)
	Transactions(ctx context.Context, tenantID string, limit int) ([]Transaction, error)
}

// Service 是额度业务入口。
type Service struct {
	store Store
}

// NewService 装配额度服务。
func NewService(store Store) *Service {
	return &Service{store: store}
}

// TryConsume 消费 1 次报告额度。余额不足返回 ErrInsufficientCredits
// （API 层映射 402 NO_CREDITS）。
func (s *Service) TryConsume(ctx context.Context, tenantID, analysisID string) error {
	_, err := s.store.ApplyDelta(ctx, tenantID, -1, Transaction{
		Reason: ReasonUse, AnalysisID: analysisID,
	})
	return err
}

// GrantTrial 发放注册试用额度（1 次）。
func (s *Service) GrantTrial(ctx context.Context, tenantID string, credits int) error {
	_, err := s.store.ApplyDelta(ctx, tenantID, credits, Transaction{Reason: ReasonTrial})
	return err
}

// GrantPurchase 购买入账（回调核销/查单补偿都会调，幂等）。
func (s *Service) GrantPurchase(ctx context.Context, tenantID, orderID string, credits int) error {
	_, err := s.store.ApplyDelta(ctx, tenantID, credits, Transaction{
		Reason: ReasonBuy, OrderID: orderID,
	})
	if errors.Is(err, errAlreadyRefunded) {
		return nil // 同一订单重复入账（回调重放/补偿并发）—— 幂等成功
	}
	return err
}

// AdminAdjust 运营手工调整（正负皆可）。
func (s *Service) AdminAdjust(ctx context.Context, tenantID string, delta int, note string) error {
	_, err := s.store.ApplyDelta(ctx, tenantID, delta, Transaction{Reason: ReasonGrant})
	_ = note // 备注暂不落库（流水 reason 已区分），P1 扩展 transactions.note 列
	return err
}

// RefundByAnalysis 回补分析失败消耗的额度（管线 markFailed 调用）。
// 返回是否真的发生了回补；无可退消费或已退过 → (false, nil)。
func (s *Service) RefundByAnalysis(ctx context.Context, tenantID, analysisID string) (bool, error) {
	consumed, err := s.store.FindUnrefundedConsume(ctx, tenantID, analysisID)
	if err != nil {
		return false, err
	}
	if consumed == nil {
		return false, nil
	}
	_, err = s.store.ApplyDelta(ctx, tenantID, -consumed.Delta, Transaction{
		Reason:      ReasonRefund,
		AnalysisID:  analysisID,
		ConsumeTxID: consumed.ID,
	})
	if err != nil {
		// 并发重复回补命中幂等键（PG 23505 / 内存去重）视为已退过
		if errors.Is(err, errAlreadyRefunded) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Balance 查询当前余额；无记录租户为 0。
func (s *Service) Balance(ctx context.Context, tenantID string) (int, error) {
	return s.store.Balance(ctx, tenantID)
}

// SetPlanCode / PlanCode 维护租户当前套餐标记（购买生效/展示）。
func (s *Service) SetPlanCode(ctx context.Context, tenantID, planCode string) error {
	return s.store.SetPlanCode(ctx, tenantID, planCode)
}

func (s *Service) PlanCode(ctx context.Context, tenantID string) (string, error) {
	return s.store.PlanCode(ctx, tenantID)
}

// Transactions 最近流水（新→旧）。
func (s *Service) Transactions(ctx context.Context, tenantID string, limit int) ([]Transaction, error) {
	return s.store.Transactions(ctx, tenantID, limit)
}

// newTxID 生成流水 ID（ULID）。
func newTxID() string { return id.New() }
