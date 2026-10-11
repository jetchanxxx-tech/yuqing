package accountclosure

import (
	"context"
	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func personalRemaining(ctx context.Context, tx pgx.Tx, uid, cid string, result *bool) error {
	return tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM verification_tokens WHERE (user_id=$1 OR issuer_user_id=$1) AND (notice_state='processing' OR COALESCE(target,'')<>'' OR token_hash IS NOT NULL OR notice_target<>'')) OR
 EXISTS(SELECT 1 FROM sms_verification_codes WHERE (user_id=$1 OR issuer_user_id=$1) AND (phone<>'anonymized:'||id OR code_hash IS NOT NULL)) OR
 EXISTS(SELECT 1 FROM login_sessions WHERE user_id=$1) OR
 EXISTS(SELECT 1 FROM account_notification_events WHERE user_id=$1 AND state IN ('pending','failed','processing')) OR
 EXISTS(SELECT 1 FROM api_keys WHERE creator_user_id=$1 AND (key_hash<>'anonymized:'||id OR prefix<>'' OR name IS DISTINCT FROM '已注销账号密钥')) OR
 EXISTS(SELECT 1 FROM credit_transactions WHERE run_id IS NULL AND (actor_id=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member)) AND ((reason_detail<>'' AND reason_detail<>'已注销账号（原因脱敏）') OR (idempotency_key IS NOT NULL AND idempotency_key<>'anonymized:'||id)))`, uid, cid).Scan(result)
}

// Each retained-metadata table gets one bounded idempotent batch. Existing
// status/version fences prevent new identity writes; the short final commit
// checks again for remaining rows before releasing the original email/phone.
func (s *PGStore) redactPersonalPage(ctx context.Context, uid, cid string, limit int) (bool, error) {
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
	// The outer predicate is essential: a previously admitted worker may win
	// the row lock after the page's snapshot. PostgreSQL rechecks this predicate
	// after waiting, so neither its recipient nor receipt CAS can be erased.
	// Processing rows remain visible to personalRemaining and the final gate.
	for _, q := range []string{
		`UPDATE account_notification_events SET state='cancelled',next_attempt_at=NULL WHERE state IN ('pending','failed') AND id IN (SELECT id FROM account_notification_events WHERE user_id=$1 AND state IN ('pending','failed') ORDER BY id LIMIT $2)`,
		`UPDATE verification_tokens SET used_at=COALESCE(used_at,now()),target='',token_hash=NULL,notice_target='',notice_state=CASE WHEN notice_state='accepted' THEN 'accepted' ELSE 'cancelled' END,notice_next_attempt=NULL WHERE notice_state<>'processing' AND id IN (SELECT id FROM verification_tokens WHERE (user_id=$1 OR issuer_user_id=$1) AND notice_state<>'processing' AND (COALESCE(target,'')<>'' OR token_hash IS NOT NULL OR notice_target<>'') ORDER BY id LIMIT $2)`,
		`UPDATE sms_verification_codes SET used_at=COALESCE(used_at,now()),phone='anonymized:'||id,code_hash=NULL WHERE id IN (SELECT id FROM sms_verification_codes WHERE (user_id=$1 OR issuer_user_id=$1) AND (phone<>'anonymized:'||id OR code_hash IS NOT NULL) ORDER BY id LIMIT $2)`,
		`DELETE FROM login_sessions WHERE id IN (SELECT id FROM login_sessions WHERE user_id=$1 ORDER BY id LIMIT $2)`,
		`UPDATE api_keys SET name='已注销账号密钥',key_hash='anonymized:'||id,prefix='',revoked_at=COALESCE(revoked_at,now()) WHERE id IN (SELECT id FROM api_keys WHERE creator_user_id=$1 AND (key_hash<>'anonymized:'||id OR prefix<>'' OR name IS DISTINCT FROM '已注销账号密钥') ORDER BY id LIMIT $2)`,
	} {
		if _, err = tx.Exec(ctx, q, uid, limit); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE credit_transactions SET reason_detail=CASE WHEN reason_detail='' THEN '' ELSE '已注销账号（原因脱敏）' END,idempotency_key=CASE WHEN idempotency_key IS NULL THEN NULL ELSE 'anonymized:'||id END WHERE id IN (SELECT id FROM credit_transactions WHERE run_id IS NULL AND (actor_id=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member)) AND ((reason_detail<>'' AND reason_detail<>'已注销账号（原因脱敏）') OR (idempotency_key IS NOT NULL AND idempotency_key<>'anonymized:'||id)) ORDER BY id LIMIT $3)`, uid, cid, limit); err != nil {
		return false, err
	}
	var remaining bool
	if err = personalRemaining(ctx, tx, uid, cid, &remaining); err != nil {
		return false, err
	}
	return !remaining, tx.Commit(ctx)
}
