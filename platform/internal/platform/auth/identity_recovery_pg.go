package auth

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yuqing/platform/internal/pkg/notification"
)

func (s *PGVerificationStore) IdentityLinkState(ctx context.Context, purpose, hash, uid string) (string, error) {
	var state string
	err := s.pool.QueryRow(ctx, `SELECT CASE WHEN used_at IS NOT NULL THEN 'used' WHEN expires_at<=now() THEN 'expired' ELSE 'invalid' END FROM verification_tokens WHERE purpose=$1 AND token_hash=$2 AND ($3='' OR user_id=$3)`, purpose, hash, uid).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "invalid", nil
	}
	if err != nil {
		return "", verificationStorageError(err)
	}
	return state, nil
}
func (s *PGVerificationStore) ClaimIdentityNotice(ctx context.Context) (*IdentityNotice, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, verificationStorageError(err)
	}
	defer tx.Rollback(context.Background())
	// Share closure's platform -> sorted users -> credential boundary. Only
	// closing identities are excluded: disabled users still need security mail.
	// Locks end before the bounded supplier request; its receipt CAS remains
	// admissible even if closure was requested after this claim committed.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, PlatformAdminLockID); err != nil {
		return nil, verificationStorageError(err)
	}
	var cid, uid, issuer string
	err = tx.QueryRow(ctx, `SELECT v.id,v.user_id,COALESCE(v.issuer_user_id,v.user_id)
 FROM verification_tokens v JOIN users u ON u.id=v.user_id
 LEFT JOIN users i ON i.id=v.issuer_user_id
 WHERE v.purpose='email_change' AND v.used_at IS NOT NULL
 AND v.notice_state IN ('pending','failed','processing') AND v.notice_next_attempt<=now()
 AND u.status NOT IN ('closure_pending','closed')
 AND (v.issuer_user_id IS NULL OR i.status NOT IN ('closure_pending','closed'))
 ORDER BY v.notice_next_attempt,v.id LIMIT 1`).Scan(&cid, &uid, &issuer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, verificationStorageError(err)
	}
	rows, err := tx.Query(ctx, `SELECT status FROM users WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, uid, issuer)
	if err != nil {
		return nil, verificationStorageError(err)
	}
	allowed := true
	for rows.Next() {
		var status string
		if err = rows.Scan(&status); err != nil {
			rows.Close()
			return nil, verificationStorageError(err)
		}
		if status == "closure_pending" || status == "closed" {
			allowed = false
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, verificationStorageError(err)
	}
	if !allowed {
		return nil, nil
	}
	n := &IdentityNotice{}
	// A lease exceeds the bounded supplier request. CAS on attempt rejects late
	// completions after recovery by another process.
	err = tx.QueryRow(ctx, `UPDATE verification_tokens SET notice_state='processing',notice_attempts=notice_attempts+1,notice_next_attempt=now()+interval '2 minutes'
 WHERE id=$1 AND purpose='email_change' AND used_at IS NOT NULL
 AND notice_state IN ('pending','failed','processing') AND notice_next_attempt<=now()
 RETURNING id,notice_target,notice_attempts`, cid).Scan(&n.ID, &n.Recipient, &n.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, verificationStorageError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, verificationStorageError(err)
	}
	return n, nil
}
func (s *PGVerificationStore) CompleteIdentityNotice(ctx context.Context, n IdentityNotice, receipt notification.Receipt) error {
	r, err := notification.Normalize(receipt, "email_changed_notice")
	if err != nil {
		return verificationInvalid()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return verificationInvalid()
	}
	tag, err := s.pool.Exec(ctx, `UPDATE verification_tokens SET notice_state=CASE WHEN $3 THEN 'accepted' ELSE 'failed' END,notice_receipt=$4,notice_next_attempt=CASE WHEN $3 THEN NULL ELSE now()+interval '1 minute' END,notice_target=CASE WHEN $3 THEN '' ELSE notice_target END WHERE id=$1 AND notice_state='processing' AND notice_attempts=$2`, n.ID, n.Attempt, r.State == "accepted", data)
	if err != nil {
		return verificationStorageError(err)
	}
	if tag.RowsAffected() != 1 {
		return verificationInvalid()
	}
	return nil
}
