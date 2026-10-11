package credit

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Reservation struct{ TenantID, AnalysisID, RunID, ActorUserID, ActorAPIKeyID, PlanCode string }

func (s *Service) UsesPool(pool *pgxpool.Pool) bool {
	store, ok := s.store.(*PGStore)
	return ok && store.pool == pool
}

// TryConsumeTx is deliberately transaction-only: callers own rollback of the
// analysis, run, ledger and queue together. The same-pool guard is in admission.
func (s *Service) TryConsumeTx(ctx context.Context, tx pgx.Tx, r Reservation) (*Transaction, error) {
	if _, ok := s.store.(*PGStore); !ok || r.RunID == "" || r.ActorUserID == "" {
		return nil, fmt.Errorf("credit: transaction reservation requires a PG run and actor")
	}
	var balance int
	err := tx.QueryRow(ctx, `UPDATE report_credits SET balance=balance-1,updated_at=now() WHERE tenant_id=$1 AND balance>=1 RETURNING balance`, r.TenantID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInsufficientCredits
	}
	if err != nil {
		return nil, err
	}
	record := &Transaction{ID: newTxID(), TenantID: r.TenantID, AnalysisID: r.AnalysisID, Reason: ReasonUse, Delta: -1, BalanceAfter: balance, CreatedAt: time.Now().UTC()}
	_, err = tx.Exec(ctx, `INSERT INTO credit_transactions(id,tenant_id,analysis_id,run_id,actor_user_id,actor_api_key_id,plan_code_snapshot,delta,reason,balance_after,created_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,-1,'consume',$8,$9)`, record.ID, r.TenantID, r.AnalysisID, r.RunID, r.ActorUserID, r.ActorAPIKeyID, r.PlanCode, balance, record.CreatedAt)
	return record, err
}

func (s *Service) RefundRunTx(ctx context.Context, tx pgx.Tx, tenantID, runID string) (bool, error) {
	return RefundRunTx(ctx, tx, tenantID, runID)
}

// RefundRunTx settles accepted debt even if the original actor is now disabled.
// It never searches an analysis's older consumption history.
func RefundRunTx(ctx context.Context, tx pgx.Tx, tenantID, runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	var consumeID, analysisID, actorID, keyID, planCode string
	err := tx.QueryRow(ctx, `SELECT COALESCE(consume_tx_id,''),analysis_id,COALESCE(actor_user_id,''),COALESCE(actor_api_key_id,''),plan_code FROM analysis_runs WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, runID, tenantID).Scan(&consumeID, &analysisID, &actorID, &keyID, &planCode)
	if err != nil {
		return false, err
	}
	if consumeID == "" {
		return false, nil
	}
	var refunded bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credit_transactions WHERE reason='refund' AND consume_tx_id=$1)`, consumeID).Scan(&refunded); err != nil {
		return false, err
	}
	if refunded {
		return false, nil
	}
	var balance int
	if err := tx.QueryRow(ctx, `UPDATE report_credits SET balance=balance+1,updated_at=now() WHERE tenant_id=$1 RETURNING balance`, tenantID).Scan(&balance); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO credit_transactions(id,tenant_id,analysis_id,run_id,consume_tx_id,delta,reason,balance_after,created_at,actor_user_id,actor_api_key_id,plan_code_snapshot) VALUES($1,$2,$3,$4,$5,1,'refund',$6,now(),$7,NULLIF($8,''),$9)`, newTxID(), tenantID, analysisID, runID, consumeID, balance, actorID, keyID, planCode)
	return err == nil, err
}
