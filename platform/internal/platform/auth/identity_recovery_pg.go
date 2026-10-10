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
	n := &IdentityNotice{}
	// A lease exceeds the bounded supplier request. CAS on attempt rejects late
	// completions after recovery by another process.
	err := s.pool.QueryRow(ctx, `WITH due AS (
 SELECT id FROM verification_tokens WHERE purpose='email_change' AND used_at IS NOT NULL
 AND notice_state IN ('pending','failed','processing') AND notice_next_attempt<=now()
 ORDER BY notice_next_attempt,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE verification_tokens v SET notice_state='processing',notice_attempts=notice_attempts+1,notice_next_attempt=now()+interval '2 minutes'
 FROM due WHERE v.id=due.id RETURNING v.id,v.notice_target,v.notice_attempts`).Scan(&n.ID, &n.Recipient, &n.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
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
