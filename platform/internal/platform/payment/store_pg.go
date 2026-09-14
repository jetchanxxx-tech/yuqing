package payment

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore 是 PostgreSQL 订单存储（0006 迁移的 orders 表）。
// ClaimPaid 的条件 UPDATE 是防重复入账的原子基石；provider_txn_id 的
// 部分唯一索引在数据库层拦截「一个渠道流水号喂多个订单」的刷单。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore 装配 PostgreSQL 存储。
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (p *PGStore) Create(ctx context.Context, o *Order) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO orders
		   (id, tenant_id, sku_code, kind, credits, amount_cents, channel,
		    state, provider_txn_id, qr_code_url, granted, expires_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13)`,
		o.ID, o.TenantID, o.SKUCode, o.Kind, o.Credits, o.AmountCents, o.Channel,
		o.State, o.ProviderTxnID, o.QRCodeURL, o.Granted, o.ExpiresAt, o.CreatedAt)
	return err
}

func (p *PGStore) Get(ctx context.Context, orderID string) (*Order, error) {
	o, err := scanOrder(p.pool.QueryRow(ctx, orderSelect+" WHERE id = $1", orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (p *PGStore) List(ctx context.Context, tenantID string, limit int) ([]*Order, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx,
		orderSelect+" WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2",
		tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *PGStore) ClaimPaid(ctx context.Context, orderID, providerTxnID string) (bool, error) {
	// 条件原子跃迁：仅 pending 可 paid；渠道流水号唯一（空串视为无流水号）。
	tag, err := p.pool.Exec(ctx,
		`UPDATE orders SET state = $2, provider_txn_id = NULLIF($3,''),
		        paid_at = now(), updated_at = now()
		 WHERE id = $1 AND state = $4
		   AND ($3 = '' OR NOT EXISTS (
		        SELECT 1 FROM orders o2 WHERE o2.provider_txn_id = $3 AND o2.id <> $1))`,
		orderID, StatePaid, providerTxnID, StatePending)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (p *PGStore) MarkClosed(ctx context.Context, orderID string) error {
	return p.markState(ctx, orderID, StateClosed)
}

func (p *PGStore) MarkRefundNeeded(ctx context.Context, orderID string) error {
	return p.markState(ctx, orderID, StateRefundNeeded)
}

func (p *PGStore) MarkGranted(ctx context.Context, orderID string) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE orders SET granted = TRUE, updated_at = now() WHERE id = $1`, orderID)
	return err
}

func (p *PGStore) SaveChannelMeta(ctx context.Context, orderID, providerTxnID, txnTime string) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE orders SET provider_txn_id = NULLIF($2,''), txn_time = $3, updated_at = now()
		 WHERE id = $1`, orderID, providerTxnID, txnTime)
	return err
}

func (p *PGStore) markState(ctx context.Context, orderID, to string) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE orders SET state = $2, updated_at = now() WHERE id = $1`, orderID, to)
	return err
}

const orderSelect = `SELECT id, tenant_id, sku_code, kind, credits, amount_cents,
	channel, state, COALESCE(provider_txn_id,''), COALESCE(qr_code_url,''),
	granted, paid_at, expires_at, created_at FROM orders`

type rowScanner interface{ Scan(dest ...any) error }

func scanOrder(row rowScanner) (*Order, error) {
	var o Order
	var paidAt *time.Time
	if err := row.Scan(&o.ID, &o.TenantID, &o.SKUCode, &o.Kind, &o.Credits,
		&o.AmountCents, &o.Channel, &o.State, &o.ProviderTxnID, &o.QRCodeURL,
		&o.Granted, &paidAt, &o.ExpiresAt, &o.CreatedAt); err != nil {
		return nil, err
	}
	o.PaidAt = paidAt
	return &o, nil
}

// 编译期契约检查。
var _ Store = (*PGStore)(nil)
