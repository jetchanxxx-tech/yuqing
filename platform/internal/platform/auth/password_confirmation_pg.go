package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// Confirmation keys are independent of all issuance purpose/target/IP keys.
// The existing durable gate table supplies row-serialized bounded admission;
// the counter survives process reconstruction and is shared by all purposes.
func (s *PGVerificationStore) ReservePasswordConfirmation(ctx context.Context, ip string) error {
	key := "confirm:ip:" + ip
	var count int
	err := s.pool.QueryRow(ctx, `INSERT INTO verification_send_gates(gate_key,window_start,last_sent,sends)
 VALUES($1,now(),now(),1) ON CONFLICT(gate_key) DO UPDATE SET
 window_start=CASE WHEN verification_send_gates.window_start<=now()-interval '1 minute' THEN now() ELSE verification_send_gates.window_start END,
 last_sent=now(),sends=CASE WHEN verification_send_gates.window_start<=now()-interval '1 minute' THEN 1 ELSE verification_send_gates.sends+1 END
 WHERE verification_send_gates.window_start<=now()-interval '1 minute' OR verification_send_gates.sends<$2
 RETURNING sends`, key, passwordConfirmationLimit).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		var seconds int
		if err = s.pool.QueryRow(ctx, `SELECT GREATEST(1,LEAST(60,ceil(extract(epoch FROM window_start+interval '1 minute'-now()))))::int FROM verification_send_gates WHERE gate_key=$1`, key).Scan(&seconds); err != nil {
			return pkgerrors.ErrServiceUnavailable
		}
		return passwordConfirmationLimited{after: seconds}
	}
	if err != nil {
		return verificationStorageError(err)
	}
	return nil
}
func (s *PGVerificationStore) PreflightPassword(ctx context.Context, a VerificationAttempt) error {
	if !passwordConfirmationPurpose(a.Purpose) {
		return pkgerrors.ErrConflict
	}
	_, err := s.consumeVerification(ctx, a, true)
	return err
}
