package payment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/billing"
)

// Payment itself is already a verified durable fact. Credit, effective plan,
// source and granted are one later transaction; failures preserve paid but
// cannot leave a partially published entitlement. Late financial settlement
// remains admissible without requiring an active account.
func (p *PGStore) grantPaid(ctx context.Context, oid string) error {
	tx, err := p.financialTx(ctx, oid)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	o, err := scanOrder(tx.QueryRow(ctx, orderSelect+` WHERE id=$1 FOR UPDATE`, oid))
	if err != nil {
		return err
	}
	if o.Granted {
		return nil
	}
	if o.State != StatePaid || o.Credits <= 0 || (o.Kind == "plan" && billing.DefaultPlans()[o.SKUCode] == nil) {
		return pkgerrors.ErrConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_credits(tenant_id,balance) VALUES($1,0) ON CONFLICT DO NOTHING`, o.TenantID); err != nil {
		return err
	}
	var balance int
	var version int64
	if err = tx.QueryRow(ctx, `SELECT balance,version FROM report_credits WHERE tenant_id=$1 FOR UPDATE`, o.TenantID).Scan(&balance, &version); err != nil {
		return err
	}
	var ledgerTenant string
	var delta int
	var ledgerVersion int64
	err = tx.QueryRow(ctx, `SELECT tenant_id,delta,version FROM credit_transactions WHERE order_id=$1 AND reason='purchase'`, o.ID).Scan(&ledgerTenant, &delta, &ledgerVersion)
	newGrant := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !newGrant {
		return err
	}
	if !newGrant && (ledgerTenant != o.TenantID || delta != o.Credits) {
		return pkgerrors.ErrConflict
	}
	if newGrant {
		if err = tx.QueryRow(ctx, `UPDATE report_credits SET balance=balance+$2,updated_at=now() WHERE tenant_id=$1 RETURNING balance,version`, o.TenantID, o.Credits).Scan(&balance, &version); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO credit_transactions(id,tenant_id,delta,reason,order_id,balance_after,version) VALUES($1,$2,$3,'purchase',$4,$5,$6)`, id.New(), o.TenantID, o.Credits, o.ID, balance, version); err != nil {
			return err
		}
	}
	// An old partial grant can only finish its plan step if the positive ledger
	// version proves no later pool mutation. Pre-version history is ambiguous;
	// preserve its current plan/source instead of guessing historical ordering.
	if o.Kind == "plan" && (newGrant || ledgerVersion > 0 && version == ledgerVersion) {
		if _, err = tx.Exec(ctx, `UPDATE report_credits SET plan_code=$2,plan_source='purchase',updated_at=now() WHERE tenant_id=$1`, o.TenantID, o.SKUCode); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET granted=true,updated_at=now() WHERE id=$1 AND state='paid'`, o.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
