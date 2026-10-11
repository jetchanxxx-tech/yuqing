package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PGVerificationStore) ReserveCurrentPassword(ctx context.Context, uid, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return verificationStorageError(err)
	}
	defer tx.Rollback(context.Background())
	// Every two-key reservation locks source before account; confirmation/closure
	// use only the same source row. Rollback never partially spends either budget.
	for _, key := range []string{"confirm:ip:" + ip, "confirm:uid:" + uid} {
		var count int
		err = tx.QueryRow(ctx, `INSERT INTO verification_send_gates(gate_key,window_start,last_sent,sends)
 VALUES($1,now(),now(),1) ON CONFLICT(gate_key) DO UPDATE SET
 window_start=CASE WHEN verification_send_gates.window_start<=now()-interval '1 minute' THEN now() ELSE verification_send_gates.window_start END,
 last_sent=now(),sends=CASE WHEN verification_send_gates.window_start<=now()-interval '1 minute' THEN 1 ELSE verification_send_gates.sends+1 END
 WHERE verification_send_gates.window_start<=now()-interval '1 minute' OR verification_send_gates.sends<$2
 RETURNING sends`, key, passwordConfirmationLimit).Scan(&count)
		if errors.Is(err, pgx.ErrNoRows) {
			var seconds int
			if err = tx.QueryRow(ctx, `SELECT GREATEST(1,LEAST(60,ceil(extract(epoch FROM window_start+interval '1 minute'-now()))))::int FROM verification_send_gates WHERE gate_key=$1`, key).Scan(&seconds); err != nil {
				return verificationStorageError(err)
			}
			return passwordConfirmationLimited{after: seconds}
		}
		if err != nil {
			return verificationStorageError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return verificationStorageError(err)
	}
	return nil
}
