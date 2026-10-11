package credit

import (
	"context"
	"encoding/json"
	"errors"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/auth"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore 是 PostgreSQL 额度存储。原子性由「INSERT 兜底 + 条件 UPDATE」
// 两步在同事务内完成；幂等由 0006 迁移的部分唯一索引兜底
// （purchase 按 order_id、refund 按 consume_tx_id），并发进程下同样成立。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore 装配 PostgreSQL 存储。
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (p *PGStore) Balance(ctx context.Context, tenantID string) (int, error) {
	var bal int
	err := p.pool.QueryRow(ctx,
		`SELECT balance FROM report_credits WHERE tenant_id = $1`, tenantID).Scan(&bal)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return bal, err
}

func (p *PGStore) Snapshot(ctx context.Context, tenantID string) (*Snapshot, error) {
	s := &Snapshot{}
	err := p.pool.QueryRow(ctx, `SELECT balance,plan_code,version FROM report_credits WHERE tenant_id=$1`, tenantID).Scan(&s.Balance, &s.PlanCode, &s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (p *PGStore) ApplyDelta(ctx context.Context, tenantID string, delta int, tx Transaction) (int, error) {
	db, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Rollback(ctx) }()

	// 兜底行：无记录租户从 0 起步（ON CONFLICT DO NOTHING 保证幂等）
	if _, err := db.Exec(ctx,
		`INSERT INTO report_credits (tenant_id, balance) VALUES ($1, 0)
		 ON CONFLICT (tenant_id) DO NOTHING`, tenantID); err != nil {
		return 0, err
	}

	// 条件原子更新：扣减时余额不足则 0 行受影响 → ErrInsufficientCredits
	var bal int
	var version int64
	err = db.QueryRow(ctx,
		`UPDATE report_credits SET balance = balance + $2, version = version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND ($2 >= 0 OR balance + $2 >= 0)
		 RETURNING balance,version`, tenantID, delta).Scan(&bal, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInsufficientCredits
	}
	if err != nil {
		return 0, err
	}

	_, err = db.Exec(ctx,
		`INSERT INTO credit_transactions
		   (id, tenant_id, delta, reason, analysis_id, order_id, consume_tx_id, reason_detail, actor_id, idempotency_key, balance_after, version, created_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5,''), NULLIF($6,''), NULLIF($7,''), $8, NULLIF($9,''), NULLIF($10,''), $11, $12, $13)`,
		newTxID(), tenantID, delta, tx.Reason,
		tx.AnalysisID, tx.OrderID, tx.ConsumeTxID, tx.ReasonDetail, tx.ActorID, tx.IdempotencyKey, bal, version, time.Now().UTC())
	if isUniqueViolation(err) {
		// 幂等命中：同订单重复入账 / 同消费重复回补 —— 回滚余额变更，
		// 以内部信号告知 Service 层这是良性重放。
		return 0, errAlreadyRefunded
	}
	if err != nil {
		return 0, err
	}

	if err := db.Commit(ctx); err != nil {
		return 0, err
	}
	return bal, nil
}

func (p *PGStore) FindUnrefundedConsume(ctx context.Context, tenantID, analysisID string) (*Transaction, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT t.id, t.delta, t.created_at
		 FROM credit_transactions t
		 WHERE t.tenant_id = $1 AND t.reason = $2 AND t.analysis_id = $3
		   AND NOT EXISTS (SELECT 1 FROM credit_transactions r
		                   WHERE r.reason = $4 AND r.consume_tx_id = t.id)
		 ORDER BY t.created_at DESC, t.id DESC
		 LIMIT 1`,
		tenantID, ReasonUse, analysisID, ReasonRefund)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	var tx Transaction
	if err := rows.Scan(&tx.ID, &tx.Delta, &tx.CreatedAt); err != nil {
		return nil, err
	}
	return &tx, nil
}

func (p *PGStore) SetPlanCode(ctx context.Context, tenantID, planCode string) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO report_credits (tenant_id, balance, plan_code)
		 VALUES ($1, 0, $2)
		 ON CONFLICT (tenant_id) DO UPDATE SET plan_code = $2, updated_at = now()`,
		tenantID, planCode)
	return err
}

func (p *PGStore) PlanCode(ctx context.Context, tenantID string) (string, error) {
	var code string
	err := p.pool.QueryRow(ctx,
		`SELECT plan_code FROM report_credits WHERE tenant_id = $1`, tenantID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return code, err
}

func (p *PGStore) Transactions(ctx context.Context, tenantID string, limit int) ([]Transaction, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx,
		`SELECT id, delta, reason, COALESCE(analysis_id,''), COALESCE(order_id,''),
		        COALESCE(consume_tx_id,''), balance_after, created_at,
		        COALESCE(reason_detail,''), COALESCE(actor_id,''), COALESCE(idempotency_key,''), version
		 FROM credit_transactions
		 WHERE tenant_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transaction
	for rows.Next() {
		var tx Transaction
		if err := rows.Scan(&tx.ID, &tx.Delta, &tx.Reason, &tx.AnalysisID,
			&tx.OrderID, &tx.ConsumeTxID, &tx.BalanceAfter, &tx.CreatedAt, &tx.ReasonDetail, &tx.ActorID, &tx.IdempotencyKey, &tx.Version); err != nil {
			return nil, err
		}
		tx.TenantID = tenantID
		out = append(out, tx)
	}
	return out, rows.Err()
}

// Adjust revalidates the original actor and serializes administrative intent
// before locking the shared credit pool. Balance, intent ledger and audit have
// one commit; the key lookup precedes CAS so a retry returns its original row.
func (p *PGStore) Adjust(ctx context.Context, a Adjustment) (*Transaction, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741915))`, a.TenantID); err != nil {
		return nil, err
	}
	var status string
	var version int64
	var admin bool
	err = tx.QueryRow(ctx, `SELECT u.status,u.token_version,EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role='platform_admin') FROM users u WHERE u.id=$1 FOR SHARE OF u`, a.ActorID).Scan(&status, &version, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if status != "active" || version != a.ActorTokenVersion || !admin {
		return nil, pkgerrors.ErrConflict
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1 FOR SHARE`, a.TenantID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var existing Transaction
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,delta,reason,reason_detail,actor_id,idempotency_key,balance_after,version,COALESCE(expected_version,-1),created_at FROM credit_transactions WHERE tenant_id=$1 AND idempotency_key=$2`, a.TenantID, a.IdempotencyKey).Scan(&existing.ID, &existing.TenantID, &existing.Delta, &existing.Reason, &existing.ReasonDetail, &existing.ActorID, &existing.IdempotencyKey, &existing.BalanceAfter, &existing.Version, &existing.ExpectedVersion, &existing.CreatedAt)
	if err == nil {
		if existing.Reason != ReasonAdjust || existing.Delta != a.Delta || existing.ReasonDetail != a.ReasonDetail || existing.ActorID != a.ActorID || existing.ExpectedVersion != a.ExpectedVersion {
			return nil, pkgerrors.ErrConflict
		}
		return &existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_credits(tenant_id,balance) VALUES($1,0) ON CONFLICT(tenant_id) DO NOTHING`, a.TenantID); err != nil {
		return nil, err
	}
	var balance int
	if err = tx.QueryRow(ctx, `SELECT version,balance FROM report_credits WHERE tenant_id=$1 FOR UPDATE`, a.TenantID).Scan(&version, &balance); err != nil {
		return nil, err
	}
	if version != a.ExpectedVersion {
		return nil, pkgerrors.ErrConflict
	}
	if int64(balance)+int64(a.Delta) < 0 {
		return nil, ErrInsufficientCredits
	}
	if int64(balance)+int64(a.Delta) > 2147483647 {
		return nil, pkgerrors.ErrConflict
	}
	before := balance
	if err = tx.QueryRow(ctx, `UPDATE report_credits SET balance=balance+$2,updated_at=now() WHERE tenant_id=$1 RETURNING balance,version`, a.TenantID, a.Delta).Scan(&balance, &version); err != nil {
		return nil, err
	}
	result := &Transaction{ID: newTxID(), TenantID: a.TenantID, Delta: a.Delta, Reason: ReasonAdjust, ReasonDetail: a.ReasonDetail, ActorID: a.ActorID, IdempotencyKey: a.IdempotencyKey, BalanceAfter: balance, Version: version, ExpectedVersion: a.ExpectedVersion}
	if err = tx.QueryRow(ctx, `INSERT INTO credit_transactions(id,tenant_id,delta,reason,reason_detail,actor_id,idempotency_key,balance_after,version,expected_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING created_at`, result.ID, a.TenantID, a.Delta, ReasonAdjust, a.ReasonDetail, a.ActorID, a.IdempotencyKey, balance, version, a.ExpectedVersion).Scan(&result.CreatedAt); err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{"target_type": "tenant", "target_id": a.TenantID, "reason": a.ReasonDetail, "request_id": a.RequestID, "idempotency_key": a.IdempotencyKey, "ledger_id": result.ID, "before": map[string]any{"balance": before, "version": a.ExpectedVersion}, "after": map[string]any{"balance": balance, "version": version}})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_id,action,resource,details_json) VALUES($1,$2,'credit.adjustment','tenant',$3::jsonb)`, a.TenantID, a.ActorID, details); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// isUniqueViolation 判断 pgx 错误是否为唯一约束冲突（SQLSTATE 23505）。
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}
