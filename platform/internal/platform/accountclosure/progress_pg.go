package accountclosure

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func scopeMatches(ctx context.Context, tx pgx.Tx, uid, cid string) error {
	var changed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_closure_tenants c WHERE closure_id=$1 AND (sole_member IS DISTINCT FROM ((SELECT count(*) FROM tenant_members WHERE tenant_id=c.tenant_id)=1) OR NOT EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id=c.tenant_id AND user_id=$2))) OR EXISTS(SELECT 1 FROM tenant_members m WHERE user_id=$2 AND NOT EXISTS(SELECT 1 FROM account_closure_tenants c WHERE c.closure_id=$1 AND c.tenant_id=m.tenant_id))`, cid, uid).Scan(&changed)
	if err != nil {
		return err
	}
	if changed {
		return pkgerrors.ErrConflict
	}
	return nil
}
func (s *PGStore) redactAuditPage(ctx context.Context, uid, cid string, limit int) (bool, error) {
	tx, err := begin(ctx, s.pool)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&status); err != nil {
		return false, err
	}
	if status != "closure_pending" {
		return false, pkgerrors.ErrConflict
	}
	if err = scopeMatches(ctx, tx, uid, cid); err != nil {
		return false, err
	}
	var cursor int64
	if err = tx.QueryRow(ctx, `SELECT audit_cursor FROM account_closures WHERE id=$1 AND state='finalizing' FOR UPDATE`, cid).Scan(&cursor); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT id,details_json FROM audit_logs WHERE id>$3 AND (actor_id=$1 OR resource=$1 OR details_json->>'target_id'=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member)) ORDER BY id LIMIT $4 FOR UPDATE`, uid, cid, cursor, limit)
	if err != nil {
		return false, err
	}
	type entry struct {
		id   int64
		data []byte
	}
	batch := make([]entry, 0, limit)
	for rows.Next() {
		var a entry
		if err = rows.Scan(&a.id, &a.data); err != nil {
			rows.Close()
			return false, err
		}
		batch = append(batch, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, a := range batch {
		var original any
		if len(a.data) > 0 {
			if err = json.Unmarshal(a.data, &original); err != nil {
				return false, err
			}
		}
		data, _ := json.Marshal(ScrubAudit(original))
		if _, err = tx.Exec(ctx, `UPDATE audit_logs SET details_json=$2::jsonb,resource=CASE WHEN resource IN ('user','tenant','member','analysis','order') OR EXISTS(SELECT 1 FROM users WHERE id=audit_logs.resource) OR EXISTS(SELECT 1 FROM tenants WHERE id=audit_logs.resource) OR EXISTS(SELECT 1 FROM analyses WHERE id=audit_logs.resource) OR EXISTS(SELECT 1 FROM orders WHERE id=audit_logs.resource) OR EXISTS(SELECT 1 FROM credit_transactions WHERE id=audit_logs.resource) THEN resource ELSE 'anonymized' END WHERE id=$1`, a.id, data); err != nil {
			return false, err
		}
		cursor = a.id
	}
	var remaining bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_logs WHERE id>$3 AND (actor_id=$1 OR resource=$1 OR details_json->>'target_id'=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member)))`, uid, cid, cursor).Scan(&remaining); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_closures SET audit_cursor=$2,audit_processed=audit_processed+$3,cleanup_status='anonymizing',next_attempt_at=now()+interval '20 seconds' WHERE id=$1`, cid, cursor, len(batch)); err != nil {
		return false, err
	}
	return !remaining, tx.Commit(ctx)
}
func (s *PGStore) Due(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 25 {
		return nil, pkgerrors.ErrBadRequest
	}
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM account_closures WHERE (state='pending' AND withdraw_until<=now() AND (last_attempt_at IS NULL OR next_attempt_at<=now())) OR (state IN ('finalizing','completed') AND cleanup_status<>'complete' AND (next_attempt_at IS NULL OR next_attempt_at<=now())) ORDER BY COALESCE(next_attempt_at,withdraw_until),id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var uid string
		if err = rows.Scan(&uid); err != nil {
			return nil, err
		}
		ids = append(ids, uid)
	}
	return ids, rows.Err()
}
